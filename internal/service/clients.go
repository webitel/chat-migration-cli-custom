package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid

	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
	"github.com/webitel/chat-migration-cli-custom/internal/model/old"
)

func (c *Converter) MigrateClientsToContacts(ctx context.Context) error {
	const perPage = 1000

	c.log.Debug("starting clients-to-contacts migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx); err != nil {
		return err
	}

	types, err := c.newDB.BotMappingStore().GetTypes(ctx)
	if err != nil {
		return err
	}

	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return err
	}

	if err := c.newDB.MigrationStore().MarkStepInProgress(ctx, c.sessionID, StepClientsToContacts); err != nil {
		return err
	}

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, StepClientsToContacts, 0, cause.Error())

		return cause
	}

	lastID := 0

	for {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return fail(err)
		}

		clients, err := c.oldDB.ClientStore().GetFromDate(ctx, lastID, perPage, fromDate, toDate, types)
		if err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		if len(clients) == 0 {
			_ = tx.Rollback(ctx)

			break
		}

		c.log.Debug("clients page fetched", "lastID", lastID, "count", len(clients))

		var (
			contacts      []*modelnew.Contact
			migrationRows []*modelnew.MigrationRow
		)

		for _, client := range clients {
			converted := convertClientToContact(client)

			contacts = append(contacts, converted...)
			for _, contact := range converted {
				migrationRows = append(migrationRows, &modelnew.MigrationRow{
					ID:         uuid.Must(uuid.NewV7()),
					EntityType: modelnew.EntityTypeClientContact,
					OldID:      strconv.Itoa(int(client.ID)),
					NewID:      contact.ID,
					DomainID:   contact.DomainID,
				})
				for _, gateway := range client.Gateways {
					migrationRows = append(migrationRows, &modelnew.MigrationRow{
						ID:         uuid.Must(uuid.NewV7()),
						EntityType: modelnew.EntityTypeGatewayToContact,
						OldID:      strconv.Itoa(int(gateway)),
						NewID:      contact.ID,
						DomainID:   contact.DomainID,
					})
				}
			}
		}

		if err := c.newDB.ContactStore().InsertContacts(ctx, tx, contacts); err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		// query is ORDER BY c.id on the outer SELECT, so the last row is the max id fetched
		lastID = clients[len(clients)-1].ID

		if err := tx.Commit(ctx); err != nil {
			return fail(err)
		}

		c.addRecordsMigrated(len(contacts))

		if len(clients) < perPage {
			break
		}
	}

	return nil
}

