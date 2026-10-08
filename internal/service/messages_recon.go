package service

import "context"

// messagesReconSourceSQL and messagesReconTargetSQL implement the full-mode
// reconciliation described in the "Full" section of
// .md/enhancements/migration_steps/messages.recon.md. Like members, this
// check is not scoped to the current session or migration window --
// im_message.messages has no session_id column, so there is no way to tell
// this run's inserted messages apart from earlier runs'. Sync mode needs its
// own session-scoped queries/checks -- see messagesSyncReconSourceSQL below.
const (
	messagesReconSourceSQL = `
SELECT
    COUNT(*) AS "source.total_messages_count",
    COUNT(*) FILTER (WHERE m."type" = 'text') AS "source.text_messages_count",
    COUNT(*) FILTER (WHERE m."type" = 'file') AS "source.file_messages_count",
    COUNT(*) FILTER (WHERE m.channel_id IS NULL) AS "source.bot_messages_count",
    COUNT(*) FILTER (WHERE m.channel_id IS NOT NULL AND ch."type" = 'webitel') AS "source.agent_messages_count",
    COUNT(*) FILTER (WHERE m.channel_id IS NOT NULL AND ch."type" IN ('portal', 'facebook')) AS "source.user_messages_count"
FROM chat.message m
JOIN chat.conversation conv ON conv.id = m.conversation_id
LEFT JOIN chat.channel ch ON ch.id = m.channel_id
WHERE conv.closed_at IS NOT NULL
  AND conv.props ->> 'flow' IS NOT NULL
  AND (conv.props ->> 'flow')::int8 = ANY(:flow_ids)
  AND EXISTS (
      SELECT 1
      FROM chat.channel initiator
      JOIN chat.client cl ON cl.id = initiator.user_id
      WHERE initiator.conversation_id = conv.id
        AND NOT initiator.internal
        AND COALESCE(cl.type, 'webchat') = ANY(:types::text[])
  )
  AND m.text IS DISTINCT FROM 'start'`

	messagesReconTargetSQL = `
SELECT
    COUNT(*) AS "target.total_messages_count",
    COUNT(*) FILTER (WHERE m."type" = 1) AS "target.text_messages_count",
    COUNT(*) FILTER (WHERE m."type" = 2) AS "target.file_messages_count",
    COUNT(*) FILTER (WHERE c.is_bot IS TRUE) AS "target.bot_messages_count",
    COUNT(*) FILTER (WHERE c.is_bot IS NOT TRUE AND c."type" = 'webitel') AS "target.agent_messages_count",
    COUNT(*) FILTER (WHERE c.is_bot IS NOT TRUE AND c."type" IS DISTINCT FROM 'webitel') AS "target.user_messages_count"
FROM im_message.messages m
LEFT JOIN im_contact.contact c ON c.id = m.sender_id`
)

var messagesReconChecks = []ReconciliationCheck{
	{Left: "source.total_messages_count", Right: "target.total_messages_count", Op: "="},
	{Left: "source.text_messages_count", Right: "target.text_messages_count", Op: "="},
	{Left: "source.file_messages_count", Right: "target.file_messages_count", Op: "="},
	{Left: "source.bot_messages_count", Right: "target.bot_messages_count", Op: "="},
	{Left: "source.agent_messages_count", Right: "target.agent_messages_count", Op: "="},
	{Left: "source.user_messages_count", Right: "target.user_messages_count", Op: "="},
}

// ReconcileMessages is the full-mode reconciliation for messages.
func (c *Converter) ReconcileMessages(ctx context.Context) (*ReconciliationResult, error) {
	clientTypes, err := c.getConversationClientTypes(ctx)
	if err != nil {
		return nil, err
	}

	flowIDs, err := c.getConversationFlowIDs(ctx)
	if err != nil {
		return nil, err
	}

	params := map[string]any{"flow_ids": flowIDs, "types": clientTypes}

	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), messagesReconSourceSQL, messagesReconTargetSQL, params, messagesReconChecks)
}

