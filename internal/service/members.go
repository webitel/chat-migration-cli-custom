package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
	"github.com/webitel/chat-migration-cli-custom/internal/model/old"
)

func (c *Converter) MigrateMembers(ctx context.Context) error {
	var (
		perPage = 1000
	)
	c.log.Debug("starting members migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx); err != nil {
		return err
	}

	completedSteps, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
	if err != nil {
		return err
	}
	deps := []string{StepClientsToContacts, StepBotsToContacts, StepConversations}
	if c.migratePortalClients {
		deps = append(deps, StepPortalClientsToContacts)
	}
	for _, dep := range deps {
		if _, ok := completedSteps[dep]; !ok {
			return fmt.Errorf("step %q requires step %q to be completed first", StepMembers, dep)
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

	if err := c.newDB.MigrationStore().MarkStepInProgress(ctx, c.sessionID, StepMembers); err != nil {
		return err
	}

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, StepMembers, 0, cause.Error())
		return cause
	}

	lastInitiator, lastFlowID := 0, 0
	for {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return fail(err)
		}

		var (
			threadDialogs  []*modelnew.ThreadDialog
			migrationRows  []*modelnew.MigrationRow
			threadSettings []*modelnew.DirectSettings
		)
		groupedConversations, err := c.oldDB.ConversationStore().GetGroupedConversationsByUsersAndFlowFromDate(ctx, lastInitiator, lastFlowID, perPage, fromDate, toDate, flowIDs)
		if err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}
		if len(groupedConversations) == 0 {
			tx.Rollback(ctx)
			break
		}
		c.log.Debug("members page fetched", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID, "count", len(groupedConversations))
		for _, groupedConv := range groupedConversations {
			if len(groupedConv.ConvIDs) == 0 {
				c.log.Warn("grouped conversation has no conv IDs, skipping",
					"initiator", groupedConv.Initiator,
					"flow_id", groupedConv.FlowID,
				)
				continue
			}
			thread, err := c.resolver.ResolveMigrationRow(ctx, tx, modelnew.EntityTypeConversationThread, groupedConv.ConvIDs[0].String(), nil, groupedConv.DomainID)
			if err != nil {
				tx.Rollback(ctx)
				return fail(errors.Join(errors.New("failed to resolve migration row for conversation thread "+groupedConv.ConvIDs[0].String()), err))
			}
			dialogs, settings, rows, err := c.buildThreadDialogsFromConversation(ctx, tx, groupedConv, thread.NewID)
			if err != nil {
				tx.Rollback(ctx)
				return fail(errors.Join(errors.New("failed to build thread dialogs from conversation"), err))
			}
			threadDialogs = append(threadDialogs, dialogs...)
			migrationRows = append(migrationRows, rows...)
			threadSettings = append(threadSettings, settings...)
		}
		if err := c.newDB.ThreadDialogStore().InsertThreadDialogs(ctx, tx, threadDialogs); err != nil {
			tx.Rollback(ctx)
			return fail(errors.Join(errors.New("failed to insert thread dialogs"), err))
		}
		if err := c.newDB.DirectSettingsStore().InsertDirectSettings(ctx, tx, threadSettings); err != nil {
			tx.Rollback(ctx)
			return fail(errors.Join(errors.New("failed to insert direct settings"), err))
		}
		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			tx.Rollback(ctx)
			return fail(errors.Join(errors.New("failed to insert migration rows for thread dialogs"), err))
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

		c.log.Debug("members page committed", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID, "conversations", len(groupedConversations))
		c.addRecordsMigrated(len(threadDialogs))

		if len(groupedConversations) < perPage {
			break
		}
	}
	return nil
}

