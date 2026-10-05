package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/jackc/pgx/v5"
	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
	"github.com/webitel/chat-migration-cli-custom/internal/store/newdb"
	"github.com/webitel/chat-migration-cli-custom/internal/store/olddb"
)

const (
	StepClientsToContacts       = "clients_to_contacts"
	StepPortalClientsToContacts = "portal_client_to_contact"
	StepPortalAppsToAccounts    = "portal_apps_to_accounts"
	StepBotsToContacts          = "bots_to_contacts"
	StepConversations           = "conversations"
	StepMembers                 = "members"
	StepMessages                = "messages"
	StepGateways                = "gateways"

	StepFacebookAndWhatsApp = "facebook_and_whatsapp"
	StepSyncContactVias     = "sync_contact_vias"
)

const (
	SyncStepClientsToContacts       = "sync_mode_clients_to_contacts"
	SyncStepPortalClientsToContacts = "sync_mode_portal_client_to_contact"
	SyncStepBotsToContacts          = "sync_mode_bots_to_contacts"
	SyncStepConversations           = "sync_mode_conversations"
	SyncStepMembers                 = "sync_mode_members"
	SyncStepMessages                = "sync_mode_messages"
	SyncStepGateways                = "sync_mode_gateways"

	SyncStepFacebookAndWhatsApp = "sync_mode_facebook_and_whatsapp"
	SyncStepSyncContactVias     = "sync_mode_sync_contact_vias"
)

const (
	migrationModeFull = "full"
	migrationModeSync = "sync"
)

// migrationEpoch is the fromDate used for full-mode steps, and the fallback
// fromDate for a sync-mode step when chat_migration_sessions has neither a
// prior sync nor a prior full session (see
// .md/enhancements/common/cutoff_date.md).
var migrationEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

type Resolver struct {
	db *newdb.DB
}

func NewResolver(db *newdb.DB) *Resolver {
	return &Resolver{db: db}
}

func (r *Resolver) ResolveMigrationRow(ctx context.Context, tx pgx.Tx, entityType modelnew.EntityType, oldID string, extraKey *string, domainID int) (*modelnew.MigrationRow, error) {
	filters := &modelnew.MigrationRowFilters{
		Type:     []modelnew.EntityType{entityType},
		OldIDs:   []string{oldID},
		DomainID: domainID,
	}
	if extraKey != nil {
		filters.ExtraKeys = []string{*extraKey}
	}
	return r.db.MigrationStore().GetMigrationRow(ctx, tx, filters)
}

func (r *Resolver) ResolveMigrationRows(ctx context.Context, tx pgx.Tx, filters *modelnew.MigrationRowFilters) ([]*modelnew.MigrationRow, error) {
	return r.db.MigrationStore().GetMigrationRows(ctx, tx, filters)
}

type Converter struct {
	log       *slog.Logger
	oldDB     *olddb.DB
	newDB     *newdb.DB
	resolver  *Resolver
	encryptor *Encryptor

	isSyncMode           bool
	migratePortalClients bool
	portalChatIssuerID   string
	stepRecordsMigrated  int64
	sessionID            uuid.UUID
}

type MigrationStep struct {
	Name string
	Run  func(ctx context.Context) error
	// Reconcile is optional. When set, it runs after Run succeeds and before
	// the step is marked completed: on a mismatch the step is marked
	// recon_failed instead of completed, and the migration run stops.
	Reconcile func(ctx context.Context) (*ReconciliationResult, error)
}

// completeStep runs step.Reconcile (if set) and then records the step's
// final status: completed, or recon_failed if reconciliation found a mismatch.
func (c *Converter) completeStep(ctx context.Context, step MigrationStep) error {
	if step.Reconcile != nil {
		result, err := step.Reconcile(ctx)
		if err != nil {
			return fmt.Errorf("reconciliation for step %q: %w", step.Name, err)
		}
		c.log.Info("reconciliation result", "step", step.Name, "passed", result.Passed(), "values", result.Values)
		if !result.Passed() {
			errMsg := strings.Join(result.Failed, "; ")
			if err := c.newDB.MigrationStore().MarkStepReconFailed(ctx, c.sessionID, step.Name, errMsg); err != nil {
				return err
			}
			return fmt.Errorf("reconciliation failed for step %q: %s", step.Name, errMsg)
		}
	}
	return c.newDB.MigrationStore().MarkStepCompleted(ctx, c.sessionID, step.Name)
}

