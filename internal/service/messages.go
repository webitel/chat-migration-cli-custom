package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/jackc/pgx/v5"
	"github.com/rivo/uniseg"
	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
	modelold "github.com/webitel/chat-migration-cli-custom/internal/model/old"
)

func (c *Converter) MigrateMessages(ctx context.Context) error {
	const (
		perPage         = 1000
		insertChunkSize = 2000
	)
	c.log.Debug("starting messages migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx, StepMessages); err != nil {
		return err
	}

	completedSteps, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
	if err != nil {
		return err
	}
	deps := []string{StepClientsToContacts, StepBotsToContacts, StepConversations, StepMembers}
	if c.migratePortalClients {
		deps = append(deps, StepPortalClientsToContacts)
	}
	for _, dep := range deps {
		if _, ok := completedSteps[dep]; !ok {
			return fmt.Errorf("step %q requires step %q to be completed first", StepMessages, dep)
		}
	}

	lastInitiator, lastFlowID, err := c.newDB.MigrationStore().GetCursorProgress(ctx, StepMessages)
	if err != nil {
		return err
	}
	if lastInitiator > 0 || lastFlowID > 0 {
		c.log.Info("resuming messages migration", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID)
	}

	flowIDs, err := c.getConversationFlowIDs(ctx)
	if err != nil {
		return err
	}

	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return err
	}

	if err := c.newDB.MigrationStore().MarkStepInProgress(ctx, c.sessionID, StepMessages); err != nil {
		return err
	}

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, StepMessages, 0, cause.Error())
		return cause
	}

	for {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return fail(err)
		}

		threadIDToConv, err := c.getConversationMap(ctx, lastInitiator, lastFlowID, perPage, tx, fromDate, toDate, flowIDs)
		if err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}
		if len(threadIDToConv) == 0 {
			tx.Rollback(ctx)
			break
		}

		messages, files, _, err := c.migratePageMessages(ctx, tx, threadIDToConv)
		if err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}

		if err := c.insertChunked(ctx, tx, messages, files, insertChunkSize); err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}

		c.addRecordsMigrated(len(messages))

		// advance cursor to the last group on this page
		var maxInitiator, maxFlowID int
		for _, conv := range threadIDToConv {
			if conv.Initiator > maxInitiator || (conv.Initiator == maxInitiator && conv.FlowID > maxFlowID) {
				maxInitiator = conv.Initiator
				maxFlowID = conv.FlowID
			}
		}
		lastInitiator, lastFlowID = maxInitiator, maxFlowID

		if err := c.newDB.MigrationStore().SaveCursorProgressInTx(ctx, tx, c.sessionID, StepMessages, lastInitiator, lastFlowID); err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fail(err)
		}

		c.log.Debug("messages page committed", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID, "conversations", len(threadIDToConv))

		if len(threadIDToConv) < perPage {
			break
		}
	}

	return nil
}

