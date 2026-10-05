package newdb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/gofrs/uuid/v5"
	"github.com/jackc/pgx/v5"
	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
)

type MigrationStore struct {
	store *DB
}

func NewMigrationStore(store *DB) *MigrationStore {
	return &MigrationStore{store: store}
}

func (s *MigrationStore) GetMigrationRow(ctx context.Context, tx pgx.Tx, filters *modelnew.MigrationRowFilters) (*modelnew.MigrationRow, error) {
	if tx == nil {
		return nil, errors.New("transaction required")
	}
	var (
		query = squirrel.StatementBuilder.
			PlaceholderFormat(squirrel.Dollar).
			Select("*").
			From("public.chat_migration")
	)
	query = s.applyFilters(query, filters)
	sql, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}

	result, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[modelnew.MigrationRow])
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (s *MigrationStore) applyFilters(query squirrel.SelectBuilder, filters *modelnew.MigrationRowFilters) squirrel.SelectBuilder {
	if filters == nil {
		return query
	}

	if len(filters.Type) != 0 {
		query = query.Where("entity_type = ANY(?)", filters.Type)
	}
	if len(filters.OldIDs) != 0 {
		query = query.Where("old_id = ANY(?)", filters.OldIDs)
	}
	if len(filters.ExtraKeys) != 0 {
		query = query.Where("extra_key = ANY(?)", filters.ExtraKeys)
	}
	if filters.DomainID != 0 {
		query = query.Where("domain_id = ?", filters.DomainID)
	}

	return query
}

