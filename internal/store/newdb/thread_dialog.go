package newdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
)

type ThreadDialogStore struct {
	store *DB
}

func (s *ThreadDialogStore) InsertThreadDialogs(ctx context.Context, tx pgx.Tx, threadDialogs []*modelnew.ThreadDialog) error {
	if len(threadDialogs) == 0 {
		return nil
	}

	var (
		threadDialogQuery = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).Insert("im_thread.thread_dialog").Columns(
			"id",
			"domain_id",
			"created_at",
			"updated_at",
			"member_id",
			"thread_id",
			"thread_role",
			"invited_by",
			"leave_reason",
			"deleted_at",
			"via",
		)
		threadPermissionQuery = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).Insert("im_thread.thread_permission").Columns(
			"thread_id",
			"thread_dialog_id",
			"can_send_messages",
			"can_add_members",
			"can_remove_members",
			"can_change_members_permissions",
			"can_change_thread_info",
		)
	)
	for _, threadDialog := range threadDialogs {
		threadDialogQuery = threadDialogQuery.Values(
			threadDialog.ID,
			threadDialog.DomainID,
			threadDialog.CreatedAt,
			threadDialog.UpdatedAt,
			threadDialog.MemberID,
			threadDialog.ThreadID,
			threadDialog.ThreadRole,
			threadDialog.InvitedBy,
			threadDialog.LeaveReason,
			threadDialog.DeletedAt,
			threadDialog.Via,
		)
		threadPermissionQuery = threadPermissionQuery.Values(
			threadDialog.ThreadID,
			threadDialog.ID,
			true,
			true,
			true,
			true,
			true,
		)
	}

	sql, args, err := threadDialogQuery.ToSql()
	if err != nil {
		return err
	}

	sqlPermission, argsPermission, err := threadPermissionQuery.ToSql()
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, sql, args...)
	if err != nil {
		return annotatePgError(err)
	}

	_, err = tx.Exec(ctx, sqlPermission, argsPermission...)
	if err != nil {
		return annotatePgError(err)
	}

	return nil
}

// annotatePgError adds constraint/detail information from a Postgres error to
// the returned error message. pgconn.PgError.Error() only includes the
// message and SQLSTATE, not the constraint name or the DETAIL line (which
// for a unique violation names the actual conflicting column values) --
// those are needed to diagnose which row collided.
func annotatePgError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("%w (constraint=%s, detail=%s)", err, pgErr.ConstraintName, pgErr.Detail)
	}

	return err
}