func (c *Converter) MigrateMessagesSyncMode(ctx context.Context) error {
	const (
		perPage         = 1000
		insertChunkSize = 2000
		stepName        = SyncStepMessages
	)
	c.log.Debug("starting messages migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx, stepName); err != nil {
		return err
	}

	completedSteps, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
	if err != nil {
		return err
	}
	deps := []string{SyncStepClientsToContacts, SyncStepConversations, SyncStepMembers}
	if c.migratePortalClients {
		deps = append(deps, SyncStepPortalClientsToContacts)
	}
	for _, dep := range deps {
		if _, ok := completedSteps[dep]; !ok {
			return fmt.Errorf("step %q requires step %q to be completed first", stepName, dep)
		}
	}

	lastInitiator, lastFlowID, err := c.newDB.MigrationStore().GetCursorProgress(ctx, stepName)
	if err != nil {
		return err
	}
	if lastInitiator > 0 || lastFlowID > 0 {
		c.log.Info("resuming messages migration", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID)
	}

	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return err
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

	for {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return fail(err)
		}

		threadIDToConv, err := c.getConversationMapSyncMode(ctx, lastInitiator, lastFlowID, perPage, tx, fromDate, toDate, flowIDs)
		if err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}
		if len(threadIDToConv) == 0 {
			tx.Rollback(ctx)
			break
		}

		messages, files, migrationRows, err := c.migratePageMessages(ctx, tx, threadIDToConv)
		if err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}

		if err := c.insertChunked(ctx, tx, messages, files, insertChunkSize); err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}

		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}

		c.addRecordsMigrated(len(messages))

		// advance cursor to the last group on this page
		var maxInitiator, maxFlowID int
		for _, conv := range threadIDToConv {
			if conv.Initiator > maxInitiator || (conv.Initiator == maxInitiator && conv.FlowID > maxFlowID) {
				maxInitiator = conv.Initiator
				maxFlowID = conv.FlowID
			}
		}
		lastInitiator, lastFlowID = maxInitiator, maxFlowID

		if err := c.newDB.MigrationStore().SaveCursorProgressInTx(ctx, tx, c.sessionID, stepName, lastInitiator, lastFlowID); err != nil {
			tx.Rollback(ctx)
			return fail(err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fail(err)
		}

		c.log.Debug("messages page committed", "lastInitiator", lastInitiator, "lastFlowID", lastFlowID, "conversations", len(threadIDToConv))

		if len(threadIDToConv) < perPage {
			break
		}
	}

	return nil
}

