package service

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
)

// MigrateFacebookProviders does not create gates, meta apps or bots: those
// are configured manually in new_db before migration (see
// .md/enhancements/migration_steps/facebook.md). It only writes
// chat_migration rows mapping each old Facebook/WhatsApp provider (chat.bot,
// resolved via public.bot_mapping.old_bot_id/flow_id) to its pre-existing
// im_provider.gates row, via bot_mapping.gate_id.
//
// old_id is chat.bot.id, not flow_id: SyncContactVias joins
// gateway_to_contact.old_id (chat.channel.connection, which clients_to_contacts
// records as chat.bot.id) against provider_to_gateway.old_id -- using flow_id
// here would silently break that join. Full mode only; see
// MigrateFacebookProvidersSyncMode.
func (c *Converter) MigrateFacebookProviders(ctx context.Context) error {
	c.log.Debug("starting facebook/whatsapp providers migration")

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, StepFacebookAndWhatsApp, 0, cause.Error())
		return cause
	}

	gateMappings, err := c.newDB.BotMappingStore().GetGateMappings(ctx)
	if err != nil {
		return fail(err)
	}
	if len(gateMappings) == 0 {
		return nil
	}

	flowIDs := make([]int, 0, len(gateMappings))
	gateByFlowID := make(map[int]uuid.UUID, len(gateMappings))
	for _, m := range gateMappings {
		flowIDs = append(flowIDs, m.OldBotID)
		gateByFlowID[m.OldBotID] = m.GateID
	}

	providers, err := c.oldDB.BotStore().GetProviderIDsByFlowIDs(ctx, flowIDs)
	if err != nil {
		return fail(err)
	}

	migrationRows := make([]*modelnew.MigrationRow, 0, len(providers))
	matchedFlowIDs := make(map[int]struct{}, len(providers))
	for _, p := range providers {
		matchedFlowIDs[p.FlowID] = struct{}{}
		migrationRows = append(migrationRows, &modelnew.MigrationRow{
			ID:         uuid.New(),
			EntityType: modelnew.EntityTypeProviderToGateway,
			OldID:      strconv.Itoa(p.ID),
			NewID:      gateByFlowID[p.FlowID],
			DomainID:   botMappingDomainID,
		})
	}
	for flowID := range gateByFlowID {
		if _, ok := matchedFlowIDs[flowID]; !ok {
			c.log.Warn("bot_mapping.gate_id is set but no matching chat.bot row found for flow_id, skipping", "flow_id", flowID)
		}
	}

	tx, err := c.newDB.Pool().Begin(ctx)
	if err != nil {
		return fail(err)
	}

	if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
		tx.Rollback(ctx)
		return fail(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fail(err)
	}

	c.addRecordsMigrated(len(migrationRows))
	return nil
}

// MigrateFacebookProvidersSyncMode is a no-op: gates are linked once, in
// full mode, from public.bot_mapping -- there is nothing new to pick up on a
// sync run. If bot_mapping gains new gate_id values later, re-run the
// full-mode step (MIGRATION_START_FROM_STEP/MIGRATION_SINGLE_STEP), after
// manually removing the rows it previously created.
func (c *Converter) MigrateFacebookProvidersSyncMode(ctx context.Context) error {
	c.log.Info("facebook/whatsapp providers sync step is a no-op; gates are migrated once in full mode from public.bot_mapping")
	return nil
}
