package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/viper"

	"github.com/webitel/chat-migration-cli-custom/internal/buildinfo"
	"github.com/webitel/chat-migration-cli-custom/internal/service"
	"github.com/webitel/chat-migration-cli-custom/internal/store/newdb"
	"github.com/webitel/chat-migration-cli-custom/internal/store/olddb"
)

// config holds all runtime configuration loaded from environment variables.
// All variables are prefixed with MIGRATION_ (e.g. MIGRATION_OLD_DB_DSN).
type config struct {
	OldDBDSN             string     // required: postgres DSN for the legacy chat DB
	NewDBDSN             string     // required: postgres DSN for the new microservices DB
	OldDBConns           int32      // OLD_DB_MAX_CONNS (default 5)
	NewDBConns           int32      // NEW_DB_MAX_CONNS (default 10)
	StartFrom            string     // START_FROM_STEP: skip steps before this one (optional)
	SingleStep           bool       // SINGLE_STEP: run only the step named by StartFrom, then stop (optional)
	LogLevel             slog.Level // LOG_LEVEL: debug|info|warn|error (default info)
	LogJSON              bool       // LOG_JSON: emit JSON instead of text (default false)
	EncryptionKey        string     // required: 32-byte AES-256 key for encrypting tokens
	Sync                 bool       // SYNC_MODE
	MigratePortalClients bool       // MIGRATE_PORTAL_CLIENTS
	PortalChatIssuerID   string     // PORTAL_CHAT_ISSUER_ID: required if MIGRATE_PORTAL_CLIENTS is enabled
	PortalClientType     string     // PORTAL_CLIENT_TYPE: contact type of migrated portal clients, required if MIGRATE_PORTAL_CLIENTS is enabled
	SessionID            uuid.UUID  // required: SESSION_ID, identifies the records created by this migration cycle
}

func main() {
	initMode := flag.Bool("init", false, "create the tables required to run a migration in the new DB, then exit")
	versionMode := flag.Bool("version", false, "print version information and exit")

	flag.Parse()

	if *versionMode {
		printVersion()

		return
	}

	if *initMode {
		if err := runInitMode(); err != nil {
			os.Exit(1)
		}

		return
	}

	cfg := mustLoadConfig()

	log := buildLogger(cfg.LogLevel, cfg.LogJSON)
	slog.SetDefault(log)

	log.Info("starting chat migration",
		"version", buildinfo.Version,
		"build", buildinfo.BuildNumber,
		"commit", buildinfo.GitCommit,
	)

	if err := run(cfg, log); err != nil {
		os.Exit(1)
	}
}

func run(cfg config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("connecting to databases")

	oldPool, err := buildPool(ctx, cfg.OldDBDSN, cfg.OldDBConns)
	if err != nil {
		log.Error("old DB connection failed", "error", err)

		return err
	}
	defer oldPool.Close()

	newPool, err := buildPool(ctx, cfg.NewDBDSN, cfg.NewDBConns)
	if err != nil {
		log.Error("new DB connection failed", "error", err)

		return err
	}
	defer newPool.Close()

	srcDB, err := olddb.New(oldPool, cfg.MigratePortalClients)
	if err != nil {
		log.Error("source DB init failed", "error", err)

		return err
	}

	dstDB := newdb.New(newPool)

	if err := dstDB.CheckTablesExist(ctx); err != nil {
		log.Error("required tables are missing", "error", err)

		return err
	}

	encryptor, err := service.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		log.Error("failed to create encryptor", "error", err)

		return err
	}

	log.Info("migration session started", "session_id", cfg.SessionID)

	converter := service.NewConverter(srcDB, dstDB, encryptor, cfg.Sync, cfg.MigratePortalClients, cfg.PortalChatIssuerID, cfg.PortalClientType, cfg.SessionID)

	var runErr error

	switch {
	case cfg.SingleStep:
		log.Info("running single migration step", "step", cfg.StartFrom)
		runErr = converter.MigrateSingleStep(ctx, cfg.StartFrom)
	case cfg.StartFrom != "":
		log.Info("starting migration from step", "step", cfg.StartFrom)
		runErr = converter.MigrateFromStep(ctx, cfg.StartFrom)
	default:
		log.Info("starting full migration")

		runErr = converter.Migrate(ctx)
	}

	if runErr != nil {
		log.Error("migration failed", "error", runErr)

		return runErr
	}

	log.Info("migration completed")

	return nil
}

// printVersion prints version and local build information and exits without
// touching configuration or any database.
func printVersion() {
	fmt.Printf("chat-migration-cli-custom %s\n", buildinfo.Full())
	fmt.Printf("commit: %s\n", buildinfo.GitCommit)
	fmt.Printf("built: %s\n", buildinfo.BuildTime)
}