func (c *Converter) MigrateClientsToContactsSyncMode(ctx context.Context) error {
	const perPage = 1000

	c.log.Debug("starting clients-to-contacts migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx); err != nil {
		return err
	}

	types, err := c.newDB.BotMappingStore().GetTypes(ctx)
	if err != nil {
		return err
	}

	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return err
	}

	if err := c.newDB.MigrationStore().MarkStepInProgress(ctx, c.sessionID, SyncStepClientsToContacts); err != nil {
		return err
	}

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, SyncStepClientsToContacts, 0, cause.Error())

		return cause
	}

	lastID := 0

	for {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return fail(err)
		}

		clients, err := c.oldDB.ClientStore().GetFromDate(ctx, lastID, perPage, fromDate, toDate, types)
		if err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		if len(clients) == 0 {
			_ = tx.Rollback(ctx)

			break
		}

		c.log.Debug("clients page fetched", "lastID", lastID, "count", len(clients))

		var (
			contacts []*modelnew.Contact
			pairs    []struct {
				client  *old.Client
				contact *modelnew.Contact
			}
		)
		for _, client := range clients {
			converted := convertClientToContact(client)

			contacts = append(contacts, converted...)
			for _, contact := range converted {
				pairs = append(pairs, struct {
					client  *old.Client
					contact *modelnew.Contact
				}{client: client, contact: contact})
			}
		}
		// InsertContactsIgnoreConflicts resolves each contact.ID in place to
		// the row's real id (its own on a fresh insert, the pre-existing
		// row's on conflict) -- migrationRows must be built from that
		// resolved ID, not the one generated before the insert, or a
		// conflicted contact ends up with a chat_migration row pointing at a
		// row that was never inserted.
		rowsAffected, err := c.newDB.ContactStore().InsertContactsIgnoreConflicts(ctx, tx, contacts)
		if err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		var migrationRows []*modelnew.MigrationRow
		for _, p := range pairs {
			migrationRows = append(migrationRows, &modelnew.MigrationRow{
				ID:         uuid.Must(uuid.NewV7()),
				EntityType: modelnew.EntityTypeClientContact,
				OldID:      strconv.Itoa(int(p.client.ID)),
				NewID:      p.contact.ID,
				DomainID:   p.contact.DomainID,
			})
			for _, gateway := range p.client.Gateways {
				migrationRows = append(migrationRows, &modelnew.MigrationRow{
					ID:         uuid.Must(uuid.NewV7()),
					EntityType: modelnew.EntityTypeGatewayToContact,
					OldID:      strconv.Itoa(int(gateway)),
					NewID:      p.contact.ID,
					DomainID:   p.contact.DomainID,
				})
			}
		}

		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			_ = tx.Rollback(ctx)

			return fail(err)
		}

		// query is ORDER BY c.id on the outer SELECT, so the last row is the max id fetched
		lastID = clients[len(clients)-1].ID

		if err := tx.Commit(ctx); err != nil {
			return fail(err)
		}

		c.addRecordsMigrated(int(rowsAffected))

		if len(clients) < perPage {
			break
		}
	}

	return nil
}

func (c *Converter) MigratePortalClientsToContacts(ctx context.Context) error {
	const perPage = 1000

	c.log.Debug("starting portal-clients-to-contacts migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx); err != nil {
		return err
	}

	completedSteps, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
	if err != nil {
		return err
	}

	if _, ok := completedSteps[StepClientsToContacts]; !ok {
		return fmt.Errorf("step %q requires step %q to be completed first", StepPortalClientsToContacts, StepClientsToContacts)
	}

	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return err
	}

	if err := c.newDB.MigrationStore().MarkStepInProgress(ctx, c.sessionID, StepPortalClientsToContacts); err != nil {
		return err
	}

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, StepPortalClientsToContacts, 0, cause.Error())

		return cause
	}

	err = PagerFunc(ctx, perPage, func(ctx context.Context, offset, limit int) (bool, error) {
		tx, err := c.newDB.Pool().Begin(ctx)
		if err != nil {
			return false, err
		}

		iterate := true

		clients, err := c.oldDB.ClientStore().GetPortalClientsFromDate(ctx, offset, limit, fromDate, toDate)
		if err != nil {
			_ = tx.Rollback(ctx)

			return false, err
		}

		if len(clients) < limit {
			iterate = false
		}

		c.log.Debug("portal clients page fetched", "offset", offset, "count", len(clients))
		contacts, pairs := dedupPortalContactsForInsert(clients, c.portalChatIssuerID)
		// old_db can carry two chat.client rows for the same portal user (one
		// per app, e.g. Salmon and Agent) sharing the same (domain_id,
		// subject_id) -- InsertContactsIgnoreConflicts resolves each
		// contact.ID in place to the row's real id (its own on a fresh
		// insert, the pre-existing row's on conflict) -- migrationRows must
		// be built from that resolved ID, not the one generated before the
		// insert, or a conflicted contact ends up with a chat_migration row
		// pointing at a row that was never inserted.
		rowsAffected, err := c.newDB.ContactStore().InsertContactsIgnoreConflicts(ctx, tx, contacts)
		if err != nil {
			_ = tx.Rollback(ctx)

			return false, err
		}

		var migrationRows []*modelnew.MigrationRow
		for _, p := range pairs {
			migrationRows = append(migrationRows, &modelnew.MigrationRow{
				ID:         uuid.Must(uuid.NewV7()),
				EntityType: modelnew.EntityTypeClientContact,
				OldID:      strconv.Itoa(p.client.ID),
				NewID:      p.contact.ID,
				DomainID:   p.contact.DomainID,
			})
		}

		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			_ = tx.Rollback(ctx)

			return false, err
		}

		if err := tx.Commit(ctx); err != nil {
			return false, err
		}

		c.addRecordsMigrated(int(rowsAffected))

		return iterate, nil
	})
	if err != nil {
		return fail(err)
	}

	return nil
}

