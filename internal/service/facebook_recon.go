package service

import "context"

// facebookReconSourceSQL and facebookReconTargetSQL implement the
// reconciliation described in
// .md/enhancements/migration_steps/facebook.recon.md. Like bots_to_contacts,
// this step writes public.bot_mapping into chat_migration wholesale, once,
// with no pagination and no session scoping -- the check holds across the
// whole database. Full mode only; the sync-mode step is a no-op.
const (
	facebookReconSourceSQL = `
SELECT COUNT(*) AS "source.matched_providers_count"
FROM chat.bot
WHERE provider = 'messenger' AND flow_id = ANY(:flow_ids)`

	facebookReconTargetSQL = `
SELECT
    (SELECT COUNT(*) FROM public.chat_migration WHERE entity_type = 'provider_to_gateway') AS "target.provider_to_gateway_count",
    (SELECT COUNT(*) FROM public.bot_mapping WHERE type = 'facebook' AND gate_id IS NOT NULL) AS "target.bot_mapping_gate_count"`
)

var facebookReconChecks = []ReconciliationCheck{
	{Left: "source.matched_providers_count", Right: "target.provider_to_gateway_count", Op: "="},
	{Left: "target.bot_mapping_gate_count", Right: "target.provider_to_gateway_count", Op: "="},
}

// ReconcileFacebookProviders is the full-mode reconciliation for
// facebook_and_whatsapp. There is no sync-mode counterpart -- the sync step
// is a no-op.
func (c *Converter) ReconcileFacebookProviders(ctx context.Context) (*ReconciliationResult, error) {
	gateMappings, err := c.newDB.BotMappingStore().GetGateMappings(ctx)
	if err != nil {
		return nil, err
	}
	flowIDs := make([]int, 0, len(gateMappings))
	for _, m := range gateMappings {
		flowIDs = append(flowIDs, m.OldBotID)
	}
	params := map[string]any{"flow_ids": flowIDs}
	return runReconciliation(ctx, c.oldDB.Pool(), c.newDB.Pool(), facebookReconSourceSQL, facebookReconTargetSQL, params, facebookReconChecks)
}