func (c *Converter) MigrateMembersSyncMode(ctx context.Context) error {
	var (
		perPage = 1000
	)
	c.log.Debug("starting members migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx); err != nil {
		return err
	}

	completedSteps, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
	if err != nil {
		return err
	}
	deps := []string{SyncStepClientsToContacts, SyncStepConversations}
	if c.migratePortalClients {
		deps = append(deps, SyncStepPortalClientsToContacts)
	}
	for _, dep := range deps {
		if _, ok := completedSteps[dep]; !ok {
			return fmt.Errorf("step %q requires step %q to be completed first", SyncStepMembers, dep)
		}
	}

	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return err
	}

	flowIDs, err := c.getConversationFlowIDs(ctx)
	if err != nil {
		return err
	}

	if err := c.newDB.MigrationStore().MarkStepInProgress(ctx, c.sessionID, SyncStepMembers); err != nil {
		return err
	}

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, SyncStepMembers, 0, cause.Error())
		return cause
	}

	lastInitiator, lastFlowID := 0, 0
	for {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return fail(err)
		}

		var (
			threadDialogs  []*modelnew.ThreadDialog
			threadSettings []*modelnew.DirectSettings
			migrationRows  []*modelnew.MigrationRow
		)
		groupedConversations, err := c.oldDB.ConversationStore().GetGroupedConversationsByUsersAndFlowFromDate(ctx, lastInitiator, lastFlowID, perPage, fromDate, toDate, flowIDs)
		if err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}
		if len(groupedConversations) == 0 {
			tx.Rollback(ctx)
			break
		}

		c.log.Debug("members page fetched", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID, "count", len(groupedConversations))

		for _, groupedConv := range groupedConversations {
			oldID := buildFlowIDAndInitiatorIdToThreadOldID(groupedConv.FlowID, groupedConv.Initiator)
			thread, err := c.newDB.MigrationStore().GetMigrationRow(ctx, tx, &modelnew.MigrationRowFilters{
				OldIDs:   []string{oldID},
				Type:     []modelnew.EntityType{modelnew.EntityTypeFlowIDAndInitiatorIDToThread},
				DomainID: groupedConv.DomainID,
			})
			if err != nil {
				tx.Rollback(ctx)
				return fail(errors.Join(errors.New("failed to resolve migration row for conversation originators pair "+oldID), err))
			}
			var (
				dialogs                []*modelnew.ThreadDialog
				settings               []*modelnew.DirectSettings
				rows                   []*modelnew.MigrationRow
				buildThreadDialogsFunc = c.buildInternalUsersThreadDialogs
			)
			if thread.ExtraKey != nil && *thread.ExtraKey == newThreadAfterSyncExtraKey {
				buildThreadDialogsFunc = c.buildThreadDialogsFromConversation
			}

			dialogs, settings, rows, err = buildThreadDialogsFunc(ctx, tx, groupedConv, thread.NewID)
			if err != nil {
				tx.Rollback(ctx)
				return fail(errors.Join(errors.New("failed to build thread dialogs from conversation"), err))
			}
			threadDialogs = append(threadDialogs, dialogs...)
			migrationRows = append(migrationRows, rows...)
			threadSettings = append(threadSettings, settings...)
		}

		if err := c.newDB.ThreadDialogStore().InsertThreadDialogs(ctx, tx, threadDialogs); err != nil {
			tx.Rollback(ctx)
			return fail(errors.Join(errors.New("failed to insert thread dialogs"), err))
		}

		if err := c.newDB.DirectSettingsStore().InsertDirectSettings(ctx, tx, threadSettings); err != nil {
			tx.Rollback(ctx)
			return fail(errors.Join(errors.New("failed to insert direct settings"), err))
		}

		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			tx.Rollback(ctx)
			return fail(errors.Join(errors.New("failed to insert migration rows for thread dialogs"), err))
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

		c.log.Debug("members page committed", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID, "conversations", len(groupedConversations))
		c.addRecordsMigrated(len(threadDialogs))

		if len(groupedConversations) < perPage {
			break
		}
	}
	return nil
}