func (c *Converter) MigratePortalClientsToContactsSyncMode(ctx context.Context) error {
	const perPage = 1000

	c.log.Debug("starting portal-clients-to-contacts migration")

	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx); err != nil {
		return err
	}

	completedSteps, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
	if err != nil {
		return err
	}

	if _, ok := completedSteps[SyncStepClientsToContacts]; !ok {
		return fmt.Errorf("step %q requires step %q to be completed first", SyncStepPortalClientsToContacts, SyncStepClientsToContacts)
	}

	fromDate, toDate, err := c.GetMigrationWindow(ctx)
	if err != nil {
		return err
	}

	if err := c.newDB.MigrationStore().MarkStepInProgress(ctx, c.sessionID, SyncStepPortalClientsToContacts); err != nil {
		return err
	}

	fail := func(cause error) error {
		_ = c.newDB.MigrationStore().MarkStepFailed(ctx, c.sessionID, SyncStepPortalClientsToContacts, 0, cause.Error())

		return cause
	}

	tx, err := c.newDB.Pool().Begin(ctx)
	if err != nil {
		return fail(err)
	}

	err = PagerFunc(ctx, perPage, func(ctx context.Context, offset, limit int) (bool, error) {
		iterate := true

		clients, err := c.oldDB.ClientStore().GetPortalClientsFromDate(ctx, offset, limit, fromDate, toDate)
		if err != nil {
			return false, err
		}

		if len(clients) < limit {
			iterate = false
		}

		c.log.Debug("portal clients page fetched", "offset", offset, "count", len(clients))
		contacts, pairs := dedupPortalContactsForInsert(clients, c.portalChatIssuerID)
		// InsertContactsIgnoreConflicts resolves each contact.ID in place to
		// the row's real id (its own on a fresh insert, the pre-existing
		// row's on conflict) -- migrationRows must be built from that
		// resolved ID, not the one generated before the insert, or a
		// conflicted contact ends up with a chat_migration row pointing at a
		// row that was never inserted.
		rowsAffected, err := c.newDB.ContactStore().InsertContactsIgnoreConflicts(ctx, tx, contacts)
		if err != nil {
			return false, err
		}

		var migrationRows []*modelnew.MigrationRow
		for _, p := range pairs {
			migrationRows = append(migrationRows, &modelnew.MigrationRow{
				ID:         uuid.Must(uuid.NewV7()),
				EntityType: modelnew.EntityTypeClientContact,
				OldID:      strconv.Itoa(p.client.ID),
				NewID:      p.contact.ID,
				DomainID:   p.contact.DomainID,
			})
		}

		if err := c.newDB.MigrationStore().InsertMigrations(ctx, tx, c.sessionID, migrationRows); err != nil {
			return false, err
		}

		c.addRecordsMigrated(int(rowsAffected))

		return iterate, nil
	})
	if err != nil {
		_ = tx.Rollback(ctx)

		return fail(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fail(err)
	}

	return nil
}

func convertClientToContact(client *old.Client) []*modelnew.Contact {
	contacts := make([]*modelnew.Contact, 0, len(client.DomainIDs))
	for _, domain := range client.DomainIDs {
		contacts = append(contacts, &modelnew.Contact{
			BaseModel: modelnew.BaseModel{
				ID:        uuid.Must(uuid.NewV7AtTime(client.CreatedAt)),
				DomainID:  domain,
				CreatedAt: client.CreatedAt,
				UpdatedAt: client.CreatedAt,
			},
			IssuerID:  client.Type,
			SubjectID: client.ExternalID,
			Type:      client.Type,
			Name:      client.Name,
			Username:  buildUsernameForClient(client),
			IsBot:     false,
		})
	}

	return contacts
}

