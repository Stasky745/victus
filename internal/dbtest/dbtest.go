// Package dbtest provisions an ephemeral, migrated database for integration
// tests, backed by whichever driver TEST_DB_DRIVER selects ("postgres", the
// default, or "sqlite") — so the same test files can be run against either
// backend to prove they behave identically. The Postgres path requires a
// working Docker (or compatible) daemon; tests using it should call
// testing.Short() to skip in constrained CI runs if needed.
package dbtest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Stasky745/victus/internal/db"
)

// Driver returns TEST_DB_DRIVER ("postgres" if unset) — the backend NewDB
// and NewDBWithDSN provision against.
func Driver() string {
	driver := os.Getenv("TEST_DB_DRIVER")
	if driver == "" {
		driver = "postgres"
	}
	return driver
}

// NewDB starts (or opens) a throwaway, migrated database for TEST_DB_DRIVER
// and returns a ready-to-use *sql.DB. Torn down automatically via t.Cleanup.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, _ := NewDBWithDSN(t)
	return sqlDB
}

// NewDBWithDSN is NewDB, additionally returning the DSN/path used to reach
// it — for the rare test (migration status/rollback) that needs to open its
// own separate connection against the same database.
func NewDBWithDSN(t *testing.T) (*sql.DB, string) {
	t.Helper()

	switch Driver() {
	case "sqlite":
		return newSQLiteDB(t)
	case "postgres":
		return newPostgresDB(t)
	default:
		t.Fatalf("unknown TEST_DB_DRIVER %q (expected: postgres, sqlite)", Driver())
		return nil, ""
	}
}

func newPostgresDB(t *testing.T) (*sql.DB, string) {
	t.Helper()

	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("victus_test"),
		postgres.WithUsername("victus"),
		postgres.WithPassword("victus"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	if err := db.Migrate(ctx, "postgres", dsn); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	sqlDB, err := db.Open(ctx, "postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	return sqlDB, dsn
}

func newSQLiteDB(t *testing.T) (*sql.DB, string) {
	t.Helper()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "victus_test.db")

	if err := db.Migrate(ctx, "sqlite", path); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	sqlDB, err := db.Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	return sqlDB, path
}