func (c *Converter) buildThreadDialogsFromConversation(ctx context.Context, tx pgx.Tx, conversation *old.GroupedConversation, newThreadID uuid.UUID) ([]*modelnew.ThreadDialog, []*modelnew.DirectSettings, []*modelnew.MigrationRow, error) {
	var (
		threadDialogs  []*modelnew.ThreadDialog
		threadSettings []*modelnew.DirectSettings
		migrationRows  []*modelnew.MigrationRow
	)

	ownerDialogs, ownerSettings, ownerRows, err := c.buildOwnerThreadDialogFromConversation(ctx, tx, conversation, newThreadID)
	if err != nil {
		return nil, nil, nil, errors.Join(errors.New("failed to build owner thread dialog from conversation"), err)
	}

	dialogs, settings, rows, err := c.buildInternalUsersThreadDialogs(ctx, tx, conversation, newThreadID)
	if err != nil {
		return nil, nil, nil, errors.Join(errors.New("failed to build internal users thread dialogs"), err)
	}
	threadDialogs = append(threadDialogs, dialogs...)
	threadSettings = append(threadSettings, settings...)
	migrationRows = append(migrationRows, rows...)

	threadDialogs = append(threadDialogs, ownerDialogs...)
	threadSettings = append(threadSettings, ownerSettings...)
	migrationRows = append(migrationRows, ownerRows...)

	return threadDialogs, threadSettings, migrationRows, nil
}