// messagesSyncReconSourceSQL and messagesSyncReconTargetSQL implement the
// sync-mode reconciliation described in the "Sync" section of
// messages.recon.md. Unlike full mode, im_message.messages can gain rows
// from live user activity during a sync run, so it can't be counted
// directly -- instead this goes through chat_migration (entity_type =
// 'message'), which the step only writes for text/file messages (see
// .md/enhancements/migration_steps/messages.md, "Добавить запись в таблицу
// chat_migration"). Both sides therefore restrict to those two types instead
// of counting every type like full mode does, and the old_db side adds the
// [fromDate, toDate) window on conv.closed_at the step itself uses to select
// records for this run.
const (
	messagesSyncReconSourceSQL = `
SELECT
    COUNT(*) AS "source.total_mapped_messages_count",
    COUNT(*) FILTER (WHERE m."type" = 'text') AS "source.text_messages_count",
    COUNT(*) FILTER (WHERE m."type" = 'file') AS "source.file_messages_count",
    COUNT(*) FILTER (WHERE m.channel_id IS NULL) AS "source.bot_messages_count",
    COUNT(*) FILTER (WHERE m.channel_id IS NOT NULL AND ch."type" = 'webitel') AS "source.agent_messages_count",
    COUNT(*) FILTER (WHERE m.channel_id IS NOT NULL AND ch."type" IN ('portal', 'facebook')) AS "source.user_messages_count"
FROM chat.message m
JOIN chat.conversation conv ON conv.id = m.conversation_id
LEFT JOIN chat.channel ch ON ch.id = m.channel_id
WHERE conv.closed_at IS NOT NULL
  AND conv.props ->> 'flow' IS NOT NULL
  AND (conv.props ->> 'flow')::int8 = ANY(:flow_ids)
  AND conv.closed_at >= :created_from
  AND conv.closed_at < :created_to
  AND EXISTS (
      SELECT 1
      FROM chat.channel initiator
      JOIN chat.client cl ON cl.id = initiator.user_id
      WHERE initiator.conversation_id = conv.id
        AND NOT initiator.internal
        AND COALESCE(cl.type, 'webchat') = ANY(:types::text[])
  )
  AND m."type" IN ('text', 'file')
  AND m.text IS DISTINCT FROM 'start'`

	messagesSyncReconTargetSQL = `
SELECT
    COUNT(*) AS "target.migration_count",
    COUNT(*) FILTER (WHERE m.id IS NOT NULL) AS "target.total_mapped_messages_count",
    COUNT(*) FILTER (WHERE m."type" = 1) AS "target.text_messages_count",
    COUNT(*) FILTER (WHERE m."type" = 2) AS "target.file_messages_count",
    COUNT(*) FILTER (WHERE c.is_bot IS TRUE) AS "target.bot_messages_count",
    COUNT(*) FILTER (WHERE c.is_bot IS NOT TRUE AND c."type" = 'webitel') AS "target.agent_messages_count",
    COUNT(*) FILTER (WHERE c.is_bot IS NOT TRUE AND c."type" IS DISTINCT FROM 'webitel') AS "target.user_messages_count"
FROM public.chat_migration cm
LEFT JOIN im_message.messages m ON m.id = cm.new_id
LEFT JOIN im_contact.contact c ON c.id = m.sender_id
WHERE cm.entity_type = 'message'
  AND cm.session_id = :session_id`
)

var messagesSyncReconChecks = []ReconciliationCheck{
	{Left: "source.total_mapped_messages_count", Right: "target.migration_count", Op: "="},
	{Left: "target.migration_count", Right: "target.total_mapped_messages_count", Op: "="},
	{Left: "source.text_messages_count", Right: "target.text_messages_count", Op: "="},
	{Left: "source.file_messages_count", Right: "target.file_messages_count", Op: "="},
	{Left: "source.bot_messages_count", Right: "target.bot_messages_count", Op: "="},
	{Left: "source.agent_messages_count", Right: "target.agent_messages_count", Op: "="},
	{Left: "source.user_messages_count", Right: "target.user_messages_count", Op: "="},
}

// ReconcileMessagesSyncMode is the sync-mode reconciliation for
// sync_mode_messages. Uses messagesSyncReconSourceSQL/messagesSyncReconTargetSQL/
// messagesSyncReconChecks, not the full-mode query/checks -- see the package
// comment on messagesSyncReconSourceSQL for why full mode's whole-table
// comparison doesn't hold once new_db keeps gaining rows from live activity.
func (c *Converter) ReconcileMessagesSyncMode(ctx context.Context) (*ReconciliationResult, error) {
	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return nil, err
	}

	clientTypes, err := c.getConversationClientTypes(ctx)
	if err != nil {
		return nil, err
	}

	flowIDs, err := c.getConversationFlowIDs(ctx)
	if err != nil {
		return nil, err
	}

	params := map[string]any{"created_from": fromDate, "created_to": toDate, "session_id": c.sessionID, "flow_ids": flowIDs, "types": clientTypes}

	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), messagesSyncReconSourceSQL, messagesSyncReconTargetSQL, params, messagesSyncReconChecks)
}
