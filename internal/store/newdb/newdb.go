package newdb

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// requiredTables lists the tables that must exist in the new DB before a
// migration run, and that InitTables creates.
var requiredTables = []string{"chat_migration", "chat_migration_step", "bot_mapping", "chat_migration_sessions"}

// DB is a connection to the new micro-services database.
type DB struct {
	pool *pgxpool.Pool

	contactStore        *ContactStore
	threadStore         *ThreadStore
	threadDialogStore   *ThreadDialogStore
	migrationStore      *MigrationStore
	messageStore        *MessageStore
	directSettingsStore *DirectSettingsStore
	appStore            *AppStore
	botMappingStore     *BotMappingStore
}

func New(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool}
}

func (db *DB) Pool() *pgxpool.Pool { return db.pool }

func (db *DB) Close() { db.pool.Close() }

func (db *DB) ContactStore() *ContactStore {
	if db.contactStore == nil {
		db.contactStore = NewContactStore(db)
	}
	return db.contactStore
}

func (db *DB) ThreadStore() *ThreadStore {
	if db.threadStore == nil {
		db.threadStore = NewThreadStore(db)
	}
	return db.threadStore
}

func (db *DB) DirectSettingsStore() *DirectSettingsStore {
	if db.directSettingsStore == nil {
		db.directSettingsStore = NewDirectSettingsStore(db)
	}
	return db.directSettingsStore
}

func (db *DB) MigrationStore() *MigrationStore {
	if db.migrationStore == nil {
		db.migrationStore = NewMigrationStore(db)
	}
	return db.migrationStore
}

func (db *DB) MessageStore() *MessageStore {
	if db.messageStore == nil {
		db.messageStore = NewMessageStore(db)
	}
	return db.messageStore
}

// InitTables creates the tables required to run a migration, if they don't
// already exist. Safe to run repeatedly.
func (db *DB) InitTables(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.chat_migration(
	id UUID PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
	entity_type TEXT NOT NULL,
	old_id TEXT NOT NULL,
	new_id UUID NOT NULL,
	domain_id INT,
	extra_key TEXT,
	session_id UUID NOT NULL
);

	CREATE UNIQUE INDEX IF NOT EXISTS chat_migration_entity_old_id_uindex ON public.chat_migration (entity_type, old_id, domain_id, new_id, extra_key);
	CREATE INDEX IF NOT EXISTS chat_migration_new_id_index ON public.chat_migration (new_id);

	CREATE TABLE IF NOT EXISTS public.chat_migration_step(
	id UUID PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
	step TEXT NOT NULL,
	status TEXT NOT NULL,
	session_id UUID NOT NULL
);

	CREATE UNIQUE INDEX IF NOT EXISTS chat_migration_step_step_session_id_uindex ON public.chat_migration_step (step, session_id);

	ALTER TABLE public.chat_migration_step ADD COLUMN IF NOT EXISTS page_offset INT NOT NULL DEFAULT 0;
	ALTER TABLE public.chat_migration_step ADD COLUMN IF NOT EXISTS page_cursor TEXT;
	ALTER TABLE public.chat_migration_step ADD COLUMN IF NOT EXISTS error TEXT;
	ALTER TABLE public.chat_migration_step ADD COLUMN IF NOT EXISTS completed_at TIMESTAMP WITH TIME ZONE;

	CREATE TABLE IF NOT EXISTS public.bot_mapping(
	old_bot_id INTEGER PRIMARY KEY,
	new_bot_id UUID NOT NULL,
	type TEXT NOT NULL,
	gate_id UUID
);

	CREATE TABLE IF NOT EXISTS public.chat_migration_sessions(
	session_id UUID PRIMARY KEY,
	started_at TIMESTAMP WITH TIME ZONE NOT NULL,
	mode TEXT NOT NULL
);
	`)
	return err
}

// CheckTablesExist returns an error listing any of the required tables that
// are missing from the new DB.
func (db *DB) CheckTablesExist(ctx context.Context) error {
	rows, err := db.pool.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = ANY($1)
	`, requiredTables)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]struct{}, len(requiredTables))
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var missing []string
	for _, table := range requiredTables {
		if _, ok := existing[table]; !ok {
			missing = append(missing, table)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required tables: %s (run with --init to create them)", strings.Join(missing, ", "))
	}

	return nil
}

func (db *DB) ThreadDialogStore() *ThreadDialogStore {
	if db.threadDialogStore == nil {
		db.threadDialogStore = &ThreadDialogStore{store: db}
	}
	return db.threadDialogStore
}

func (db *DB) AppStore() *AppStore {
	if db.appStore == nil {
		db.appStore = NewAppStore(db)
	}
	return db.appStore
}

func (db *DB) BotMappingStore() *BotMappingStore {
	if db.botMappingStore == nil {
		db.botMappingStore = NewBotMappingStore(db)
	}
	return db.botMappingStore
}