func (c *Converter) insertChunked(ctx context.Context, tx pgx.Tx, messages []*modelnew.Message, files []*modelnew.MessageDocument, chunkSize int) error {
	for i := 0; i < len(messages); i += chunkSize {
		end := i + chunkSize
		if end > len(messages) {
			end = len(messages)
		}
		if err := c.newDB.MessageStore().CreateMessages(ctx, tx, messages[i:end]); err != nil {
			return err
		}
	}
	for i := 0; i < len(files); i += chunkSize {
		end := i + chunkSize
		if end > len(files) {
			end = len(files)
		}
		if err := c.newDB.MessageStore().CreateDocuments(ctx, tx, files[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Converter) getConversationMap(ctx context.Context, lastInitiator, lastFlowID, limit int, tx pgx.Tx, from, to time.Time, flowIDs []int32) (map[uuid.UUID]*modelold.GroupedConversation, error) {
	conversations, err := c.oldDB.ConversationStore().GetGroupedConversationsByUsersAndFlowFromDate(ctx, lastInitiator, lastFlowID, limit, from, to, flowIDs)
	if err != nil {
		return nil, err
	}
	var convIDs []string
	for _, conv := range conversations {
		for _, id := range conv.ConvIDs {
			convIDs = append(convIDs, id.String())
		}
	}
	threads, err := c.resolver.ResolveMigrationRows(ctx, tx, &modelnew.MigrationRowFilters{
		Type:   []modelnew.EntityType{modelnew.EntityTypeConversationThread},
		OldIDs: convIDs,
	})
	if err != nil {
		return nil, err
	}
	threadByOldID := make(map[string]*modelnew.MigrationRow, len(threads))
	for _, t := range threads {
		threadByOldID[t.OldID] = t
	}
	threadIDToConv := make(map[uuid.UUID]*modelold.GroupedConversation)
	for _, conv := range conversations {
		for _, convID := range conv.ConvIDs {
			if t, ok := threadByOldID[convID.String()]; ok {
				threadIDToConv[t.NewID] = conv
				break
			}
		}
	}
	return threadIDToConv, nil
}

func (c *Converter) getConversationMapSyncMode(ctx context.Context, lastInitiator, lastFlowID, limit int, tx pgx.Tx, from, to time.Time, flowIDs []int32) (map[uuid.UUID]*modelold.GroupedConversation, error) {
	conversations, err := c.oldDB.ConversationStore().GetGroupedConversationsByUsersAndFlowFromDate(ctx, lastInitiator, lastFlowID, limit, from, to, flowIDs)
	if err != nil {
		return nil, err
	}
	var convIDs []string
	for _, conv := range conversations {
		for _, id := range conv.ConvIDs {
			convIDs = append(convIDs, id.String())
		}
	}
	threads, err := c.resolver.ResolveMigrationRows(ctx, tx, &modelnew.MigrationRowFilters{
		Type:   []modelnew.EntityType{modelnew.EntityTypeConversationThread},
		OldIDs: convIDs,
	})
	if err != nil {
		return nil, err
	}
	threadByOldID := make(map[string]*modelnew.MigrationRow, len(threads))
	for _, t := range threads {
		threadByOldID[t.OldID] = t
	}
	threadIDToConv := make(map[uuid.UUID]*modelold.GroupedConversation)
	for _, conv := range conversations {
		for _, convID := range conv.ConvIDs {
			if t, ok := threadByOldID[convID.String()]; ok {
				threadIDToConv[t.NewID] = conv
				break
			}
		}
	}
	return threadIDToConv, nil
}

type pageSenderMaps struct {
	operatorContactsByID  map[string]uuid.UUID // userIDStr → contactID
	operatorMembersByKey  map[string]uuid.UUID // "userIDStr:threadIDStr" → memberID
	initiatorContactsByID map[string]uuid.UUID // userIDStr → contactID
	initiatorMembersByKey map[string]uuid.UUID // "userIDStr:threadIDStr" → memberID
	botContactsByID       map[string]uuid.UUID // botIDStr → contactID
	botMembersByKey       map[string]uuid.UUID // "botIDStr:threadIDStr" → memberID
}

func (c *Converter) migratePageMessages(ctx context.Context, tx pgx.Tx, threadIDToConv map[uuid.UUID]*modelold.GroupedConversation) ([]*modelnew.Message, []*modelnew.MessageDocument, []*modelnew.MigrationRow, error) {
	messagesByConvID, err := c.batchFetchMessages(ctx, threadIDToConv)
	if err != nil {
		return nil, nil, nil, err
	}
	senderMaps, err := c.buildPageSenderMaps(ctx, tx, threadIDToConv)
	if err != nil {
		return nil, nil, nil, err
	}

	var (
		allMessages      []*modelnew.Message
		allFiles         []*modelnew.MessageDocument
		allMigrationRows []*modelnew.MigrationRow
	)
	for threadID, conv := range threadIDToConv {
		var msgs []*modelold.Message
		for _, cid := range conv.ConvIDs {
			msgs = append(msgs, messagesByConvID[cid]...)
		}

		initiator, bot, operators := c.filterMessagesBySender(msgs)

		converted, files, migrationRows, err := c.convertOperatorMessagesFromMaps(threadID, senderMaps, operators, conv.DomainID)
		if err != nil {
			return nil, nil, nil, err
		}
		allMessages = append(allMessages, converted...)
		allFiles = append(allFiles, files...)
		allMigrationRows = append(allMigrationRows, migrationRows...)

		initiatorID := strconv.Itoa(conv.Initiator)
		contactID := senderMaps.initiatorContactsByID[initiatorID]
		memberID := senderMaps.initiatorMembersByKey[initiatorID+":"+threadID.String()]
		converted, files, migrationRows = c.buildMessagesForSender(threadID, contactID, memberID, initiator, conv.DomainID)
		allMessages = append(allMessages, converted...)
		allFiles = append(allFiles, files...)
		allMigrationRows = append(allMigrationRows, migrationRows...)

		botID := strconv.Itoa(conv.FlowID)
		contactID = senderMaps.botContactsByID[botID]
		memberID = senderMaps.botMembersByKey[botID+":"+threadID.String()]
		converted, files, migrationRows = c.buildMessagesForSender(threadID, contactID, memberID, bot, conv.DomainID)
		allMessages = append(allMessages, converted...)
		allFiles = append(allFiles, files...)
		allMigrationRows = append(allMigrationRows, migrationRows...)
	}
	return allMessages, allFiles, allMigrationRows, nil
}

func (c *Converter) batchFetchMessages(ctx context.Context, threadIDToConv map[uuid.UUID]*modelold.GroupedConversation) (map[uuid.UUID][]*modelold.Message, error) {
	var allConvIDs []uuid.UUID
	for _, conv := range threadIDToConv {
		allConvIDs = append(allConvIDs, conv.ConvIDs...)
	}
	msgs, err := c.oldDB.MessageStore().GetMessagesByConversationID(ctx, allConvIDs)
	if err != nil {
		return nil, err
	}
	byConvID := make(map[uuid.UUID][]*modelold.Message)
	for _, m := range msgs {
		byConvID[m.ConversationID] = append(byConvID[m.ConversationID], m)
	}
	return byConvID, nil
}

func (c *Converter) buildPageSenderMaps(ctx context.Context, tx pgx.Tx, threadIDToConv map[uuid.UUID]*modelold.GroupedConversation) (*pageSenderMaps, error) {
	var (
		operatorIDStrs  []string
		initiatorIDStrs []string
		botIDStrs       []string
		threadIDStrs    []string
		seenOps         = map[int]struct{}{}
		seenInits       = map[int]struct{}{}
		seenBots        = map[int]struct{}{}
		seenThreads     = map[string]struct{}{}
	)
	for threadID, conv := range threadIDToConv {
		tidStr := threadID.String()
		if _, ok := seenThreads[tidStr]; !ok {
			seenThreads[tidStr] = struct{}{}
			threadIDStrs = append(threadIDStrs, tidStr)
		}
		for _, u := range conv.InternalUsers {
			if _, ok := seenOps[u.UserID]; !ok {
				seenOps[u.UserID] = struct{}{}
				operatorIDStrs = append(operatorIDStrs, strconv.Itoa(u.UserID))
			}
		}
		if _, ok := seenInits[conv.Initiator]; !ok {
			seenInits[conv.Initiator] = struct{}{}
			initiatorIDStrs = append(initiatorIDStrs, strconv.Itoa(conv.Initiator))
		}
		if _, ok := seenBots[conv.FlowID]; !ok {
			seenBots[conv.FlowID] = struct{}{}
			botIDStrs = append(botIDStrs, strconv.Itoa(conv.FlowID))
		}
	}

	result := &pageSenderMaps{
		operatorContactsByID:  make(map[string]uuid.UUID),
		operatorMembersByKey:  make(map[string]uuid.UUID),
		initiatorContactsByID: make(map[string]uuid.UUID),
		initiatorMembersByKey: make(map[string]uuid.UUID),
		botContactsByID:       make(map[string]uuid.UUID),
		botMembersByKey:       make(map[string]uuid.UUID),
	}

	if len(operatorIDStrs) > 0 {
		contacts, err := c.newDB.ContactStore().GetByWebitelUserIDs(ctx, tx, operatorIDStrs)
		if err != nil {
			return nil, err
		}
		for _, ct := range contacts {
			result.operatorContactsByID[ct.SubjectID] = ct.ID
		}

		members, err := c.resolver.ResolveMigrationRows(ctx, tx, &modelnew.MigrationRowFilters{
			Type:      []modelnew.EntityType{modelnew.EntityTypeInternalChannelThreadDialog},
			OldIDs:    operatorIDStrs,
			ExtraKeys: threadIDStrs,
		})
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			result.operatorMembersByKey[m.OldID+":"+*m.ExtraKey] = m.NewID
		}
	}

	if len(initiatorIDStrs) > 0 {
		contacts, err := c.resolver.ResolveMigrationRows(ctx, tx, &modelnew.MigrationRowFilters{
			Type:   []modelnew.EntityType{modelnew.EntityTypeClientContact},
			OldIDs: initiatorIDStrs,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range contacts {
			result.initiatorContactsByID[r.OldID] = r.NewID
		}

		members, err := c.resolver.ResolveMigrationRows(ctx, tx, &modelnew.MigrationRowFilters{
			Type:      []modelnew.EntityType{modelnew.EntityTypeInitiatorChannelThreadDialog},
			OldIDs:    initiatorIDStrs,
			ExtraKeys: threadIDStrs,
		})
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			result.initiatorMembersByKey[m.OldID+":"+*m.ExtraKey] = m.NewID
		}
	}

	if len(botIDStrs) > 0 {
		contacts, err := c.resolver.ResolveMigrationRows(ctx, tx, &modelnew.MigrationRowFilters{
			Type:   []modelnew.EntityType{modelnew.EntityTypeBotContact},
			OldIDs: botIDStrs,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range contacts {
			result.botContactsByID[r.OldID] = r.NewID
		}

		members, err := c.resolver.ResolveMigrationRows(ctx, tx, &modelnew.MigrationRowFilters{
			Type:      []modelnew.EntityType{modelnew.EntityTypeBotChannelThreadDialog},
			OldIDs:    botIDStrs,
			ExtraKeys: threadIDStrs,
		})
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			result.botMembersByKey[m.OldID+":"+*m.ExtraKey] = m.NewID
		}
	}

	return result, nil
}

func (c *Converter) convertOperatorMessagesFromMaps(threadID uuid.UUID, maps *pageSenderMaps, messages []*modelold.Message, domainID int) ([]*modelnew.Message, []*modelnew.MessageDocument, []*modelnew.MigrationRow, error) {
	var (
		newMessages   []*modelnew.Message
		files         []*modelnew.MessageDocument
		migrationRows []*modelnew.MigrationRow
	)
	for _, oldMsg := range messages {
		if oldMsg.UserID == nil {
			continue
		}
		userIDStr := strconv.Itoa(*oldMsg.UserID)
		contactID := maps.operatorContactsByID[userIDStr]
		memberID := maps.operatorMembersByKey[userIDStr+":"+threadID.String()]
		msg, file, migrationRow := c.buildMessage(threadID, contactID, memberID, oldMsg, domainID)
		if file != nil {
			files = append(files, file)
		}
		if migrationRow != nil {
			migrationRows = append(migrationRows, migrationRow)
		}
		newMessages = append(newMessages, msg)
	}
	return newMessages, files, migrationRows, nil
}

func (c *Converter) buildMessagesForSender(threadID, senderID, memberID uuid.UUID, messages []*modelold.Message, domainID int) ([]*modelnew.Message, []*modelnew.MessageDocument, []*modelnew.MigrationRow) {
	var (
		newMessages   []*modelnew.Message
		files         []*modelnew.MessageDocument
		migrationRows []*modelnew.MigrationRow
	)
	for _, oldMsg := range messages {
		msg, file, migrationRow := c.buildMessage(threadID, senderID, memberID, oldMsg, domainID)
		if file != nil {
			files = append(files, file)
		}
		if migrationRow != nil {
			migrationRows = append(migrationRows, migrationRow)
		}
		newMessages = append(newMessages, msg)
	}
	return newMessages, files, migrationRows
}

// buildMessage also returns the chat_migration row mapping the old message
// id to the new one, for text/file messages only (sync mode's reconciliation
// needs it; system messages such as joined/closed aren't reconciled this
// way). The caller decides whether to actually persist it -- full mode
// discards it, see .md/enhancements/migration_steps/messages.md, "Добавить
// запись в таблицу chat_migration".
func (c *Converter) buildMessage(threadID, senderID, memberID uuid.UUID, oldMsg *modelold.Message, domainID int) (*modelnew.Message, *modelnew.MessageDocument, *modelnew.MigrationRow) {
	var body string
	if oldMsg.Text != nil {
		body = *oldMsg.Text
	}
	updatedAt := oldMsg.CreatedAt
	if oldMsg.UpdatedAt != nil {
		updatedAt = *oldMsg.UpdatedAt
	}
	messageType := c.convertMessageType(oldMsg.Type)
	newMsg := &modelnew.Message{
		ID:        uuid.Must(uuid.NewV7AtTime(oldMsg.CreatedAt)),
		ThreadID:  threadID,
		SenderID:  senderID,
		MemberID:  memberID,
		Type:      messageType,
		Body:      body,
		Metadata:  buildMetadata(body),
		CreatedAt: oldMsg.CreatedAt,
		UpdatedAt: updatedAt,
		DomainID:  int32(domainID),
	}
	if oldMsg.Content != nil && oldMsg.Content.Keyboard != nil {
		newMsg.Interactive = &modelnew.MessageInteractive{
			Kind: modelnew.InteractiveKind{
				Markup: c.convertButtons(oldMsg.Content.Keyboard),
			},
		}
	}
	var file *modelnew.MessageDocument
	if messageType == modelnew.MessageTypeFile {
		file = c.convertMessageDocument(newMsg.ID, oldMsg)
		attachments := &modelnew.MessageContent{Documents: true}
		if newMsg.Interactive != nil {
			newMsg.Interactive.Attachments = attachments
		} else {
			newMsg.Interactive = &modelnew.MessageInteractive{Attachments: attachments}
		}
	}

	var migrationRow *modelnew.MigrationRow
	if messageType == modelnew.MessageTypeText || messageType == modelnew.MessageTypeFile {
		migrationRow = &modelnew.MigrationRow{
			ID:         uuid.Must(uuid.NewV7()),
			EntityType: modelnew.EntityTypeMessage,
			OldID:      strconv.FormatInt(oldMsg.ID, 10),
			NewID:      newMsg.ID,
			DomainID:   domainID,
		}
	}

	return newMsg, file, migrationRow
}

func buildMetadata(text string) []byte {
	res := map[string]any{
		"entities":  nil,
		"graphemes": uniseg.GraphemeClusterCount(text),
	}
	encoded, _ := json.Marshal(res)
	return encoded

}

func (c *Converter) filterMessagesBySender(messages []*modelold.Message) (initiator []*modelold.Message, bot []*modelold.Message, operators []*modelold.Message) {
	for _, message := range messages {
		if message.Internal == nil {
			bot = append(bot, message)
			continue
		}
		if *message.Internal {
			operators = append(operators, message)
			continue
		}
		initiator = append(initiator, message)
	}
	return initiator, bot, operators
}

func (c *Converter) convertMessageType(oldType modelold.OldMessageType) modelnew.MessageType {
	switch oldType {
	case modelold.MessageText:
		return modelnew.MessageTypeText
	case modelold.MessageFile:
		return modelnew.MessageTypeFile
	case modelold.MessageJoined, modelold.MessageClosed:
		return modelnew.MessageTypeSystem
	default:
		return modelnew.MessageTypeText
	}
}

func (c *Converter) convertMessageDocument(messageID uuid.UUID, oldMsg *modelold.Message) *modelnew.MessageDocument {
	if oldMsg == nil {
		return nil
	}
	var res = modelnew.MessageDocument{
		ID:        uuid.Must(uuid.NewV7AtTime(oldMsg.CreatedAt)),
		MessageID: messageID,
		CreatedAt: oldMsg.CreatedAt,
	}
	if oldMsg.FileID != nil {
		res.FileID = *oldMsg.FileID
	}
	if oldMsg.FileName != nil {
		res.Name = *oldMsg.FileName
	}
	if oldMsg.FileType != nil {
		res.Mime = *oldMsg.FileType
	}
	if oldMsg.FileSize != nil {
		res.Size = *oldMsg.FileSize
	}
	if oldMsg.FileURL != nil {
		res.URL = *oldMsg.FileURL
	}
	return &res
}

func (c *Converter) convertButtons(previousReplyMarkup *modelold.ReplyMarkup) *modelnew.KeyboardButtonMarkup {
	if previousReplyMarkup == nil {
		return nil
	}
	result := make([]*modelnew.KeyboardButtonRow, len(previousReplyMarkup.Buttons))
	for i, row := range previousReplyMarkup.Buttons {
		result[i] = &modelnew.KeyboardButtonRow{}
		for _, button := range row.Row {
			result[i].Buttons = append(result[i].Buttons, c.convertButton(button))
		}
	}
	return &modelnew.KeyboardButtonMarkup{Rows: result}
}

func (c *Converter) convertButton(button *modelold.Button) *modelnew.KeyboardButton {
	return &modelnew.KeyboardButton{
		Label: button.Text,
		URL:   button.Url,
		Data:  button.Code,
		Metadata: map[string]any{
			"share": button.Share,
		},
	}
}
