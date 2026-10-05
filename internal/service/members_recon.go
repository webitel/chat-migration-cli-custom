package service

import "context"

// membersReconSourceSQL and membersReconTargetSQL implement the full-mode
// reconciliation described in .md/enhancements/migration_steps/members.full.recon.md.
// These checks are not scoped to the current session or migration window --
// they hold across the whole database, regardless of which session created
// which row. A thread's owner dialogs are created once, whenever the thread
// itself is first created (in whichever session that happens to be); a later
// sync run that only appends new conv_ids to an already-existing thread never
// recreates them, so per-session counting would not work here. Sync mode
// needs its own session-scoped queries/checks -- see membersSyncReconSourceSQL
// below.
const (
	membersReconSourceSQL = `
SELECT
    (
        SELECT COUNT(DISTINCT (initiator.user_id, conv.props ->> 'flow')) * 2
        FROM chat.conversation conv
        INNER JOIN chat.channel initiator
            ON initiator.conversation_id = conv.id
           AND NOT initiator.internal
        WHERE conv.closed_at IS NOT NULL
          AND conv.props ->> 'flow' IS NOT NULL
          AND (conv.props ->> 'flow')::int = ANY (:flow_ids::int[])
    ) AS "source.owner_dialogs_expected",
    (
        SELECT COUNT(DISTINCT (initiator.user_id, conv.props ->> 'flow', ch.user_id))
        FROM chat.conversation conv
        INNER JOIN chat.channel initiator
            ON initiator.conversation_id = conv.id
           AND NOT initiator.internal
        INNER JOIN chat.channel ch
            ON ch.conversation_id = conv.id
           AND ch.internal
        WHERE conv.closed_at IS NOT NULL
          AND conv.props ->> 'flow' IS NOT NULL
          AND (conv.props ->> 'flow')::int = ANY (:flow_ids::int[])
    ) AS "source.internal_dialogs_count"`

	// thread_dialogs_count/thread_permissions_count/direct_settings_count
	// check an intra-new_db invariant that isn't specific to this
	// migration -- every thread_dialog InsertThreadDialogs creates gets
	// exactly one thread_permission row (thread_dialog.go), and by the same
	// design every thread_dialog should get exactly one direct_settings row
	// (buildOwnerThreadDialogFromConversation/buildInternalUsersThreadDialogs
	// both build one DirectSettings per dialog, and DirectSettingsStore.
	// InsertDirectSettings now inserts all of them, not just settings[0]).
	membersReconTargetSQL = `
SELECT
    (SELECT COUNT(*) FROM im_thread.thread_dialog WHERE thread_role = 4) AS "target.owner_dialogs_count",
    (SELECT COUNT(*) FROM im_thread.thread_dialog WHERE thread_role = 1) AS "target.internal_dialogs_count",
    (SELECT COUNT(*) FROM im_thread.thread_dialog) AS "target.thread_dialogs_count",
    (SELECT COUNT(*) FROM im_thread.thread_permission) AS "target.thread_permissions_count",
    (SELECT COUNT(*) FROM im_thread.direct_settings) AS "target.direct_settings_count"`
)

var membersReconChecks = []ReconciliationCheck{
	{Left: "source.owner_dialogs_expected", Right: "target.owner_dialogs_count", Op: "="},
	{Left: "source.internal_dialogs_count", Right: "target.internal_dialogs_count", Op: "="},
	{Left: "target.thread_dialogs_count", Right: "target.thread_permissions_count", Op: "="},
	{Left: "target.thread_dialogs_count", Right: "target.direct_settings_count", Op: "="},
}

// ReconcileMembers is the full-mode reconciliation for members.
func (c *Converter) ReconcileMembers(ctx context.Context) (*ReconciliationResult, error) {
	flowIDs, err := c.getConversationFlowIDs(ctx)
	if err != nil {
		return nil, err
	}

	params := map[string]any{"flow_ids": flowIDs}

	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), membersReconSourceSQL, membersReconTargetSQL, params, membersReconChecks)
}

