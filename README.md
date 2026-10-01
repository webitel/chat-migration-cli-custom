# chat-migration-cli

A CLI tool that migrates data from the legacy monolithic chat service database to the new microservices database. Both sources are PostgreSQL.

The tool supports two modes:

- **Migration mode** (default) — full one-shot migration of all historical data.
- **Sync mode** — incremental re-run that picks up only records created since the last completed migration step. Safe to run repeatedly without duplicating data.

## How it works

Migration runs as an ordered sequence of steps. Each step is idempotent and resumable — progress is checkpointed after every page, so a failed or interrupted run can be restarted from where it left off.

### Migration mode steps

| Order | Step | What it does |
|-------|------|--------------|
| 1 | `clients_to_contacts` | Migrates external client users to contacts |
| 1b | `portal_client_to_contact` | Migrates portal clients to contacts (only runs if `MIGRATION_MIGRATE_PORTAL_CLIENTS` is enabled) |
| 2 | `bots_to_contacts` | Links flow bots to their already-existing contacts via `public.bot_mapping` (bots are created manually in the new database before migration; this step only writes the `flow_id -> contact_id` mapping) |
| 3 | `conversations` | Groups legacy conversations by `(initiator, flow)` and creates chat threads |
| 4 | `members` | Creates thread dialog members for all participants |
| 5 | `messages` | Migrates all messages, file attachments and interactive content |
| 6 | `facebook_and_whatsapp` | Migrates Facebook and WhatsApp provider configs to gates and Meta apps. Makes outbound HTTP calls to the Meta Graph API to resolve WhatsApp Business Account phone numbers |
| 7 | `sync_contact_vias` | Syncs contact communication channels (vias) after all contacts and providers are in place |

Steps that have already completed are skipped automatically on re-runs. Use `MIGRATION_START_FROM_STEP` to resume from a specific step.

### Sync mode steps

Sync mode runs a parallel set of steps prefixed with `sync_mode_`. Each step queries the last completion timestamp of its counterpart migration step and fetches only records created after that point.

| Order | Step | What it does |
|-------|------|--------------|
| 1 | `sync_mode_clients_to_contacts` | Inserts new clients created since last run; existing records are skipped |
| 1b | `sync_mode_portal_client_to_contact` | Inserts new portal clients created since last run (only runs if `MIGRATION_MIGRATE_PORTAL_CLIENTS` is enabled) |
| 2 | `sync_mode_bots_to_contacts` | No-op: bots are linked once, in full mode, from `public.bot_mapping` |
| 3 | `sync_mode_conversations` | Creates threads for new conversations; adds new conversation IDs to existing threads for the same `(initiator, flow)` pair |
| 4 | `sync_mode_members` | Adds full member set to newly created threads; adds only new internal users to existing threads |
| 5 | `sync_mode_messages` | Migrates messages from conversations created since last run |
| 6 | `sync_mode_facebook_and_whatsapp` | Migrates new Facebook and WhatsApp provider configs created since last run |
| 7 | `sync_mode_sync_contact_vias` | Re-syncs contact communication channels |

On the very first sync run (when no previous migration has completed), the timestamp falls back to the epoch, so all records are processed — equivalent to running a full migration.

### Pagination

Steps use one of three pagination strategies:

- **Two-value keyset pagination** (`conversations`, `members`, `messages`) ordered by `(initiator_id, flow_id)`. This avoids the O(N²) cost of OFFSET-based pagination on large datasets.
- **Single-value keyset pagination** (`clients_to_contacts`) — `WHERE c.id > $afterID ORDER BY c.id LIMIT $limit` on the legacy client's primary key, for the same reason as above.
- **Offset pagination** (`facebook_and_whatsapp`) — plain `OFFSET`/`LIMIT` paging over a deterministically ordered query, so repeated runs resume against the same row order.

Every paginated step commits its destination-DB writes and saves its checkpoint in the same transaction, once per page, so a crash or transient error at any point resumes from the last committed page rather than restarting the whole step. `bots_to_contacts` is the exception: it doesn't read the old database at all, just copies `public.bot_mapping` into the tracking table in one transaction (see below).