// runInitMode creates the tables required to run a migration in the new DB.
// Only MIGRATION_NEW_DB_DSN is required; every other MIGRATION_* variable is
// ignored.
func runInitMode() error {
	v := viper.New()
	v.SetEnvPrefix("MIGRATION")
	v.AutomaticEnv()

	v.SetDefault("NEW_DB_MAX_CONNS", 10)
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("LOG_JSON", false)

	log := buildLogger(parseLogLevel(v.GetString("LOG_LEVEL")), v.GetBool("LOG_JSON"))
	slog.SetDefault(log)

	newDSN := v.GetString("NEW_DB_DSN")
	if newDSN == "" {
		log.Error("MIGRATION_NEW_DB_DSN is required")

		return errors.New("MIGRATION_NEW_DB_DSN is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	newPool, err := buildPool(ctx, newDSN, int32(v.GetInt("NEW_DB_MAX_CONNS")))
	if err != nil {
		log.Error("new DB connection failed", "error", err)

		return err
	}
	defer newPool.Close()

	dstDB := newdb.New(newPool)
	if err := dstDB.InitTables(ctx); err != nil {
		log.Error("failed to initialize tables", "error", err)

		return err
	}

	log.Info("required tables initialized")

	return nil
}

func parseLogLevel(value string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return slog.LevelInfo
	}

	return level
}

func mustLoadConfig() config {
	v := viper.New()
	v.SetEnvPrefix("MIGRATION")
	v.AutomaticEnv()

	v.SetDefault("OLD_DB_MAX_CONNS", 5)
	v.SetDefault("NEW_DB_MAX_CONNS", 10)
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("LOG_JSON", false)
	v.SetDefault("START_FROM_STEP", "")
	v.SetDefault("SINGLE_STEP", false)
	v.SetDefault("SYNC_MODE", false)
	v.SetDefault("MIGRATE_PORTAL_CLIENTS", false)
	v.SetDefault("PORTAL_CHAT_ISSUER_ID", "")
	v.SetDefault("PORTAL_CLIENT_TYPE", "")

	oldDSN := v.GetString("OLD_DB_DSN")
	newDSN := v.GetString("NEW_DB_DSN")
	encryptionKey := v.GetString("ENCRYPTION_KEY")

	if oldDSN == "" {
		slog.Error("MIGRATION_OLD_DB_DSN is required")
		os.Exit(1)
	}

	if newDSN == "" {
		slog.Error("MIGRATION_NEW_DB_DSN is required")
		os.Exit(1)
	}

	if encryptionKey == "" {
		slog.Error("MIGRATION_ENCRYPTION_KEY is required")
		os.Exit(1)
	}

	startFrom := v.GetString("START_FROM_STEP")

	singleStep := v.GetBool("SINGLE_STEP")
	if singleStep && startFrom == "" {
		slog.Error("MIGRATION_START_FROM_STEP is required when MIGRATION_SINGLE_STEP is enabled")
		os.Exit(1)
	}

	level := parseLogLevel(v.GetString("LOG_LEVEL"))

	migratePortalClients := v.GetBool("MIGRATE_PORTAL_CLIENTS")

	portalChatIssuerID := v.GetString("PORTAL_CHAT_ISSUER_ID")
	if migratePortalClients && portalChatIssuerID == "" {
		slog.Error("MIGRATION_PORTAL_CHAT_ISSUER_ID is required when MIGRATION_MIGRATE_PORTAL_CLIENTS is enabled")
		os.Exit(1)
	}

	portalClientType := v.GetString("PORTAL_CLIENT_TYPE")
	if migratePortalClients && portalClientType == "" {
		slog.Error("MIGRATION_PORTAL_CLIENT_TYPE is required when MIGRATION_MIGRATE_PORTAL_CLIENTS is enabled")
		os.Exit(1)
	}

	sessionIDRaw := v.GetString("SESSION_ID")
	if sessionIDRaw == "" {
		slog.Error("MIGRATION_SESSION_ID is required")
		os.Exit(1)
	}

	sessionID, err := uuid.FromString(sessionIDRaw)
	if err != nil {
		slog.Error("MIGRATION_SESSION_ID must be a valid UUID", "error", err)
		os.Exit(1)
	}

	return config{
		OldDBDSN:             oldDSN,
		NewDBDSN:             newDSN,
		OldDBConns:           int32(v.GetInt("OLD_DB_MAX_CONNS")),
		NewDBConns:           int32(v.GetInt("NEW_DB_MAX_CONNS")),
		StartFrom:            startFrom,
		SingleStep:           singleStep,
		LogLevel:             level,
		LogJSON:              v.GetBool("LOG_JSON"),
		EncryptionKey:        encryptionKey,
		Sync:                 v.GetBool("SYNC_MODE"),
		MigratePortalClients: migratePortalClients,
		PortalChatIssuerID:   portalChatIssuerID,
		PortalClientType:     portalClientType,
		SessionID:            sessionID,
	}
}

func buildLogger(level slog.Level, json bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	if json {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}

	return slog.New(h)
}

func buildPool(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}

	return pgxpool.NewWithConfig(ctx, cfg)
}
