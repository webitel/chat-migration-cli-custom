package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid

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

	clientTypes, err := c.getConversationClientTypes(ctx)
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

	lastInitiator := 0

	for {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return fail(err)
		}

		groupedConversations, err := c.oldDB.ConversationStore().GetGroupedConversationsByInitiatorFromDate(ctx, lastInitiator, perPage, fromDate, toDate, flowIDs, clientTypes)
		if err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		if len(groupedConversations) == 0 {
			_ = tx.Rollback(ctx)

			break
		}

		c.log.Debug("conversations page fetched", "lastInitiator", lastInitiator, "count", len(groupedConversations))

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
				EntityType: modelnew.EntityTypeTypeAndInitiatorIDToThread,
				OldID:      buildTypeAndInitiatorIDToThreadOldID(conversation.ClientType, conversation.Initiator),
				NewID:      converted.ID,
				DomainID:   conversation.DomainID,
			})
			threads = append(threads, converted)
		}

		if err := c.newDB.ThreadStore().InsertThreads(ctx, tx, threads); err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		// advance cursor to the max group on this page (result row order is not guaranteed)
		var maxInitiator int
		for _, conv := range groupedConversations {
			if conv.Initiator > maxInitiator {
				maxInitiator = conv.Initiator
			}
		}

		lastInitiator = maxInitiator

		if err := tx.Commit(ctx); err != nil {
			return fail(err)
		}

		c.log.Debug("conversations page committed", "lastInitiator", lastInitiator, "conversations", len(groupedConversations))
		c.addRecordsMigrated(len(threads))

		if len(groupedConversations) < perPage {
			break
		}
	}

	return nil
}

func buildTypeAndInitiatorIDToThreadOldID(clientType string, initiatorID int) string {
	return clientType + "_" + strconv.Itoa(initiatorID)
}

// deconstructTypeAndInitiatorID splits a "<type>_<initiator>" key on the last
// "_" -- the client type may itself contain "_", the initiator id never does.
func deconstructTypeAndInitiatorID(recordedID string) (clientType string, initiatorID int) {
	i := strings.LastIndex(recordedID, "_")
	if i < 0 {
		return "", 0
	}

	initiatorID, _ = strconv.Atoi(recordedID[i+1:])

	return recordedID[:i], initiatorID
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

	clientTypes, err := c.getConversationClientTypes(ctx)
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

	if err := c.newDB.MigrationStore().NullifyMigrationRowsExtraKey(ctx, nullifyTx, newThreadAfterSyncExtraKey, string(modelnew.EntityTypeTypeAndInitiatorIDToThread)); err != nil {
		_ = nullifyTx.Rollback(ctx)

		return fail(err)
	}

	if err := nullifyTx.Commit(ctx); err != nil {
		return fail(err)
	}

	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return fail(err)
	}

	lastInitiator := 0

	for {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return fail(err)
		}

		groupedConversations, err := c.oldDB.ConversationStore().GetGroupedConversationsByInitiatorFromDate(ctx, lastInitiator, perPage, fromDate, toDate, flowIDs, clientTypes)
		if err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		if len(groupedConversations) == 0 {
			_ = tx.Rollback(ctx)

			break
		}

		originalCount := len(groupedConversations)
		c.log.Debug("conversations page fetched", "lastInitiator", lastInitiator, "count", originalCount)

		// compute the max cursor from the originally fetched page, before the
		// already-migrated dedup loop below mutates groupedConversations; result row
		// order is not guaranteed, so take the max rather than the last element.
		var maxInitiator int
		for _, conv := range groupedConversations {
			if conv.Initiator > maxInitiator {
				maxInitiator = conv.Initiator
			}
		}

		var (
			threads       []*modelnew.Thread
			migrationRows []*modelnew.MigrationRow

			idsToCheck []string
		)
		for _, conv := range groupedConversations {
			idsToCheck = append(idsToCheck, buildTypeAndInitiatorIDToThreadOldID(conv.ClientType, conv.Initiator))
		}

		alreadyMigratedThreads, err := c.newDB.MigrationStore().GetMigrationRows(ctx, tx, &modelnew.MigrationRowFilters{
			OldIDs: idsToCheck,
			Type:   []modelnew.EntityType{modelnew.EntityTypeTypeAndInitiatorIDToThread},
		})
		if err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		for _, thread := range alreadyMigratedThreads {
			var (
				j     int
				found bool
			)

			clientType, initiatorID := deconstructTypeAndInitiatorID(thread.OldID)
			for i, conv := range groupedConversations {
				if clientType != conv.ClientType || initiatorID != conv.Initiator || thread.DomainID != conv.DomainID {
					continue
				}

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
				EntityType: modelnew.EntityTypeTypeAndInitiatorIDToThread,
				OldID:      buildTypeAndInitiatorIDToThreadOldID(conversation.ClientType, conversation.Initiator),
				NewID:      converted.ID,
				DomainID:   conversation.DomainID,
				ExtraKey:   &syncExtraKey,
			})
			threads = append(threads, converted)
		}

		if err := c.newDB.ThreadStore().InsertThreads(ctx, tx, threads); err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		// advance cursor to the max group on this page (computed above, before dedup)
		lastInitiator = maxInitiator

		if err := tx.Commit(ctx); err != nil {
			return fail(err)
		}

		c.log.Debug("conversations page committed", "lastInitiator", lastInitiator, "conversations", originalCount)
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
