package service

import "context"

// clientsReconSourceSQL and clientsReconTargetSQL implement the full-mode
// reconciliation described in the "Full" section of
// .md/enhancements/migration_steps/clients_to_contacts.recon.md. They
// intentionally don't reuse the migration step's own queries/filters -- see
// that doc for why the type filter here is a hardcoded snapshot rather than
// sourced from public.bot_mapping.
const (
	clientsReconSourceSQL = `
SELECT COUNT(*) AS "source.source_count"
FROM chat.client c
WHERE c.type = 'facebook'
  AND c.created_at >= :created_from
  AND c.created_at < :created_to
  AND EXISTS (
    SELECT 1
    FROM chat.channel ch
    WHERE ch.user_id = c.id
      AND NOT ch.internal
      AND ch.connection IS NOT NULL
  )`

	clientsReconTargetSQL = `
SELECT
    COUNT(*) AS "target.target_count",
    (
        SELECT COUNT(*)
        FROM im_contact.contact_setting cs
        JOIN im_contact.contact c2 ON c2.id = cs.contact_id
        WHERE c2.type = 'facebook'
          AND NOT c2.is_bot
    ) AS "target.setting_count",
    (
        SELECT COUNT(*)
        FROM public.chat_migration cm
        WHERE cm.entity_type = 'client_contact'
          AND cm.session_id = :session_id
    ) AS "target.migration_count"
FROM im_contact.contact c
WHERE c.type = 'facebook'`

	// clientsReconSyncTargetSQL implements the "Sync" section of
	// clients_to_contacts.recon.md: target db already carries data unrelated
	// to migration (organic post-cutover activity), so target_count/setting_count
	// can no longer be scoped by type = 'facebook' and compared against
	// source_count -- instead they check, for every contact regardless of
	// type, whether it has a matching contact_setting row (LEFT JOIN +
	// FILTER, not a separate COUNT(*) per table). migration_count/
	// mapped_contact_count go through the migration mapping instead, scoped
	// to the current session.
	clientsReconSyncTargetSQL = `
SELECT
    COUNT(*) AS "target.target_count",
    COUNT(*) FILTER (WHERE cs.contact_id IS NOT NULL) AS "target.setting_count",
    (
        SELECT COUNT(*)
        FROM public.chat_migration cm
        WHERE cm.entity_type = 'client_contact'
          AND cm.session_id = :session_id
    ) AS "target.migration_count",
    (
        SELECT COUNT(*)
        FROM public.chat_migration cm
        JOIN im_contact.contact c3 ON c3.id = cm.new_id
        WHERE cm.entity_type = 'client_contact'
          AND cm.session_id = :session_id
    ) AS "target.mapped_contact_count"
FROM im_contact.contact c
LEFT JOIN im_contact.contact_setting cs ON cs.contact_id = c.id`
)

var clientsReconChecks = []ReconciliationCheck{
	{Left: "source.source_count", Right: "target.migration_count", Op: "="},
	{Left: "source.source_count", Right: "target.target_count", Op: "="},
	{Left: "source.source_count", Right: "target.setting_count", Op: "="},
}

// clientsReconSyncChecks implements the sync-mode reconciliation described in
// the "Sync" section of clients_to_contacts.recon.md: unlike full mode, target
// db already carries data unrelated to migration (organic post-cutover
// activity), so source_count can't be compared against the full im_contact.contact/
// contact_setting tables. Instead the checks go step by step through the
// migration mapping (migration_count -> mapped_contact_count), plus a
// session-independent, type-independent invariant that every contact has a
// matching contact_setting row (target_count = setting_count).
var clientsReconSyncChecks = []ReconciliationCheck{
	{Left: "source.source_count", Right: "target.migration_count", Op: "="},
	{Left: "target.migration_count", Right: "target.mapped_contact_count", Op: "="},
	{Left: "target.target_count", Right: "target.setting_count", Op: "="},
}

// ReconcileClientsToContacts is the full-mode reconciliation for
// clients_to_contacts: :created_from/:created_to are the same fixed
// migration window ([fromDate, toDate)) the step itself used to select
// records for this run (see .md/enhancements/common/cutoff_date.md).
func (c *Converter) ReconcileClientsToContacts(ctx context.Context) (*ReconciliationResult, error) {
	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return nil, err
	}

	params := map[string]any{"created_from": fromDate, "created_to": toDate, "session_id": c.sessionID}

	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), clientsReconSourceSQL, clientsReconTargetSQL, params, clientsReconChecks)
}

// ReconcileClientsToContactsSyncMode is the sync-mode reconciliation for
// sync_mode_clients_to_contacts: :created_from/:created_to are the same
// fixed migration window the step itself used to select records for this
// run. Uses clientsReconSyncTargetSQL/clientsReconSyncChecks, not the full-mode
// query/checks -- see the "Sync" section of clients_to_contacts.recon.md for
// why sync mode can't compare source_count against the full im_contact.contact/
// contact_setting tables.
func (c *Converter) ReconcileClientsToContactsSyncMode(ctx context.Context) (*ReconciliationResult, error) {
	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return nil, err
	}

	params := map[string]any{"created_from": fromDate, "created_to": toDate, "session_id": c.sessionID}

	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), clientsReconSourceSQL, clientsReconSyncTargetSQL, params, clientsReconSyncChecks)
}
