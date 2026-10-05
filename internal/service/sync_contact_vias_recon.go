package service

import "context"

// syncContactViasReconTargetSQL implements the full-mode reconciliation
// described in the "Full" section of
// .md/enhancements/migration_steps/sync_contact_vias.recon.md. Both values
// come from new_db (im_contact.contact vs im_contact.via) -- no old_db query
// is needed. In full mode target db starts empty and only this tool writes
// to it, so counting the whole table is safe. Sync mode needs its own
// session-scoped query -- see syncContactViasSyncReconTargetSQL below, and
// the "Sync" section of the doc for why.
const syncContactViasReconTargetSQL = `
SELECT
    (SELECT COUNT(*)
       FROM im_contact.contact c
      WHERE c.type = 'facebook' AND c.is_bot = false) AS "target.facebook_contacts_count",
    (SELECT COUNT(DISTINCT c.id)
       FROM im_contact.contact c
       JOIN im_contact.via v ON v.contact_id = c.id
      WHERE c.type = 'facebook' AND c.is_bot = false) AS "target.facebook_contacts_with_via_count"`

var syncContactViasReconChecks = []ReconciliationCheck{
	{Left: "target.facebook_contacts_count", Right: "target.facebook_contacts_with_via_count", Op: "="},
}

// ReconcileSyncContactVias is the full-mode reconciliation for
// sync_contact_vias.
func (c *Converter) ReconcileSyncContactVias(ctx context.Context) (*ReconciliationResult, error) {
	return runTargetOnlyReconciliation(ctx, c.newDB.Pool(), syncContactViasReconTargetSQL, nil, syncContactViasReconChecks)
}

// syncContactViasSyncReconTargetSQL implements the sync-mode reconciliation
// described in the "Sync" section of sync_contact_vias.recon.md. Unlike full
// mode, im_contact.contact can gain facebook contacts that have nothing to
// do with this migration session (live user activity during the sync
// window), so counting the whole table would produce false mismatches --
// instead this restricts to contacts this session's
// sync_mode_clients_to_contacts created (chat_migration, entity_type =
// 'client_contact', session_id = :session_id). SyncContactVias itself
// (internal/store/newdb/contact.go) isn't session-scoped -- it idempotently
// fills in im_contact.via for the whole chat_migration table -- but that's
// fine here: the check only cares whether a via row exists by the time it
// runs, not which run created it.
const syncContactViasSyncReconTargetSQL = `
SELECT
    (SELECT COUNT(DISTINCT c.id)
       FROM public.chat_migration cm
       JOIN im_contact.contact c ON c.id = cm.new_id
      WHERE cm.entity_type = 'client_contact'
        AND cm.session_id = :session_id
        AND c.type = 'facebook'
        AND c.is_bot = false) AS "target.facebook_contacts_count",
    (SELECT COUNT(DISTINCT c.id)
       FROM public.chat_migration cm
       JOIN im_contact.contact c ON c.id = cm.new_id
       JOIN im_contact.via v ON v.contact_id = c.id
      WHERE cm.entity_type = 'client_contact'
        AND cm.session_id = :session_id
        AND c.type = 'facebook'
        AND c.is_bot = false) AS "target.facebook_contacts_with_via_count"`

var syncContactViasSyncReconChecks = []ReconciliationCheck{
	{Left: "target.facebook_contacts_count", Right: "target.facebook_contacts_with_via_count", Op: "="},
}

// ReconcileSyncContactViasSyncMode is the sync-mode reconciliation for
// sync_mode_sync_contact_vias. Uses syncContactViasSyncReconTargetSQL/
// syncContactViasSyncReconChecks, not the full-mode query/checks -- see the
// package comment on syncContactViasSyncReconTargetSQL for why full mode's
// whole-table comparison doesn't hold once new_db can gain unrelated
// facebook contacts during a sync run.
func (c *Converter) ReconcileSyncContactViasSyncMode(ctx context.Context) (*ReconciliationResult, error) {
	params := map[string]any{"session_id": c.sessionID}

	return runTargetOnlyReconciliation(ctx, c.newDB.Pool(), syncContactViasSyncReconTargetSQL, params, syncContactViasSyncReconChecks)
}
