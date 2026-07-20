package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

func init() {
	goose.SetLogger(slogGooseLogger{})
}

const (
	// migrationLockKey is an arbitrary constant passed to pg_advisory_lock to
	// serialize concurrent migration runs — e.g. a rolling restart where the
	// old and new containers briefly overlap, or a one-shot "migrate" service
	// racing the app's own auto-migrate-on-start. goose's legacy Up/Down API
	// (the version this package uses) takes no lock of its own, so without
	// this two processes could both see the same migration as pending and
	// race applying it.
	migrationLockKey = 8617345001

	// migrationLockTimeout bounds how long a caller waits to acquire the
	// lock — generous enough for a real migration run to finish elsewhere,
	// but finite so a stuck prior process can't hang this one forever with
	// no operator-visible error.
	migrationLockTimeout = 2 * time.Minute

	// unlockTimeout bounds the deferred release call itself, so a degraded
	// (not fully disconnected) database can't stall the unlock — and the
	// conn.Close()/sqlDB.Close() calls queued behind it — indefinitely.
	unlockTimeout = 10 * time.Second
)

// Migrate applies all pending goose migrations embedded in MigrationsFS for
// driver ("postgres" or "sqlite").
func Migrate(ctx context.Context, driver, dsn string) error {
	return withGooseDB(ctx, driver, dsn, func(sqlDB *sql.DB, dir string) error {
		return goose.Up(sqlDB, dir)
	})
}

// MigrateDown rolls back the most recently applied migration.
func MigrateDown(ctx context.Context, driver, dsn string) error {
	return withGooseDB(ctx, driver, dsn, func(sqlDB *sql.DB, dir string) error {
		return goose.Down(sqlDB, dir)
	})
}

// MigrateStatus prints the status of all migrations.
func MigrateStatus(ctx context.Context, driver, dsn string) error {
	return withGooseDB(ctx, driver, dsn, func(sqlDB *sql.DB, dir string) error {
		return goose.Status(sqlDB, dir)
	})
}

func withGooseDB(ctx context.Context, driver, dsn string, fn func(sqlDB *sql.DB, dir string) error) error {
	dir, dialect, err := migrationsDir(driver)
	if err != nil {
		return err
	}

	var sqlDB *sql.DB
	switch driver {
	case "postgres":
		sqlDB, err = sql.Open("pgx", dsn)
	case "sqlite":
		sqlDB, err = sql.Open("sqlite", sqliteDSN(dsn))
	}
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	// Postgres-only: serialize concurrent migration runs (e.g. a rolling
	// restart where the old and new containers briefly overlap) via an
	// advisory lock held on a single checked-out connection. SQLite needs
	// no equivalent — it's a single file with no separate server process
	// multiple app instances could race against the same way.
	if driver == "postgres" {
		conn, err := sqlDB.Conn(ctx)
		if err != nil {
			return fmt.Errorf("acquire connection: %w", err)
		}
		defer func() { _ = conn.Close() }()

		lockCtx, cancelLock := context.WithTimeout(ctx, migrationLockTimeout)
		defer cancelLock()
		if _, err := conn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}
		defer func() {
			unlockCtx, cancelUnlock := context.WithTimeout(context.Background(), unlockTimeout)
			defer cancelUnlock()
			if _, err := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationLockKey); err != nil {
				slog.Error("failed to release migration advisory lock", "error", err)
			}
		}()
	}

	goose.SetBaseFS(MigrationsFS)
	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	return fn(sqlDB, dir)
}

// slogGooseLogger routes goose's own migration-progress logging through
// slog so migration logs come out as the same structured JSON as the rest
// of the app, instead of goose's default unstructured stdlib log output.
type slogGooseLogger struct{}

func (slogGooseLogger) Printf(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...))
}

func (slogGooseLogger) Fatalf(format string, v ...any) {
	slog.Error(fmt.Sprintf(format, v...))
	os.Exit(1)
}