// membersSyncReconSourceSQL and membersSyncReconTargetSQL implement the
// sync-mode reconciliation described in
// .md/enhancements/migration_steps/members.sync.recon.md. Unlike full mode,
// owner dialogs aren't recreated for a thread inherited from an earlier
// session, so they're checked over the set of threads this session's
// conversation_thread rows point at (chat_migration, entity_type =
// 'conversation_thread'), not over the whole im_thread.thread_dialog table.
// Internal dialogs and thread_permission/direct_settings, on the other hand,
// must be counted only for rows this session itself created, using the
// chat_migration rows the step writes per created thread_dialog -- otherwise
// dialogs left over from a previous session on a reused thread would cause a
// false mismatch.
const (
	membersSyncReconSourceSQL = `
SELECT COUNT(DISTINCT (initiator.user_id, conv.props ->> 'flow', ch.user_id)) AS "source.internal_dialogs_count"
FROM chat.conversation conv
INNER JOIN chat.channel initiator
    ON initiator.conversation_id = conv.id
   AND NOT initiator.internal
INNER JOIN chat.channel ch
    ON ch.conversation_id = conv.id
   AND ch.internal
WHERE conv.closed_at IS NOT NULL
  AND conv.props ->> 'flow' IS NOT NULL
  AND (conv.props ->> 'flow')::int = ANY (:flow_ids::int[])
  AND conv.closed_at >= :created_from
  AND conv.closed_at < :created_to`

	membersSyncReconTargetSQL = `
SELECT
    (
        SELECT COUNT(DISTINCT new_id)
        FROM public.chat_migration
        WHERE entity_type = 'conversation_thread'
          AND session_id = :session_id
    ) * 2 AS "target.owner_dialogs_expected",
    (
        SELECT COUNT(*)
        FROM im_thread.thread_dialog td
        WHERE td.thread_role = 4
          AND td.thread_id IN (
              SELECT DISTINCT new_id
              FROM public.chat_migration
              WHERE entity_type = 'conversation_thread'
                AND session_id = :session_id
          )
    ) AS "target.owner_dialogs_count",
    (
        SELECT COUNT(*)
        FROM public.chat_migration cm
        WHERE cm.entity_type = 'internal_channel_thread_dialog'
          AND cm.session_id = :session_id
    ) AS "target.migration_count",
    (
        SELECT COUNT(*)
        FROM public.chat_migration cm
        INNER JOIN im_thread.thread_dialog td ON td.id = cm.new_id
        WHERE cm.entity_type = 'internal_channel_thread_dialog'
          AND cm.session_id = :session_id
          AND td.thread_role = 1
    ) AS "target.internal_dialogs_count",
    (
        SELECT COUNT(*)
        FROM public.chat_migration cm
        WHERE cm.entity_type IN ('initiator_channel_thread_dialog', 'bot_channel_thread_dialog', 'internal_channel_thread_dialog')
          AND cm.session_id = :session_id
    ) AS "target.thread_dialogs_count",
    (
        SELECT COUNT(*)
        FROM public.chat_migration cm
        INNER JOIN im_thread.thread_permission tp ON tp.thread_dialog_id = cm.new_id
        WHERE cm.entity_type IN ('initiator_channel_thread_dialog', 'bot_channel_thread_dialog', 'internal_channel_thread_dialog')
          AND cm.session_id = :session_id
    ) AS "target.thread_permissions_count",
    (
        SELECT COUNT(*)
        FROM public.chat_migration cm
        INNER JOIN im_thread.direct_settings ds ON ds.thread_dialog_id = cm.new_id
        WHERE cm.entity_type IN ('initiator_channel_thread_dialog', 'bot_channel_thread_dialog', 'internal_channel_thread_dialog')
          AND cm.session_id = :session_id
    ) AS "target.direct_settings_count"`
)

var membersSyncReconChecks = []ReconciliationCheck{
	{Left: "target.owner_dialogs_expected", Right: "target.owner_dialogs_count", Op: "="},
	{Left: "source.internal_dialogs_count", Right: "target.migration_count", Op: "="},
	{Left: "target.migration_count", Right: "target.internal_dialogs_count", Op: "="},
	{Left: "target.thread_dialogs_count", Right: "target.thread_permissions_count", Op: "="},
	{Left: "target.thread_dialogs_count", Right: "target.direct_settings_count", Op: "="},
}

// ReconcileMembersSyncMode is the sync-mode reconciliation for
// sync_mode_members. Uses membersSyncReconSourceSQL/membersSyncReconTargetSQL/
// membersSyncReconChecks, not the full-mode query/checks -- see the package
// comment on membersSyncReconSourceSQL for why full mode's whole-database
// comparison doesn't hold once threads and their dialogs can be reused or
// appended to across sessions.
func (c *Converter) ReconcileMembersSyncMode(ctx context.Context) (*ReconciliationResult, error) {
	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return nil, err
	}

	flowIDs, err := c.getConversationFlowIDs(ctx)
	if err != nil {
		return nil, err
	}

	params := map[string]any{"created_from": fromDate, "created_to": toDate, "session_id": c.sessionID, "flow_ids": flowIDs}

	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), membersSyncReconSourceSQL, membersSyncReconTargetSQL, params, membersSyncReconChecks)
}
