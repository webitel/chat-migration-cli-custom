package service

import "context"

// botsReconTargetSQL implements the reconciliation described in
// .md/enhancements/migration_steps/bots_to_contacts.recon.md. Full mode only
// -- the sync-mode step is a no-op. bots_to_contacts doesn't read old_db (bots
// are configured in new_db manually), so both sides of the comparison live in
// new_db and there is no source query. Not scoped to :session_id: like
// facebook_and_whatsapp (see facebook_recon.go), this step writes
// public.bot_mapping into chat_migration wholesale, once, with entity_type =
// 'bot_contact' unique to this step -- the check holds across the whole
// database.
const botsReconTargetSQL = `
SELECT
    COUNT(*) AS "target.bot_mapping_count",
    COUNT(*) FILTER (WHERE c.id IS NOT NULL) AS "target.contact_count",
    COUNT(*) FILTER (WHERE cs.contact_id IS NOT NULL) AS "target.setting_count",
    COUNT(*) FILTER (WHERE cm.id IS NOT NULL) AS "target.migration_count"
FROM public.bot_mapping bm
LEFT JOIN im_contact.contact c ON c.id = bm.new_bot_id
LEFT JOIN im_contact.contact_setting cs ON cs.contact_id = bm.new_bot_id
LEFT JOIN public.chat_migration cm
    ON cm.entity_type = 'bot_contact'
   AND cm.new_id = bm.new_bot_id
   AND cm.old_id = bm.old_bot_id::text`

var botsReconChecks = []ReconciliationCheck{
	{Left: "target.bot_mapping_count", Right: "target.contact_count", Op: "="},
	{Left: "target.bot_mapping_count", Right: "target.setting_count", Op: "="},
	{Left: "target.bot_mapping_count", Right: "target.migration_count", Op: "="},
}

// ReconcileBotsToContacts is the full-mode reconciliation for
// bots_to_contacts. There is no sync-mode counterpart -- the sync step is a
// no-op.
func (c *Converter) ReconcileBotsToContacts(ctx context.Context) (*ReconciliationResult, error) {
	return runTargetOnlyReconciliation(ctx, c.newDB.Pool(), botsReconTargetSQL, nil, botsReconChecks)
}
