package service

import "context"

// portalReconSourceSQL and portalReconTargetSQL implement the full-mode
// reconciliation described in the "Full" section of
// .md/enhancements/migration_steps/portal_client_to_contact.recon.md.
const (
	portalReconSourceSQL = `
SELECT COUNT(*) AS "source.source_count"
FROM chat.client c
INNER JOIN portal.user_account acc ON acc.id = c.external_id::uuid
WHERE c.type = 'portal'
  AND c.created_at >= :created_from
  AND c.created_at < :created_to`

	portalReconTargetSQL = `
SELECT
    COUNT(*) AS "target.target_count",
    (
        SELECT COUNT(*)
        FROM im_contact.contact_setting cs
        JOIN im_contact.contact c2 ON c2.id = cs.contact_id
        WHERE c2.type = 'salmon'
          AND NOT c2.is_bot
    ) AS "target.setting_count",
    (
        SELECT COUNT(*)
        FROM public.chat_migration cm
        JOIN im_contact.contact c3 ON c3.id = cm.new_id
        WHERE cm.entity_type = 'client_contact'
          AND cm.session_id = :session_id
          AND c3.type = 'salmon'
    ) AS "target.migration_count"
FROM im_contact.contact c
WHERE c.type = 'salmon'`

	// portalReconSyncTargetSQL implements the "Sync" section of
	// portal_client_to_contact.recon.md: target_count/setting_count can no
	// longer be scoped by the portal contact type and compared against source_count,
	// since target db already carries organic post-cutover activity --
	// instead they check, for every contact regardless of type, whether it
	// has a matching contact_setting row (LEFT JOIN + FILTER, not a separate
	// COUNT(*) per table). migration_count is unchanged from Full: it already
	// requires a real, existing, portal-typed contact via its JOIN, since
	// chat_migration alone can't tell this step's rows apart from
	// clients_to_contacts' rows in the same session.
	portalReconSyncTargetSQL = `
SELECT
    COUNT(*) AS "target.target_count",
    COUNT(*) FILTER (WHERE cs.contact_id IS NOT NULL) AS "target.setting_count",
    (
        SELECT COUNT(*)
        FROM public.chat_migration cm
        JOIN im_contact.contact c3 ON c3.id = cm.new_id
        WHERE cm.entity_type = 'client_contact'
          AND cm.session_id = :session_id
          AND c3.type = 'salmon'
    ) AS "target.migration_count"
FROM im_contact.contact c
LEFT JOIN im_contact.contact_setting cs ON cs.contact_id = c.id`
)

// portalReconChecks does not compare source_count against target_count/
// setting_count: since the step now inserts via InsertContactsIgnoreConflicts
// (old_db can carry two chat.client rows for the same portal user, e.g. one
// per app, sharing the same (domain_id, subject_id)), several old rows can
// resolve to the same contact, so target_count can legitimately be lower
// than source_count. migration_count stays 1:1 with source_count regardless
// (one chat_migration row per old row, even if new_id repeats); target_count
// = setting_count is the same self-consistency invariant sync mode uses --
// every real contact has a settings row.
var portalReconChecks = []ReconciliationCheck{
	{Left: "source.source_count", Right: "target.migration_count", Op: "="},
	{Left: "target.target_count", Right: "target.setting_count", Op: "="},
}

// portalReconSyncChecks implements the sync-mode reconciliation described in
// the "Sync" section of portal_client_to_contact.recon.md: unlike full mode,
// target db already carries data unrelated to migration (organic post-cutover
// activity), so source_count can't be compared against the full
// im_contact.contact/contact_setting tables. migration_count still goes
// through the migration mapping (session-scoped), same query as Full; the
// contact/contact_setting invariant is type- and session-independent
// (target_count = setting_count).
var portalReconSyncChecks = []ReconciliationCheck{
	{Left: "source.source_count", Right: "target.migration_count", Op: "="},
	{Left: "target.target_count", Right: "target.setting_count", Op: "="},
}

// ReconcilePortalClientsToContacts is the full-mode reconciliation for
// portal_client_to_contact: :created_from/:created_to are the same fixed
// migration window ([fromDate, toDate)) the step itself used to select
// records for this run (see .md/enhancements/common/cutoff_date.md).
func (c *Converter) ReconcilePortalClientsToContacts(ctx context.Context) (*ReconciliationResult, error) {
	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return nil, err
	}

	params := map[string]any{"created_from": fromDate, "created_to": toDate, "session_id": c.sessionID}

	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), portalReconSourceSQL, portalReconTargetSQL, params, portalReconChecks)
}

// ReconcilePortalClientsToContactsSyncMode is the sync-mode reconciliation
// for sync_mode_portal_client_to_contact: :created_from/:created_to are the
// same fixed migration window the step itself used to select records for
// this run. Uses portalReconSyncTargetSQL/portalReconSyncChecks, not the
// full-mode query/checks -- see the "Sync" section of
// portal_client_to_contact.recon.md for why sync mode can't compare
// source_count against the full im_contact.contact/contact_setting tables.
func (c *Converter) ReconcilePortalClientsToContactsSyncMode(ctx context.Context) (*ReconciliationResult, error) {
	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return nil, err
	}

	params := map[string]any{"created_from": fromDate, "created_to": toDate, "session_id": c.sessionID}

	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), portalReconSourceSQL, portalReconSyncTargetSQL, params, portalReconSyncChecks)
}
