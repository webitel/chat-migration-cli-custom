package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid
	"github.com/jackc/pgx/v5"

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

	botByType, err := c.getBotByType(ctx)
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

		liveThreads, err := c.findLiveDirectThreads(ctx, tx, groupedConversations, botByType)
		if err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		for _, conversation := range groupedConversations {
			threadID, live := liveThreads[conversation]
			if !live {
				converted := convertGroupedConversationToThread(conversation)
				threadID = converted.ID
				threads = append(threads, converted)
			}

			for _, convID := range conversation.ConvIDs {
				migrationRows = append(migrationRows, &modelnew.MigrationRow{
					ID:         uuid.Must(uuid.NewV7()),
					EntityType: modelnew.EntityTypeConversationThread,
					OldID:      convID.String(),
					NewID:      threadID,
					DomainID:   conversation.DomainID,
				})
			}

			migrationRows = append(migrationRows, &modelnew.MigrationRow{
				ID:         uuid.Must(uuid.NewV7()),
				EntityType: modelnew.EntityTypeTypeAndInitiatorIDToThread,
				OldID:      buildTypeAndInitiatorIDToThreadOldID(conversation.ClientType, conversation.Initiator),
				NewID:      threadID,
				DomainID:   conversation.DomainID,
			})
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

	botByType, err := c.getBotByType(ctx)
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

		liveThreads, err := c.findLiveDirectThreads(ctx, tx, groupedConversations, botByType)
		if err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		for _, conversation := range groupedConversations {
			threadID, live := liveThreads[conversation]
			if !live {
				converted := convertGroupedConversationToThread(conversation)
				threadID = converted.ID
				threads = append(threads, converted)
			}

			for _, convID := range conversation.ConvIDs {
				migrationRows = append(migrationRows, &modelnew.MigrationRow{
					ID:         uuid.Must(uuid.NewV7()),
					EntityType: modelnew.EntityTypeConversationThread,
					OldID:      convID.String(),
					NewID:      threadID,
					DomainID:   conversation.DomainID,
				})
			}

			syncExtraKey := newThreadAfterSyncExtraKey
			migrationRows = append(migrationRows, &modelnew.MigrationRow{
				ID:         uuid.Must(uuid.NewV7()),
				EntityType: modelnew.EntityTypeTypeAndInitiatorIDToThread,
				OldID:      buildTypeAndInitiatorIDToThreadOldID(conversation.ClientType, conversation.Initiator),
				NewID:      threadID,
				DomainID:   conversation.DomainID,
				ExtraKey:   &syncExtraKey,
			})
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

func (c *Converter) findLiveDirectThreads(ctx context.Context, tx pgx.Tx, conversations []*old.GroupedConversation, botByType map[string]*modelnew.BotTypeMapping) (map[*old.GroupedConversation]uuid.UUID, error) {
	initiatorIDs := make([]string, 0, len(conversations))
	for _, conv := range conversations {
		initiatorIDs = append(initiatorIDs, strconv.Itoa(conv.Initiator))
	}

	contactRows, err := c.resolver.ResolveMigrationRows(ctx, tx, &modelnew.MigrationRowFilters{
		Type:   []modelnew.EntityType{modelnew.EntityTypeClientContact},
		OldIDs: initiatorIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve initiator contacts: %w", err)
	}

	type contactKey struct {
		domainID int
		oldID    string
	}

	contacts := make(map[contactKey]uuid.UUID, len(contactRows))
	for _, row := range contactRows {
		contacts[contactKey{domainID: row.DomainID, oldID: row.OldID}] = row.NewID
	}

	pairs := make([]modelnew.DirectPair, 0, len(conversations))
	pairOf := make(map[*old.GroupedConversation]modelnew.DirectPair, len(conversations))

	for _, conv := range conversations {
		bot, ok := botByType[conv.ClientType]
		if !ok {
			continue
		}

		clientID, ok := contacts[contactKey{domainID: conv.DomainID, oldID: strconv.Itoa(conv.Initiator)}]
		if !ok {
			continue
		}

		pair := modelnew.DirectPair{DomainID: conv.DomainID, ClientID: clientID, BotID: bot.NewBotID}
		pairs = append(pairs, pair)
		pairOf[conv] = pair
	}

	threads, err := c.newDB.ThreadStore().FindLiveDirectThreads(ctx, tx, pairs)
	if err != nil {
		return nil, err
	}

	live := make(map[*old.GroupedConversation]uuid.UUID, len(threads))

	for conv, pair := range pairOf {
		if threadID, ok := threads[pair]; ok {
			live[conv] = threadID

			c.log.Info("conversation joins existing direct thread",
				"thread_id", threadID,
				"initiator", conv.Initiator,
				"client_type", conv.ClientType,
				"domain_id", conv.DomainID,
			)
		}
	}

	return live, nil
}