func (c *Converter) buildOwnerThreadDialogFromConversation(ctx context.Context, tx pgx.Tx, conversation *old.GroupedConversation, newThreadID uuid.UUID) ([]*modelnew.ThreadDialog, []*modelnew.DirectSettings, []*modelnew.MigrationRow, error) {
	initiatorContact, err := c.resolver.ResolveMigrationRow(ctx, tx, modelnew.EntityTypeClientContact, strconv.Itoa(conversation.Initiator), nil, conversation.DomainID)
	if err != nil {
		c.log.Error("failed to resolve initiator contact", slog.String("error", err.Error()), slog.Int("initiator", conversation.Initiator), slog.Int("domain_id", conversation.DomainID))
		return nil, nil, nil, err
	}
	botContact, err := c.resolver.ResolveMigrationRow(ctx, tx, modelnew.EntityTypeBotContact, strconv.Itoa(conversation.FlowID), nil, conversation.DomainID)
	if err != nil {
		c.log.Error("failed to resolve bot contact", slog.String("error", err.Error()), slog.Int("flow_id", conversation.FlowID), slog.Int("domain_id", conversation.DomainID))
		return nil, nil, nil, err
	}

	now := time.Now()
	initiatorDialog := &modelnew.ThreadDialog{
		ID:         uuid.New(),
		ThreadID:   newThreadID,
		MemberID:   initiatorContact.NewID,
		ThreadRole: modelnew.RoleOwner,
		DomainID:   conversation.DomainID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	botDialog := &modelnew.ThreadDialog{
		ID:         uuid.New(),
		ThreadID:   newThreadID,
		MemberID:   botContact.NewID,
		ThreadRole: modelnew.RoleOwner,
		DomainID:   conversation.DomainID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	threadSettings := []*modelnew.DirectSettings{
		{
			ID:             uuid.New(),
			ThreadDialogID: initiatorDialog.ID,
			DomainID:       conversation.DomainID,
			Title:          conversation.Title,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             uuid.New(),
			ThreadDialogID: botDialog.ID,
			DomainID:       conversation.DomainID,
			Title:          conversation.Title,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}

	threadDialogs := []*modelnew.ThreadDialog{initiatorDialog, botDialog}
	newThreadIDStr := newThreadID.String()
	migrationRows := []*modelnew.MigrationRow{
		{
			ID:         uuid.New(),
			EntityType: modelnew.EntityTypeInitiatorChannelThreadDialog,
			OldID:      strconv.Itoa(conversation.Initiator),
			NewID:      initiatorDialog.ID,
			DomainID:   conversation.DomainID,
			ExtraKey:   &newThreadIDStr,
		},
		{
			ID:         uuid.New(),
			EntityType: modelnew.EntityTypeBotChannelThreadDialog,
			OldID:      strconv.Itoa(conversation.FlowID),
			NewID:      botDialog.ID,
			DomainID:   conversation.DomainID,
			ExtraKey:   &newThreadIDStr,
		},
	}

	return threadDialogs, threadSettings, migrationRows, nil
}

func (c *Converter) buildInternalUsersThreadDialogs(ctx context.Context, tx pgx.Tx, conversation *old.GroupedConversation, threadID uuid.UUID) ([]*modelnew.ThreadDialog, []*modelnew.DirectSettings, []*modelnew.MigrationRow, error) {
	var (
		threadDialogs  []*modelnew.ThreadDialog
		threadSettings []*modelnew.DirectSettings
		migrationRows  []*modelnew.MigrationRow
		webitelUserIDs []string
		now            = time.Now()
	)
	for _, user := range conversation.InternalUsers {
		webitelUserIDs = append(webitelUserIDs, strconv.Itoa(user.UserID))
	}
	contacts, err := c.newDB.ContactStore().GetByWebitelUserIDs(ctx, tx, webitelUserIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, user := range conversation.InternalUsers {
		var foundContact *modelnew.Contact
		for _, contact := range contacts {
			if contact.SubjectID == strconv.Itoa(user.UserID) {
				foundContact = contact
				break
			}
		}
		if foundContact == nil {
			foundContact, err = c.restoreWebitelUser(ctx, tx, user, threadID, conversation.DomainID)
			if err != nil {
				c.log.Warn("webitel user can't be restored, skipping", slog.Int("user_id", user.UserID), slog.String("thread_id", threadID.String()), slog.String("err", err.Error()))
				err = nil
				continue
			}
		}

		// Migration only ever processes closed conversations, so an internal
		// user's membership is always over by migration time. old_db may still
		// leave the channel's closed_at unset (the conversation was closed
		// without explicitly closing the channel) — fall back to now so
		// deleted_at is never NULL, matching idx_thread_dialog_member_unique
		// (unique on (member_id, thread_id) WHERE deleted_at IS NULL).
		closedAt := user.ClosedAt
		if closedAt.IsZero() {
			closedAt = now
		}
		threadDialog := &modelnew.ThreadDialog{
			ID:          uuid.New(),
			ThreadID:    threadID,
			MemberID:    foundContact.ID,
			ThreadRole:  modelnew.RoleMember,
			DomainID:    conversation.DomainID,
			CreatedAt:   user.CreatedAt,
			UpdatedAt:   closedAt,
			DeletedAt:   &closedAt,
			LeaveReason: user.LeaveReason,
		}
		threadDialogs = append(threadDialogs, threadDialog)

		threadSettings = append(threadSettings, &modelnew.DirectSettings{
			ID:             uuid.New(),
			ThreadDialogID: threadDialog.ID,
			DomainID:       conversation.DomainID,
			Title:          conversation.Title,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		threadIDStr := threadID.String()
		migrationRows = append(migrationRows, &modelnew.MigrationRow{
			ID:         uuid.New(),
			EntityType: modelnew.EntityTypeInternalChannelThreadDialog,
			OldID:      strconv.Itoa(user.UserID),
			NewID:      threadDialog.ID,
			DomainID:   conversation.DomainID,
			ExtraKey:   &threadIDStr,
		})
	}

	return threadDialogs, threadSettings, migrationRows, nil
}

func (c *Converter) restoreWebitelUser(ctx context.Context, tx pgx.Tx, user *old.ConversationUser, threadID uuid.UUID, domainID int) (*modelnew.Contact, error) {
	now := time.Now()
	name := "Customer service"
	if user.Name != nil {
		name = *user.Name
	}
	c.log.Warn("webitel user not found for conversation member, try to restore user",
		"user_id", user.UserID,
		"thread_id", threadID,
	)
	newContact := &modelnew.Contact{
		BaseModel: modelnew.BaseModel{
			ID:        uuid.New(),
			DomainID:  domainID,
			CreatedAt: now,
			UpdatedAt: now,
		},
		IssuerID:  "webitel",
		SubjectID: strconv.Itoa(user.UserID),
		Type:      "webitel",
		Name:      name,
		Username:  buildUsername(name, "webitel", strconv.Itoa(user.UserID)),
		IsBot:     false,
	}
	_, err := c.newDB.ContactStore().InsertContactsIgnoreConflicts(ctx, tx, []*modelnew.Contact{newContact})
	if err != nil {
		return nil, err
	}
	return newContact, nil
}
