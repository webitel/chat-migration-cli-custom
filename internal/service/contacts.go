package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (c *Converter) SyncContactsVias(ctx context.Context) error {
	if err := c.newDB.MigrationStore().CheckAllStepsCompleted(ctx); err != nil {
		return err
	}

	stepName := StepSyncContactVias
	deps := []string{StepClientsToContacts, StepFacebookAndWhatsApp}

	if c.isSyncMode {
		stepName = SyncStepSyncContactVias
		deps = []string{SyncStepClientsToContacts}
	}

	completedSteps, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
	if err != nil {
		return err
	}

	for _, dep := range deps {
		if _, ok := completedSteps[dep]; !ok {
			return fmt.Errorf("step %q requires step %q to be completed first", stepName, dep)
		}
	}

	tx, err := c.newDB.Pool().BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	rowsAffected, err := c.newDB.ContactStore().SyncContactVias(ctx, tx)
	if err != nil {
		return err
	}

	c.addRecordsMigrated(int(rowsAffected))

	return tx.Commit(ctx)
}