## Configuration

All options are read from environment variables prefixed with `MIGRATION_`.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `MIGRATION_OLD_DB_DSN` | yes | — | Postgres DSN for the legacy chat database |
| `MIGRATION_NEW_DB_DSN` | yes | — | Postgres DSN for the new microservices database |
| `MIGRATION_OLD_DB_MAX_CONNS` | no | `5` | Connection pool size for the legacy DB |
| `MIGRATION_NEW_DB_MAX_CONNS` | no | `10` | Connection pool size for the new DB |
| `MIGRATION_SYNC_MODE` | no | `false` | Run in sync mode instead of full migration mode |
| `MIGRATION_MIGRATE_PORTAL_CLIENTS` | no | `false` | Include portal clients in the migration (runs the `portal_client_to_contact` step). When enabled, consider adding an index on the legacy database's `portal.identity` table to speed up that step. See "Portal client migration index" below. |
| `MIGRATION_START_FROM_STEP` | no | _(all)_ | Start from this step, skipping earlier ones |
| `MIGRATION_SINGLE_STEP` | no | `false` | Run only the step named by `MIGRATION_START_FROM_STEP`, then stop. Requires `MIGRATION_START_FROM_STEP` to be set. Resumes a not-yet-completed step from its last saved progress; fails if the step is already completed (outside sync mode - sync-mode steps remain re-runnable) |
| `MIGRATION_LOG_LEVEL` | no | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `MIGRATION_LOG_JSON` | no | `false` | Emit structured JSON logs instead of text |
| `MIGRATION_ENCRYPTION_KEY` | yes | — | 32-byte AES-256 key used to encrypt provider tokens at rest |
| `MIGRATION_SESSION_ID` | yes | — | UUID identifying the records created by this migration cycle. Generate a new one per full/sync cycle; every step of that cycle must be run with the same value. Not required with `--init` |

DSN format: `postgres://user:password@host:5432/dbname?sslmode=disable`

`MIGRATION_ENCRYPTION_KEY` must be exactly 32 characters (256 bits). Tokens stored in the new database (Facebook page access tokens and WhatsApp Business access tokens) are encrypted with AES-256-GCM using this key.

### Portal client migration index

When `MIGRATION_MIGRATE_PORTAL_CLIENTS` is enabled, the `portal_client_to_contact` step (and its sync-mode counterpart) queries the legacy database's `portal.identity` table. To speed up this step on large deployments, create an index on the old database *before* running the migration:

```sql
CREATE INDEX IF NOT EXISTS identity_top_updated_at_idx ON portal.identity (top, updated_at DESC);
```

This index is optional and not created automatically by the tool — if it does not exist, the migration will still complete but may be slower on tables with many portal identities. Run this statement before starting the migration rather than while it is in progress: a plain `CREATE INDEX` takes a lock on `portal.identity` that blocks writes to that table for the duration of the build, and `portal.identity` lives in the legacy *production* database. If you need to build it against a live system, use `CREATE INDEX CONCURRENTLY IF NOT EXISTS ...` instead (it cannot run inside a transaction, and leaves an `INVALID` index behind — safe to `DROP` and retry — if it fails partway through). Once created, the index also speeds up subsequent sync-mode runs of `sync_mode_portal_client_to_contact`.

### Bot mapping

Bot contacts are **not** created by this tool — they must already exist in the new microservices database before running the migration. `bots_to_contacts` only links each old bot's chat history to its already-existing contact, by reading `public.bot_mapping` (created by `--init`) and copying it into the tracking table (`public.chat_migration`) so downstream steps (`members`, `messages`) can resolve `flow_id -> contact_id`.

**Who manages it:** You create and populate `public.bot_mapping` yourself *before* running migration. The tool never seeds or migrates its contents — only reads them.

**Table shape** (created by `--init`, see above):
```sql
CREATE TABLE public.bot_mapping (
  old_bot_id INTEGER PRIMARY KEY,
  new_bot_id UUID NOT NULL,
  type TEXT NOT NULL
);
```
- `old_bot_id` — must equal the old database's `chat.bot.flow_id`.
- `new_bot_id` — must equal an existing new-database `im_contact.contact.id` row where `is_bot = true`.

