// Package postgrestest gives each test its own empty PostgreSQL database.
//
// Tests use the server in FORGE_TEST_POSTGRES_URL, or FORGE_TEST_DATABASE_URL
// when it is a postgres:// URL. The user needs permission to create
// databases. Without a server tests are skipped, unless
// FORGE_TEST_REQUIRE_DATABASE=true turns the skip into a failure (see dbtest).
package postgrestest

import (
	"context"
	"io/fs"
	"log/slog"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/fabriciobonjorno/forge-go/config"
	"github.com/fabriciobonjorno/forge-go/dbtest"
	"github.com/fabriciobonjorno/forge-go/migrate"
	"github.com/fabriciobonjorno/forge-go/postgres"
)

// New creates a fresh database, returns a pool connected to it and drops the
// database when the test ends. Parallel tests are fully isolated.
func New(t testing.TB) *postgres.DB {
	t.Helper()
	db, _ := NewWithConfig(t)
	return db
}

// NewMigrated is New followed by applying every migration in migrations.
func NewMigrated(t testing.TB, migrations fs.FS) *postgres.DB {
	t.Helper()
	db, cfg := NewWithConfig(t)
	dbtest.Migrate(t, migrations, func(loaded []migrate.Migration, logger *slog.Logger) (*migrate.Migrator, error) {
		return postgres.NewMigrator(cfg, loaded, logger)
	})
	return db
}

// Config returns configuration for a fresh, empty database, for tests of
// code that opens its own pool (for example Configure).
func Config(t testing.TB) config.Database {
	t.Helper()
	db, cfg := NewWithConfig(t)
	db.Close()
	return cfg
}

// NewWithConfig is New that also returns the database's configuration.
func NewWithConfig(t testing.TB) (*postgres.DB, config.Database) {
	t.Helper()
	base := dbtest.ServerURL(t, config.AdapterPostgres)
	serverURL, err := url.Parse(base)
	if err != nil {
		t.Fatalf("%s is not a valid URL", dbtest.EnvAdapterURL(config.AdapterPostgres)) // not wrapped: may contain a password
	}

	name := dbtest.DatabaseName()
	ctx, cancel := context.WithTimeout(context.Background(), dbtest.SetupTimeout)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect to the PostgreSQL test server: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+identifier); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	// Registered before anything else can fail, so the database is always
	// dropped. Cleanups run last-in first-out: the pool closes first.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), dbtest.SetupTimeout)
		defer cancel()
		admin, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Errorf("drop test database: %v", err)
			return
		}
		defer func() { _ = admin.Close(ctx) }()
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+identifier+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database: %v", err)
		}
	})

	testURL := *serverURL
	testURL.Path = "/" + name
	cfg := config.Default().Database
	cfg.URL = config.NewSecret(testURL.String())
	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(db.Close)
	return db, cfg
}