func (s *MigrationStore) GetMigrationRows(ctx context.Context, tx pgx.Tx, filters *modelnew.MigrationRowFilters) ([]*modelnew.MigrationRow, error) {
	if tx == nil {
		return nil, errors.New("transaction required")
	}
	var (
		query = squirrel.StatementBuilder.
			PlaceholderFormat(squirrel.Dollar).
			Select("*").
			From("public.chat_migration")
	)

	query = s.applyFilters(query, filters)

	sql, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[modelnew.MigrationRow])
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (s *MigrationStore) InsertMigrations(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, migrations []*modelnew.MigrationRow) error {
	if len(migrations) == 0 {
		return nil
	}

	// 65535 parameters max for a single query
	// for each row there is 7 params
	// 8000 rows * 7 parameters per row = 56000
	const chunkSize = 8000

	for i := 0; i < len(migrations); i += chunkSize {
		end := i + chunkSize
		if end > len(migrations) {
			end = len(migrations)
		}

		chunk := migrations[i:end]

		var (
			query = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).Insert("public.chat_migration").Columns(
				"id",
				"entity_type",
				"old_id",
				"new_id",
				"domain_id",
				"extra_key",
				"session_id",
			)
		)
		for _, migration := range chunk {
			query = query.Values(
				migration.ID,
				migration.EntityType,
				migration.OldID,
				migration.NewID,
				migration.DomainID,
				migration.ExtraKey,
				sessionID,
			)
		}

		sql, args, err := query.ToSql()
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, sql, args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// NullifyMigrationRowsExtraKey clears extraKey wherever it was left by any
// previously completed sync run -- not just the current session, which at
// call time has not written any chat_migration rows yet and so never
// matches anything.
func (s *MigrationStore) NullifyMigrationRowsExtraKey(ctx context.Context, tx pgx.Tx, extraKey string, migrationType string) error {

	var (
		query = `UPDATE public.chat_migration SET extra_key = NULL WHERE extra_key = $1 AND entity_type = $2`
	)

	_, err := tx.Exec(ctx, query, extraKey, migrationType)
	if err != nil {
		return err
	}

	return nil
}

// GetCompletedSteps returns the set of step names completed within sessionID.
func (s *MigrationStore) GetCompletedSteps(ctx context.Context, sessionID uuid.UUID) (map[string]struct{}, error) {
	rows, err := s.store.Pool().Query(ctx, `SELECT step FROM public.chat_migration_step WHERE status = 'completed' AND session_id = $1`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	completed := make(map[string]struct{})
	for rows.Next() {
		var step string
		if err := rows.Scan(&step); err != nil {
			return nil, err
		}
		completed[step] = struct{}{}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return completed, nil
}

func (s *MigrationStore) MarkStepCompleted(ctx context.Context, sessionID uuid.UUID, step string) error {
	_, err := s.store.Pool().Exec(ctx, `
		INSERT INTO public.chat_migration_step (id, step, status, page_offset, completed_at, session_id)
		VALUES (gen_random_uuid(), $1, 'completed', 0, now(), $2)
		ON CONFLICT (step, session_id) DO UPDATE SET status = 'completed', page_offset = 0, page_cursor = NULL, error = NULL, completed_at = now()
	`, step, sessionID)
	return err
}

// MarkStepReconFailed records that a step's migration work completed but its
// post-step reconciliation check found a mismatch between old_db and new_db.
// The mismatch details are recorded in the existing error column.
func (s *MigrationStore) MarkStepReconFailed(ctx context.Context, sessionID uuid.UUID, step string, errMsg string) error {
	_, err := s.store.Pool().Exec(ctx, `
		INSERT INTO public.chat_migration_step (id, step, status, session_id, error)
		VALUES (gen_random_uuid(), $1, 'recon_failed', $2, $3)
		ON CONFLICT (step, session_id) DO UPDATE SET status = 'recon_failed', error = EXCLUDED.error
	`, step, sessionID, errMsg)
	return err
}

// MarkStepInProgress records that a step has started, independently of any
// page-level checkpoint. Used by steps that don't support resuming: the row
// it creates is picked up by CheckAllStepsCompleted to detect an interrupted
// run on the next attempt.
func (s *MigrationStore) MarkStepInProgress(ctx context.Context, sessionID uuid.UUID, step string) error {
	_, err := s.store.Pool().Exec(ctx, `
		INSERT INTO public.chat_migration_step (id, step, status, session_id)
		VALUES (gen_random_uuid(), $1, 'in_progress', $2)
		ON CONFLICT (step, session_id) DO UPDATE SET status = 'in_progress', error = NULL
	`, step, sessionID)
	return err
}

// GetStepProgress returns the last successfully committed page offset for a step.
// Returns 0 if the step has no recorded progress (first run).
func (s *MigrationStore) GetStepProgress(ctx context.Context, step string) (int, error) {
	var offset int
	err := s.store.Pool().QueryRow(ctx, `
		SELECT page_offset FROM public.chat_migration_step
		WHERE step = $1 AND status != 'completed'
	`, step).Scan(&offset)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return offset, err
}

// offset should be the next offset to process (current offset + page size).
func (s *MigrationStore) SaveStepProgressInTx(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, step string, offset int) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO public.chat_migration_step (id, step, status, page_offset, session_id)
		VALUES (gen_random_uuid(), $1, 'in_progress', $2, $3)
		ON CONFLICT (step, session_id) DO UPDATE SET status = 'in_progress', page_offset = EXCLUDED.page_offset, error = NULL
	`, step, offset, sessionID)
	return err
}

// GetCursorProgress returns the last successfully committed keyset cursor for a step.
// Returns (0, 0, nil) if the step has no recorded cursor progress (first run).
func (s *MigrationStore) GetCursorProgress(ctx context.Context, step string) (initiator int, flowID int, err error) {
	var cursor *string
	err = s.store.Pool().QueryRow(ctx, `
		SELECT page_cursor FROM public.chat_migration_step
		WHERE step = $1 AND status != 'completed'
	`, step).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil || cursor == nil {
		return 0, 0, err
	}
	parts := strings.SplitN(*cursor, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid cursor %q", *cursor)
	}
	initiator, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cursor initiator: %w", err)
	}
	flowID, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cursor flowID: %w", err)
	}
	return initiator, flowID, nil
}

func (s *MigrationStore) SaveCursorProgressInTx(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, step string, initiator int, flowID int) error {
	cursor := fmt.Sprintf("%d:%d", initiator, flowID)
	_, err := tx.Exec(ctx, `
		INSERT INTO public.chat_migration_step (id, step, status, page_cursor, session_id)
		VALUES (gen_random_uuid(), $1, 'in_progress', $2, $3)
		ON CONFLICT (step, session_id) DO UPDATE SET status = 'in_progress', page_cursor = EXCLUDED.page_cursor, error = NULL
	`, step, cursor, sessionID)
	return err
}

// GetIDCursorProgress returns the last successfully committed keyset cursor (a single
// primary-key value) for a step. Returns (0, nil) if the step has no recorded cursor
// progress (first run).
func (s *MigrationStore) GetIDCursorProgress(ctx context.Context, step string) (id int, err error) {
	var cursor *string
	err = s.store.Pool().QueryRow(ctx, `
		SELECT page_cursor FROM public.chat_migration_step
		WHERE step = $1 AND status != 'completed'
	`, step).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil || cursor == nil {
		return 0, err
	}
	id, err = strconv.Atoi(*cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor %q: %w", *cursor, err)
	}
	return id, nil
}

func (s *MigrationStore) SaveIDCursorProgressInTx(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, step string, id int) error {
	cursor := strconv.Itoa(id)
	_, err := tx.Exec(ctx, `
		INSERT INTO public.chat_migration_step (id, step, status, page_cursor, session_id)
		VALUES (gen_random_uuid(), $1, 'in_progress', $2, $3)
		ON CONFLICT (step, session_id) DO UPDATE SET status = 'in_progress', page_cursor = EXCLUDED.page_cursor, error = NULL
	`, step, cursor, sessionID)
	return err
}

// MarkStepFailed records the offset and error message at the point of failure.
func (s *MigrationStore) MarkStepFailed(ctx context.Context, sessionID uuid.UUID, step string, offset int, errMsg string) error {
	_, err := s.store.Pool().Exec(ctx, `
		INSERT INTO public.chat_migration_step (id, step, status, page_offset, error, session_id)
		VALUES (gen_random_uuid(), $1, 'failed', $2, $3, $4)
		ON CONFLICT (step, session_id) DO UPDATE SET status = 'failed', page_offset = EXCLUDED.page_offset, error = EXCLUDED.error
	`, step, offset, errMsg, sessionID)
	return err
}

// CreateSession records the start of a new migration cycle in
// public.chat_migration_sessions. Returns an error if sessionID has already
// been used -- reusing a session_id across migration cycles is not allowed.
func (s *MigrationStore) CreateSession(ctx context.Context, sessionID uuid.UUID, mode string) error {
	exists, err := s.SessionExists(ctx, sessionID)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("session_id %s is already in use, choose a new one", sessionID)
	}

	_, err = s.store.Pool().Exec(ctx, `
		INSERT INTO public.chat_migration_sessions (session_id, started_at, mode)
		VALUES ($1, now(), $2)
	`, sessionID, mode)
	return err
}

// SessionExists reports whether sessionID already has a row in
// public.chat_migration_sessions.
func (s *MigrationStore) SessionExists(ctx context.Context, sessionID uuid.UUID) (bool, error) {
	var exists bool
	err := s.store.Pool().QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM public.chat_migration_sessions WHERE session_id = $1)
	`, sessionID).Scan(&exists)
	return exists, err
}

// CheckSession verifies that sessionID was created for mode. Used before
// running any step other than the first one in a migration cycle, to reject
// a session_id borrowed from a different run or a different run mode.
func (s *MigrationStore) CheckSession(ctx context.Context, sessionID uuid.UUID, mode string) error {
	var actualMode string
	err := s.store.Pool().QueryRow(ctx, `
		SELECT mode FROM public.chat_migration_sessions WHERE session_id = $1
	`, sessionID).Scan(&actualMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("session_id %s was not found in chat_migration_sessions", sessionID)
	}
	if err != nil {
		return err
	}
	if actualMode != mode {
		return fmt.Errorf("session_id %s was created for mode %q, but current run is in mode %q", sessionID, actualMode, mode)
	}
	return nil
}

// GetSessionStartedAt returns the started_at recorded for sessionID in
// public.chat_migration_sessions.
func (s *MigrationStore) GetSessionStartedAt(ctx context.Context, sessionID uuid.UUID) (time.Time, error) {
	var startedAt time.Time
	err := s.store.Pool().QueryRow(ctx, `
		SELECT started_at FROM public.chat_migration_sessions WHERE session_id = $1
	`, sessionID).Scan(&startedAt)
	return startedAt, err
}

// GetLastSyncSessionStartedAt returns the started_at of the most recently
// started sync-mode session other than excludeSessionID, or nil if there is
// none. Used to resolve fromDate for a sync-mode step (see
// .md/enhancements/common/cutoff_date.md); excludeSessionID is the current
// session, which already has its own row by the time this is queried.
func (s *MigrationStore) GetLastSyncSessionStartedAt(ctx context.Context, excludeSessionID uuid.UUID) (*time.Time, error) {
	var startedAt time.Time
	err := s.store.Pool().QueryRow(ctx, `
		SELECT started_at FROM public.chat_migration_sessions
		WHERE mode = 'sync' AND session_id != $1
		ORDER BY started_at DESC LIMIT 1
	`, excludeSessionID).Scan(&startedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &startedAt, nil
}

// GetLastFullSessionStartedAt returns the started_at of the most recently
// started full-mode session, or nil if there is none.
func (s *MigrationStore) GetLastFullSessionStartedAt(ctx context.Context) (*time.Time, error) {
	var startedAt time.Time
	err := s.store.Pool().QueryRow(ctx, `
		SELECT started_at FROM public.chat_migration_sessions
		WHERE mode = 'full'
		ORDER BY started_at DESC LIMIT 1
	`).Scan(&startedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &startedAt, nil
}

// CheckAllStepsCompleted returns an error if public.chat_migration_step has
// any row (any step, any session) whose status isn't 'completed', other than
// the given exceptSteps. exceptSteps is used by messages, the one step that
// supports resuming a failed/in-progress run within the same session -- its
// own incomplete row must not block its own resumed run.
func (s *MigrationStore) CheckAllStepsCompleted(ctx context.Context, exceptSteps ...string) error {
	rows, err := s.store.Pool().Query(ctx, `SELECT DISTINCT step FROM public.chat_migration_step WHERE status != 'completed' AND NOT (step = ANY($1))`, exceptSteps)
	if err != nil {
		return err
	}
	defer rows.Close()

	var incomplete []string
	for rows.Next() {
		var step string
		if err := rows.Scan(&step); err != nil {
			return err
		}
		incomplete = append(incomplete, step)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if len(incomplete) > 0 {
		sort.Strings(incomplete)
		return fmt.Errorf("previous migration run(s) left incomplete steps: %s -- clean up their records and chat_migration_step rows manually before starting a new one", strings.Join(incomplete, ", "))
	}
	return nil
}