func convertPortalClientToContact(client *old.PortalClient, issuerID string) *modelnew.Contact {
	updatedAt := client.CreatedAt
	if client.UpdatedAt != nil {
		updatedAt = *client.UpdatedAt
	}

	return &modelnew.Contact{
		BaseModel: modelnew.BaseModel{
			ID:        uuid.Must(uuid.NewV7AtTime(client.CreatedAt)),
			DomainID:  client.DomainID,
			CreatedAt: client.CreatedAt,
			UpdatedAt: updatedAt,
		},
		IssuerID:  issuerID,
		SubjectID: client.Sub,
		Type:      client.Type,
		Name:      client.Name,
		Username:  buildUsername(client.Name, client.Type, client.ProfileID.String()),
		IsBot:     false,
	}
}

type contactDedupKey struct {
	DomainID  int
	IssuerID  string
	SubjectID string
}

// dedupPortalContactsForInsert converts a page of portal clients to contacts,
// collapsing clients that share a (domain_id, issuer_id, subject_id) --
// old_db can carry two chat.client rows for the same portal user (e.g. one
// per app, Salmon and Agent) with the same (dc, name). InsertContactsIgnoreConflicts
// upserts via "ON CONFLICT ... DO UPDATE", and Postgres rejects a single
// INSERT statement that would have that DO UPDATE branch affect the same
// target row twice ("ON CONFLICT DO UPDATE command cannot affect row a
// second time") -- so duplicates within one page must be collapsed before
// the insert, not left for ON CONFLICT to resolve. contacts holds one
// *modelnew.Contact per distinct key (first occurrence in clients) to pass
// to InsertContactsIgnoreConflicts; pairs holds one entry per input client,
// with duplicates sharing the same *modelnew.Contact pointer as the first
// occurrence, so once the insert resolves that pointer's ID in place, every
// pair referencing it sees the resolved ID too.
func dedupPortalContactsForInsert(clients []*old.PortalClient, issuerID string) ([]*modelnew.Contact, []struct {
	client  *old.PortalClient
	contact *modelnew.Contact
},
) {
	var (
		contacts []*modelnew.Contact
		pairs    = make([]struct {
			client  *old.PortalClient
			contact *modelnew.Contact
		}, 0, len(clients))
		seen = make(map[contactDedupKey]*modelnew.Contact, len(clients))
	)
	for _, client := range clients {
		contact := convertPortalClientToContact(client, issuerID)

		key := contactDedupKey{DomainID: contact.DomainID, IssuerID: contact.IssuerID, SubjectID: contact.SubjectID}
		if canonical, ok := seen[key]; ok {
			contact = canonical
		} else {
			seen[key] = contact
			contacts = append(contacts, contact)
		}

		pairs = append(pairs, struct {
			client  *old.PortalClient
			contact *modelnew.Contact
		}{client: client, contact: contact})
	}

	return contacts, pairs
}

func buildUsernameForClient(cli *old.Client) string {
	return buildUsername(cli.Name, cli.Type, cli.ExternalID)
}

func buildUsername(name, userType, userID string) string {
	replacedName := replaceCharactersForUsername(name)
	replacedType := replaceCharactersForUsername(userType)
	replacedID := replaceCharactersForUsername(userID)

	return fmt.Sprintf("%s_%s_%s", replacedName, replacedType, replacedID)
}

func replaceCharactersForUsername(in string) string {
	lowered := strings.ToLower(in)
	replacedBlank := strings.ReplaceAll(lowered, " ", "_")
	replacedDash := strings.ReplaceAll(replacedBlank, "-", "_")

	return replacedDash
}
