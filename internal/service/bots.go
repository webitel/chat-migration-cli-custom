package service

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
)

// botMappingDomainID is the domain_id recorded for every chat_migration row
// this step writes. public.bot_mapping doesn't carry a domain, and the old
// chat.bot table (which did, via dc) is no longer read -- see
// .md/enhancements/migration_steps/bots_to_contacts.md. Production data has
// exactly one domain (domain_id = 1 everywhere), same assumption already
// documented in clients_to_contacts.recon.md.
const botMappingDomainID = 1

// MigrateBotsToContacts does not create bot contacts: those are configured
// manually in new_db before migration. It only writes chat_migration rows
// mapping each public.bot_mapping.old_bot_id (the old flow_id) to its
// new_bot_id, so downstream steps (members, messages) can resolve
// flow_id -> contact_id. Full mode only; see MigrateBotsToContactsSyncMode.
func (c *Converter) MigrateBotsToContacts(ctx context.Context) error {
	c.log.Debug("starting bots-to-contacts migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx); err != nil {
		return err
	}

	completedSteps, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
	if err != nil {
		return err
	}
	if _, ok := completedSteps[StepClientsToContacts]; !ok {
		return fmt.Errorf("step %q requires step %q to be completed first", StepBotsToContacts, StepClientsToContacts)
	}
	if c.migratePortalClients {
		if _, ok := completedSteps[StepPortalClientsToContacts]; !ok {
			return fmt.Errorf("step %q requires step %q to be completed first", StepBotsToContacts, StepPortalClientsToContacts)
		}
	}

	if err := c.newDB.MigrationStore().MarkStepInProgress(ctx, c.sessionID, StepBotsToContacts); err != nil {
		return err
	}

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, StepBotsToContacts, 0, cause.Error())
		return cause
	}

	mappings, err := c.newDB.BotMappingStore().GetAll(ctx, "public", "bot_mapping")
	if err != nil {
		return fail(err)
	}

	migrationRows := make([]*modelnew.MigrationRow, 0, len(mappings))
	for _, m := range mappings {
		migrationRows = append(migrationRows, &modelnew.MigrationRow{
			ID:         uuid.New(),
			EntityType: modelnew.EntityTypeBotContact,
			OldID:      strconv.Itoa(m.OldBotID),
			NewID:      m.NewBotID,
			DomainID:   botMappingDomainID,
		})
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

// MigrateBotsToContactsSyncMode is a no-op: bots are linked once, in full
// mode, from public.bot_mapping -- there is nothing new to pick up on a sync
// run.
func (c *Converter) MigrateBotsToContactsSyncMode(ctx context.Context) error {
	c.log.Info("bots-to-contacts sync step is a no-op; bots are migrated once in full mode from public.bot_mapping")
	return nil
}
