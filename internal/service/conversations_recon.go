package service

import "context"

// conversationsReconSourceSQL and conversationsReconTargetSQL implement the
// full-mode reconciliation described in the "Full" section of
// .md/enhancements/migration_steps/conversations.recon.md. At this step
// threads aren't yet linked to members/dialogs, so only thread creation
// itself can be verified. :created_from/:created_to/:flow_ids are the same
// window and flow_id list the step itself used to select records for this
// run (see .md/enhancements/common/cutoff_date.md); full and sync mode
// resolve them the same way, but sync mode needs its own query/checks -- see
// conversationsSyncReconSourceSQL below.
const (
	conversationsReconSourceSQL = `
SELECT COUNT(DISTINCT initiator.user_id) AS "source.source_count"
FROM chat.conversation conv
INNER JOIN chat.channel initiator
    ON initiator.conversation_id = conv.id
   AND NOT initiator.internal
INNER JOIN chat.client cl ON cl.id = initiator.user_id
WHERE conv.closed_at IS NOT NULL
  AND conv.props ->> 'flow' IS NOT NULL
  AND (conv.props ->> 'flow')::int = ANY (:flow_ids::int[])
  AND COALESCE(cl.type, 'webchat') = ANY (:types::text[])
  AND conv.closed_at >= :created_from
  AND conv.closed_at < :created_to`

	// migration_count counts distinct threads referenced by this session's
	// conversation_thread rows -- one row per old conv_id, but one thread per
	// initiator group, including a sync-mode group that appends to
	// a thread created in an earlier session (its new conversation_thread
	// rows still carry the current session_id). thread_count restricts that
	// to new_id values that actually exist in im_thread.thread, to catch an
	// orphaned mapping.
	conversationsReconTargetSQL = `
SELECT
    COUNT(DISTINCT cm.new_id) AS "target.migration_count",
    COUNT(DISTINCT cm.new_id) FILTER (WHERE th.id IS NOT NULL) AS "target.thread_count"
FROM public.chat_migration cm
LEFT JOIN im_thread.thread th ON th.id = cm.new_id
WHERE cm.entity_type = 'conversation_thread'
  AND cm.session_id = :session_id`
)

var conversationsReconChecks = []ReconciliationCheck{
	{Left: "source.source_count", Right: "target.migration_count", Op: "="},
	{Left: "source.source_count", Right: "target.thread_count", Op: "="},
}

// conversationsSyncReconSourceSQL and conversationsSyncReconTargetSQL
// implement the sync-mode reconciliation described in the "Sync" section of
// conversations.recon.md. Unlike full mode, grouping by initiator
// doesn't work here: a group whose thread already exists from an earlier
// session doesn't guarantee every new chat.conversation row in that group
// this run got its own conversation_thread mapping. So both sides count
// individual chat.conversation rows (conv_id) instead of distinct groups --
// same selection criteria as full mode's source query, just without the
// DISTINCT/grouping.
const (
	conversationsSyncReconSourceSQL = `
SELECT COUNT(*) AS "source.source_count"
FROM chat.conversation conv
WHERE conv.closed_at IS NOT NULL
  AND conv.props ->> 'flow' IS NOT NULL
  AND (conv.props ->> 'flow')::int = ANY (:flow_ids::int[])
  AND conv.closed_at >= :created_from
  AND conv.closed_at < :created_to
  AND EXISTS (
    SELECT 1
    FROM chat.channel initiator
    JOIN chat.client cl ON cl.id = initiator.user_id
    WHERE initiator.conversation_id = conv.id
      AND NOT initiator.internal
      AND COALESCE(cl.type, 'webchat') = ANY (:types::text[])
  )`

	conversationsSyncReconTargetSQL = `
SELECT
    COUNT(DISTINCT cm.old_id) AS "target.migration_count",
    COUNT(DISTINCT cm.old_id) FILTER (WHERE th.id IS NOT NULL) AS "target.thread_count"
FROM public.chat_migration cm
LEFT JOIN im_thread.thread th ON th.id = cm.new_id
WHERE cm.entity_type = 'conversation_thread'
  AND cm.session_id = :session_id`
)

var conversationsSyncReconChecks = []ReconciliationCheck{
	{Left: "source.source_count", Right: "target.migration_count", Op: "="},
	{Left: "source.source_count", Right: "target.thread_count", Op: "="},
}

// ReconcileConversations is the full-mode reconciliation for conversations.
func (c *Converter) ReconcileConversations(ctx context.Context) (*ReconciliationResult, error) {
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

	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), conversationsReconSourceSQL, conversationsReconTargetSQL, params, conversationsReconChecks)
}

// ReconcileConversationsSyncMode is the sync-mode reconciliation for
// sync_mode_conversations. Uses conversationsSyncReconSourceSQL/
// conversationsSyncReconTargetSQL/conversationsSyncReconChecks, not the
// full-mode query/checks -- see the "Sync" section of
// conversations.recon.md for why full mode's group-based comparison doesn't
// hold once threads can be appended to across sessions.
func (c *Converter) ReconcileConversationsSyncMode(ctx context.Context) (*ReconciliationResult, error) {
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

	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), conversationsSyncReconSourceSQL, conversationsSyncReconTargetSQL, params, conversationsSyncReconChecks)
}