// StepDuration records how long a single migration step took to execute,
// and how many records were migrated during that step.
type StepDuration struct {
	Name            string
	Duration        time.Duration
	RecordsMigrated int64
}

func NewConverter(oldDB *olddb.DB, modelnewDB *newdb.DB, encryptor *Encryptor, isSyncMode bool, migratePortalClients bool, portalChatIssuerID string, sessionID uuid.UUID) *Converter {
	return &Converter{
		log:                  slog.Default(),
		oldDB:                oldDB,
		newDB:                modelnewDB,
		resolver:             NewResolver(modelnewDB),
		encryptor:            encryptor,
		isSyncMode:           isSyncMode,
		migratePortalClients: migratePortalClients,
		portalChatIssuerID:   portalChatIssuerID,
		sessionID:            sessionID,
	}
}

func (c *Converter) Migrate(ctx context.Context) error {
	return c.runSteps(ctx)
}

func (c *Converter) MigrateFromStep(ctx context.Context, stepName string) error {
	return c.runStepsFrom(ctx, stepName)
}

func (c *Converter) MigrateSingleStep(ctx context.Context, stepName string) error {
	return c.runSingleStep(ctx, stepName)
}

func (c *Converter) runSingleStep(ctx context.Context, stepName string) error {
	if stepName == "" {
		return errors.New("step name is required")
	}

	var steps []MigrationStep
	if c.isSyncMode {
		steps = c.getSyncModeMigrationSteps()
	} else {
		steps = c.getMigrationSteps()
	}

	var step *MigrationStep
	for i := range steps {
		if steps[i].Name == stepName {
			step = &steps[i]
			break
		}
	}
	if step == nil {
		return fmt.Errorf("unknown migration step: %s", stepName)
	}

	if !c.isSyncMode {
		completed, err := c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
		if err != nil {
			return err
		}
		if _, ok := completed[step.Name]; ok {
			return fmt.Errorf("migration step %q is already completed; re-run is not supported outside sync mode - restore the target database and retry", step.Name)
		}
	}

	if err := c.ensureSession(ctx, step.Name); err != nil {
		return err
	}

	c.log.Info("migration step started", "step", step.Name)
	c.resetStepRecordsMigrated()
	stepStart := time.Now()
	if err := step.Run(ctx); err != nil {
		return err
	}
	duration := time.Since(stepStart)
	recordsMigrated := c.stepRecordsMigrated
	if err := c.completeStep(ctx, *step); err != nil {
		return err
	}
	c.log.Info("migration step completed", "step", step.Name)
	c.logStepDurations([]StepDuration{{Name: step.Name, Duration: duration, RecordsMigrated: recordsMigrated}})
	return nil
}

func (c *Converter) runStepsFrom(ctx context.Context, startFrom string) error {
	if startFrom == "" {
		return errors.New("step requires to start from it")
	}
	var (
		steps     []MigrationStep
		completed map[string]struct{}
		err       error
	)

	if c.isSyncMode {
		steps = c.getSyncModeMigrationSteps()
		completed = make(map[string]struct{})

	} else {
		steps = c.getMigrationSteps()
		completed, err = c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
		if err != nil {
			return err
		}
	}

	var (
		firstStepIndex int
		found          bool
	)
	for i, step := range steps {
		if step.Name == startFrom {
			found = true
			if _, alreadyCompleted := completed[step.Name]; alreadyCompleted {
				if i > 0 {
					for s, nextUncompletedStep := range steps[i-1:] {
						if _, alreadyCompleted := completed[nextUncompletedStep.Name]; !alreadyCompleted {
							firstStepIndex = s
							break
						}
					}
				}
			} else {
				firstStepIndex = i
			}
			break
		}
	}

	if !found {
		return fmt.Errorf("unknown migration step: %s", startFrom)
	}

	durations := make([]StepDuration, 0, len(steps)-firstStepIndex)
	for _, step := range steps[firstStepIndex:] {
		if _, ok := completed[step.Name]; ok {
			c.log.Info("migration step already completed, skipping", "step", step.Name)
			continue
		}

		if err := c.ensureSession(ctx, step.Name); err != nil {
			c.logStepDurations(durations)
			return err
		}

		c.log.Info("migration step started", "step", step.Name)
		c.resetStepRecordsMigrated()
		stepStart := time.Now()
		if err := step.Run(ctx); err != nil {
			c.logStepDurations(durations)
			return err
		}
		durations = append(durations, StepDuration{Name: step.Name, Duration: time.Since(stepStart), RecordsMigrated: c.stepRecordsMigrated})

		if err := c.completeStep(ctx, step); err != nil {
			c.logStepDurations(durations)
			return err
		}
		c.log.Info("migration step completed", "step", step.Name)
	}
	c.logStepDurations(durations)
	return nil
}

