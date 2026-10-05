package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
	"github.com/webitel/chat-migration-cli-custom/internal/model/old"
)

const (
	newThreadAfterSyncExtraKey = "sync_new"
)

func (c *Converter) MigrateConversations(ctx context.Context) error {
	const perPage = 1000
	c.log.Debug("starting conversations migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx); err != nil {
		return err
	}

	completedSteps, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
	if err != nil {
		return err
	}
	deps := []string{StepClientsToContacts, StepBotsToContacts}
	if c.migratePortalClients {
		deps = append(deps, StepPortalClientsToContacts)
	}
	for _, dep := range deps {
		if _, ok := completedSteps[dep]; !ok {
			return fmt.Errorf("step %q requires step %q to be completed first", StepConversations, dep)
		}
	}

	flowIDs, err := c.getConversationFlowIDs(ctx)
	if err != nil {
		return err
	}

	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return err
	}

	if err := c.newDB.MigrationStore().MarkStepInProgress(ctx, c.sessionID, StepConversations); err != nil {
		return err
	}

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, StepConversations, 0, cause.Error())
		return cause
	}

	lastInitiator, lastFlowID := 0, 0
	for {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return fail(err)
		}

		groupedConversations, err := c.oldDB.ConversationStore().GetGroupedConversationsByUsersAndFlowFromDate(ctx, lastInitiator, lastFlowID, perPage, fromDate, toDate, flowIDs)
		if err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}
		if len(groupedConversations) == 0 {
			tx.Rollback(ctx)
			break
		}
		c.log.Debug("conversations page fetched", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID, "count", len(groupedConversations))

		var (
			threads       []*modelnew.Thread
			migrationRows []*modelnew.MigrationRow
		)
		for _, conversation := range groupedConversations {
			converted := convertGroupedConversationToThread(conversation)
			for _, convID := range conversation.ConvIDs {
				migrationRows = append(migrationRows, &modelnew.MigrationRow{
					ID:         uuid.Must(uuid.NewV7()),
					EntityType: modelnew.EntityTypeConversationThread,
					OldID:      convID.String(),
					NewID:      converted.ID,
					DomainID:   conversation.DomainID,
				})
			}
			migrationRows = append(migrationRows, &modelnew.MigrationRow{
				ID:         uuid.Must(uuid.NewV7()),
				EntityType: modelnew.EntityTypeFlowIDAndInitiatorIDToThread,
				OldID:      buildFlowIDAndInitiatorIdToThreadOldID(conversation.FlowID, conversation.Initiator),
				NewID:      converted.ID,
				DomainID:   conversation.DomainID,
			})
			threads = append(threads, converted)
		}

		if err := c.newDB.ThreadStore().InsertThreads(ctx, tx, threads); err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}
		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}

		// advance cursor to the max group on this page (result row order is not guaranteed)
		var maxInitiator, maxFlowID int
		for _, conv := range groupedConversations {
			if conv.Initiator > maxInitiator || (conv.Initiator == maxInitiator && conv.FlowID > maxFlowID) {
				maxInitiator = conv.Initiator
				maxFlowID = conv.FlowID
			}
		}
		lastInitiator, lastFlowID = maxInitiator, maxFlowID

		if err := tx.Commit(ctx); err != nil {
			return fail(err)
		}

		c.log.Debug("conversations page committed", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID, "conversations", len(groupedConversations))
		c.addRecordsMigrated(len(threads))

		if len(groupedConversations) < perPage {
			break
		}
	}

	return nil
}

func buildFlowIDAndInitiatorIdToThreadOldID(flowID, initiatorID int) string {
	return strconv.Itoa(flowID) + "_" + strconv.Itoa(initiatorID)
}

func deconstructFlowIDAndInitiatorId(recordedID string) (flowID, initiatorID int) {
	parts := strings.Split(recordedID, "_")
	if len(parts) != 2 {
		return 0, 0
	}
	flowID, _ = strconv.Atoi(parts[0])
	initiatorID, _ = strconv.Atoi(parts[1])
	return
}