**Validation:** The tool does *not* check that `new_bot_id` corresponds to an existing contact. A misconfigured mapping will surface as a failure in a later migration step (e.g., a foreign-key violation or an unresolved reference), not at the `bots_to_contacts` step itself.

**Known limitations:**
- **`old_bot_id` is not domain-scoped.** The old database's `chat.bot` rows are keyed by `(flow_id, dc)`, so the same `flow_id` can in principle repeat across domains. The mapping table is keyed by bare `flow_id` only, so if your old DB has non-unique `flow_id` values across domains, bots in different domains that share a `flow_id` will all resolve to the same mapped contact. This is safe only if your deployment's `flow_id` values are globally unique.
- **`domain_id` in the tracking table is hardcoded to `1`.** `bots_to_contacts` never reads the old database, so it can't look up each bot's actual domain from `chat.bot.dc` — it assumes the single-domain layout (`domain_id = 1` everywhere) that production currently has.
- **`sync_mode_bots_to_contacts` is a no-op.** Bots are linked once, in full mode, from `public.bot_mapping`. If you add mapping rows after the full migration has run, re-run `bots_to_contacts` with `MIGRATION_START_FROM_STEP`/`MIGRATION_SINGLE_STEP` (after cleaning up its previous `chat_migration`/`chat_migration_step` rows) rather than relying on sync mode to pick them up.

## Usage

```sh
# Full migration
MIGRATION_OLD_DB_DSN="postgres://..." \
MIGRATION_NEW_DB_DSN="postgres://..." \
MIGRATION_ENCRYPTION_KEY="<32-character-key>" \
MIGRATION_SESSION_ID="$(uuidgen)" \
./chat-migration-cli

# Incremental sync (safe to run repeatedly, use a new MIGRATION_SESSION_ID each run)
MIGRATION_OLD_DB_DSN="postgres://..." \
MIGRATION_NEW_DB_DSN="postgres://..." \
MIGRATION_ENCRYPTION_KEY="<32-character-key>" \
MIGRATION_SESSION_ID="$(uuidgen)" \
MIGRATION_SYNC_MODE=true \
./chat-migration-cli

# Resume from a specific step (use the same MIGRATION_SESSION_ID as the run being resumed)
MIGRATION_OLD_DB_DSN="postgres://..." \
MIGRATION_NEW_DB_DSN="postgres://..." \
MIGRATION_ENCRYPTION_KEY="<32-character-key>" \
MIGRATION_SESSION_ID="<session-id-from-the-original-run>" \
MIGRATION_START_FROM_STEP=messages \
./chat-migration-cli

# Run exactly one step, then stop (fails if the step is already completed outside sync mode)
MIGRATION_OLD_DB_DSN="postgres://..." \
MIGRATION_NEW_DB_DSN="postgres://..." \
MIGRATION_ENCRYPTION_KEY="<32-character-key>" \
MIGRATION_SESSION_ID="<session-id-from-the-original-run>" \
MIGRATION_START_FROM_STEP=messages \
MIGRATION_SINGLE_STEP=true \
./chat-migration-cli

# With debug logging
MIGRATION_LOG_LEVEL=debug \
MIGRATION_LOG_JSON=true \
...
```

## Prerequisites

- Go 1.25+
- Network access to `https://graph.facebook.com` is required during the `facebook_and_whatsapp` step to resolve WhatsApp Business Account phone numbers from the Meta Graph API.

The tool auto-creates two tracking tables (`chat_migration` and `chat_migration_step`) in the new database on first run — no manual schema preparation is needed.

## Building

```sh
go build -o chat-migration-cli .
```

For a local Debian/Linux (amd64) build with version metadata embedded (application version, build number, git commit, build time, dirty flag), use the provided PowerShell script instead:

```powershell
.\build.ps1
```

It produces `chat-migration-cli` in the project root. Run `chat-migration-cli --version` to print the embedded version information; this does not require any configuration or database connection.