func (c *Converter) runSteps(ctx context.Context) error {
	var (
		steps     []MigrationStep
		completed map[string]struct{}
		err       error
	)

	if c.isSyncMode {
		steps = c.getSyncModeMigrationSteps()
		completed = make(map[string]struct{})

	} else {
		steps = c.getMigrationSteps()
		completed, err = c.newDB.MigrationStore().GetCompletedSteps(ctx, c.sessionID)
		if err != nil {
			return err
		}
	}

	durations := make([]StepDuration, 0, len(steps))
	for _, step := range steps {
		if _, ok := completed[step.Name]; ok {
			c.log.Info("migration step already completed, skipping", "step", step.Name)
			continue
		}

		if err := c.ensureSession(ctx, step.Name); err != nil {
			c.logStepDurations(durations)
			return err
		}

		c.log.Info("migration step started", "step", step.Name)
		c.resetStepRecordsMigrated()
		stepStart := time.Now()
		if err := step.Run(ctx); err != nil {
			c.logStepDurations(durations)
			return err
		}
		durations = append(durations, StepDuration{Name: step.Name, Duration: time.Since(stepStart), RecordsMigrated: c.stepRecordsMigrated})

		if err := c.completeStep(ctx, step); err != nil {
			c.logStepDurations(durations)
			return err
		}
		c.log.Info("migration step completed", "step", step.Name)
	}
	c.logStepDurations(durations)
	return nil
}

// ensureSession records or validates the migration session for stepName
// in public.chat_migration_sessions. The first step of a migration cycle
// (clients_to_contacts or sync_mode_clients_to_contacts) creates the session
// row; every other step verifies that the session_id was created for the
// same run mode.
func (c *Converter) ensureSession(ctx context.Context, stepName string) error {
	mode := migrationModeFull
	if c.isSyncMode {
		mode = migrationModeSync
	}

	if stepName == StepClientsToContacts || stepName == SyncStepClientsToContacts {
		return c.newDB.MigrationStore().CreateSession(ctx, c.sessionID, mode)
	}
	return c.newDB.MigrationStore().CheckSession(ctx, c.sessionID, mode)
}

func (c *Converter) resetStepRecordsMigrated() {
	c.stepRecordsMigrated = 0
}

func (c *Converter) addRecordsMigrated(n int) {
	c.stepRecordsMigrated += int64(n)
}

// logStepDurations emits a summary report of how long each executed
// migration step took, how many records were migrated, plus the total
// elapsed time and record count across all of them.
// It is a no-op when durations is empty (e.g. every step in the run was
// already completed and skipped).
func (c *Converter) logStepDurations(durations []StepDuration) {
	if len(durations) == 0 {
		return
	}
	var (
		total        time.Duration
		totalRecords int64
	)
	for _, d := range durations {
		total += d.Duration
		totalRecords += d.RecordsMigrated
		c.log.Info("migration step duration", "step", d.Name, "duration", d.Duration.String(), "records_migrated", d.RecordsMigrated)
	}
	c.log.Info("migration duration summary", "steps", len(durations), "total_duration", total.String(), "total_records_migrated", totalRecords)
}