func (c *Converter) MigrateConversationsSyncMode(ctx context.Context) error {
	const (
		perPage  = 1000
		stepName = SyncStepConversations
	)
	c.log.Debug("starting conversations migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx); err != nil {
		return err
	}

	completedSteps, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
	if err != nil {
		return err
	}
	syncDeps := []string{SyncStepClientsToContacts}
	if c.migratePortalClients {
		syncDeps = append(syncDeps, SyncStepPortalClientsToContacts)
	}
	for _, dep := range syncDeps {
		if _, ok := completedSteps[dep]; !ok {
			return fmt.Errorf("step %q requires step %q to be completed first", stepName, dep)
		}
	}

	flowIDs, err := c.getConversationFlowIDs(ctx)
	if err != nil {
		return err
	}

	if err := c.newDB.MigrationStore().MarkStepInProgress(ctx, c.sessionID, stepName); err != nil {
		return err
	}

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, stepName, 0, cause.Error())
		return cause
	}

	// Unconditional: since the step no longer resumes, every invocation is a
	// fresh one -- there's no longer a "resuming this same run" case where
	// nullifying now would wipe tags this run already produced. Clear the
	// tag left by the last successfully completed sync run before tagging
	// this run's newly created threads.
	nullifyTx, err := c.newDB.Pool().Begin(ctx)
	if err != nil {
		return fail(err)
	}
	if err := c.newDB.MigrationStore().NullifyMigrationRowsExtraKey(ctx, nullifyTx, newThreadAfterSyncExtraKey, string(modelnew.EntityTypeFlowIDAndInitiatorIDToThread)); err != nil {
		nullifyTx.Rollback(ctx)
		return fail(err)
	}
	if err := nullifyTx.Commit(ctx); err != nil {
		return fail(err)
	}

	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return fail(err)
	}

	lastInitiator, lastFlowID := 0, 0
	for {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return fail(err)
		}

		groupedConversations, err := c.oldDB.ConversationStore().GetGroupedConversationsByUsersAndFlowFromDate(ctx, lastInitiator, lastFlowID, perPage, fromDate, toDate, flowIDs)
		if err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}
		if len(groupedConversations) == 0 {
			tx.Rollback(ctx)
			break
		}
		originalCount := len(groupedConversations)
		c.log.Debug("conversations page fetched", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID, "count", originalCount)

		// compute the max cursor from the originally fetched page, before the
		// already-migrated dedup loop below mutates groupedConversations; result row
		// order is not guaranteed, so take the max rather than the last element.
		var maxInitiator, maxFlowID int
		for _, conv := range groupedConversations {
			if conv.Initiator > maxInitiator || (conv.Initiator == maxInitiator && conv.FlowID > maxFlowID) {
				maxInitiator = conv.Initiator
				maxFlowID = conv.FlowID
			}
		}

		var (
			threads       []*modelnew.Thread
			migrationRows []*modelnew.MigrationRow

			idsToCheck []string
		)
		for _, conv := range groupedConversations {
			idsToCheck = append(idsToCheck, buildFlowIDAndInitiatorIdToThreadOldID(conv.FlowID, conv.Initiator))
		}

		alreadyMigratedThreads, err := c.newDB.MigrationStore().GetMigrationRows(ctx, tx, &modelnew.MigrationRowFilters{
			OldIDs: idsToCheck,
			Type:   []modelnew.EntityType{modelnew.EntityTypeFlowIDAndInitiatorIDToThread},
		})
		if err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}

		for _, thread := range alreadyMigratedThreads {
			var (
				j     int
				found bool
			)
			flowID, initiatorID := deconstructFlowIDAndInitiatorId(thread.OldID)
			for i, conv := range groupedConversations {
				if flowID == conv.FlowID && initiatorID == conv.Initiator && thread.DomainID == conv.DomainID {
					for _, convID := range conv.ConvIDs {
						migrationRows = append(migrationRows, &modelnew.MigrationRow{
							ID:         uuid.Must(uuid.NewV7()),
							EntityType: modelnew.EntityTypeConversationThread,
							OldID:      convID.String(),
							NewID:      thread.NewID,
							DomainID:   thread.DomainID,
						})
					}

					j = i
					found = true
					break
				}
			}
			if found {
				groupedConversations = append(groupedConversations[:j], groupedConversations[j+1:]...)
			}

		}

		for _, conversation := range groupedConversations {
			converted := convertGroupedConversationToThread(conversation)
			for _, convID := range conversation.ConvIDs {
				migrationRows = append(migrationRows, &modelnew.MigrationRow{
					ID:         uuid.Must(uuid.NewV7()),
					EntityType: modelnew.EntityTypeConversationThread,
					OldID:      convID.String(),
					NewID:      converted.ID,
					DomainID:   conversation.DomainID,
				})
			}
			syncExtraKey := newThreadAfterSyncExtraKey
			migrationRows = append(migrationRows, &modelnew.MigrationRow{
				ID:         uuid.Must(uuid.NewV7()),
				EntityType: modelnew.EntityTypeFlowIDAndInitiatorIDToThread,
				OldID:      buildFlowIDAndInitiatorIdToThreadOldID(conversation.FlowID, conversation.Initiator),
				NewID:      converted.ID,
				DomainID:   conversation.DomainID,
				ExtraKey:   &syncExtraKey,
			})
			threads = append(threads, converted)
		}

		if err := c.newDB.ThreadStore().InsertThreads(ctx, tx, threads); err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}
		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}

		// advance cursor to the max group on this page (computed above, before dedup)
		lastInitiator, lastFlowID = maxInitiator, maxFlowID

		if err := tx.Commit(ctx); err != nil {
			return fail(err)
		}

		c.log.Debug("conversations page committed", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID, "conversations", originalCount)
		c.addRecordsMigrated(len(threads))

		if originalCount < perPage {
			break
		}
	}
	return nil
}

func convertGroupedConversationToThread(groupedConversation *old.GroupedConversation) *modelnew.Thread {
	return &modelnew.Thread{
		ID:        uuid.Must(uuid.NewV7()),
		DomainID:  groupedConversation.DomainID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Subject:   groupedConversation.Title,
		Kind:      modelnew.ThreadDirect,
	}
}