func (c *Converter) getMigrationSteps() []MigrationStep {
	steps := []MigrationStep{
		{Name: StepClientsToContacts, Run: c.MigrateClientsToContacts, Reconcile: c.ReconcileClientsToContacts},
	}
	if c.migratePortalClients {
		steps = append(steps, MigrationStep{Name: StepPortalClientsToContacts, Run: c.MigratePortalClientsToContacts, Reconcile: c.ReconcilePortalClientsToContacts})
	}
	steps = append(steps, []MigrationStep{
		{Name: StepBotsToContacts, Run: c.MigrateBotsToContacts, Reconcile: c.ReconcileBotsToContacts},
		{Name: StepConversations, Run: c.MigrateConversations, Reconcile: c.ReconcileConversations},
		{Name: StepMembers, Run: c.MigrateMembers, Reconcile: c.ReconcileMembers},
		{Name: StepMessages, Run: c.MigrateMessages, Reconcile: c.ReconcileMessages},
		{Name: StepFacebookAndWhatsApp, Run: c.MigrateFacebookProviders, Reconcile: c.ReconcileFacebookProviders},
		{Name: StepSyncContactVias, Run: c.SyncContactsVias, Reconcile: c.ReconcileSyncContactVias},
	}...)
	return steps
}
func (c *Converter) getSyncModeMigrationSteps() []MigrationStep {
	steps := []MigrationStep{
		{Name: SyncStepClientsToContacts, Run: c.MigrateClientsToContactsSyncMode, Reconcile: c.ReconcileClientsToContactsSyncMode},
	}
	if c.migratePortalClients {
		steps = append(steps, MigrationStep{Name: SyncStepPortalClientsToContacts, Run: c.MigratePortalClientsToContactsSyncMode, Reconcile: c.ReconcilePortalClientsToContactsSyncMode})
	}
	steps = append(steps, []MigrationStep{
		{Name: SyncStepBotsToContacts, Run: c.MigrateBotsToContactsSyncMode},
		{Name: SyncStepConversations, Run: c.MigrateConversationsSyncMode, Reconcile: c.ReconcileConversationsSyncMode},
		{Name: SyncStepMembers, Run: c.MigrateMembersSyncMode, Reconcile: c.ReconcileMembersSyncMode},
		{Name: SyncStepMessages, Run: c.MigrateMessagesSyncMode, Reconcile: c.ReconcileMessagesSyncMode},
		{Name: SyncStepFacebookAndWhatsApp, Run: c.MigrateFacebookProvidersSyncMode},
		{Name: SyncStepSyncContactVias, Run: c.SyncContactsVias, Reconcile: c.ReconcileSyncContactViasSyncMode},
	}...)
	return steps
}

// GetMigrationWindow returns the fixed, half-open [fromDate, toDate) window
// a step must use to select source data, per
// .md/enhancements/common/cutoff_date.md. toDate is always the current
// session's own started_at. fromDate is migrationEpoch for full mode; for
// sync mode it's the started_at of the most recent other sync session,
// falling back to the most recent full session, falling back to
// migrationEpoch if neither exists.
func (c *Converter) GetMigrationWindow(ctx context.Context) (fromDate, toDate time.Time, err error) {
	toDate, err = c.newDB.MigrationStore().GetSessionStartedAt(ctx, c.sessionID)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("resolving current session started_at: %w", err)
	}

	if !c.isSyncMode {
		return migrationEpoch, toDate, nil
	}

	prevSync, err := c.newDB.MigrationStore().GetLastSyncSessionStartedAt(ctx, c.sessionID)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("resolving previous sync session: %w", err)
	}
	if prevSync != nil {
		return *prevSync, toDate, nil
	}

	lastFull, err := c.newDB.MigrationStore().GetLastFullSessionStartedAt(ctx)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("resolving last full session: %w", err)
	}
	if lastFull != nil {
		return *lastFull, toDate, nil
	}

	return migrationEpoch, toDate, nil
}

// getConversationFlowIDs returns the distinct public.bot_mapping.old_bot_id
// values used to restrict which old conversations (chat.conversation's
// props->>'flow') are eligible for migration -- read independently by
// conversations, members and messages, since each re-derives the same
// (initiator, flow_id) grouping and can run on its own via
// MIGRATION_START_FROM_STEP. An empty bot_mapping is treated as a
// misconfiguration rather than "nothing to migrate".
func (c *Converter) getConversationFlowIDs(ctx context.Context) ([]int32, error) {
	flowIDs, err := c.newDB.BotMappingStore().GetAllOldBotIDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(flowIDs) == 0 {
		return nil, errors.New("public.bot_mapping has no rows -- populate it with flow_id mappings before migrating conversations")
	}
	return flowIDs, nil
}

func PagerFunc(ctx context.Context, perPage int, do func(ctx context.Context, offset, limit int) (bool, error)) error {
	var (
		limit   = perPage
		iterate = true
		err     error
	)
	for offset := 0; iterate; offset += perPage {
		iterate, err = do(ctx, offset, limit)
		if err != nil {
			return err
		}
	}
	return nil
}
