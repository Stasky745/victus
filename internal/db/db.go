// Package db wires up the connection pool and migrations for whichever
// backend Victus is configured to use (Postgres or SQLite).
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jackc/pgx/v5/tracelog"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// MigrationsFS holds the embedded goose SQL migrations applied by Migrate,
// one subdirectory per dialect (migrations/postgres, migrations/sqlite).
//
//go:embed migrations
var MigrationsFS embed.FS

const (
	maxConns        = 10
	minConns        = 2
	maxConnLifetime = time.Hour
	maxConnIdleTime = 30 * time.Minute
	connectTimeout  = 5 * time.Second

	// sqliteBusyTimeoutMS bounds how long a connection waits on SQLite's
	// own lock before returning SQLITE_BUSY, so a brief writer/writer or
	// writer/reader collision (WAL mode still serializes writers) resolves
	// itself instead of surfacing as a user-facing error.
	sqliteBusyTimeoutMS = 5000
)

// migrationsDir returns the goose migration directory (relative to
// MigrationsFS) and dialect string for driver ("postgres" or "sqlite").
func migrationsDir(driver string) (dir, dialect string, err error) {
	switch driver {
	case "postgres":
		return "migrations/postgres", "postgres", nil
	case "sqlite":
		return "migrations/sqlite", "sqlite3", nil
	default:
		return "", "", fmt.Errorf("unknown DB_DRIVER %q", driver)
	}
}

// Open opens a tuned *sql.DB for driver ("postgres" or "sqlite") against
// dsn — a Postgres connection string, or a SQLite file path — wires
// pgx's internal logging through slog for Postgres, and verifies
// connectivity before returning.
func Open(ctx context.Context, driver, dsn string) (*sql.DB, error) {
	var sqlDB *sql.DB
	switch driver {
	case "postgres":
		config, err := pgx.ParseConfig(dsn)
		if err != nil {
			return nil, fmt.Errorf("parse database url: %w", err)
		}
		config.ConnectTimeout = connectTimeout
		config.Tracer = &tracelog.TraceLog{
			Logger:   slogTracer{},
			LogLevel: tracelog.LogLevelWarn, // queries/errors worth investigating; bump to LogLevelDebug locally to see every query
		}
		sqlDB = stdlib.OpenDB(*config)
		sqlDB.SetMaxOpenConns(maxConns)
		sqlDB.SetMaxIdleConns(minConns)
	case "sqlite":
		var err error
		sqlDB, err = sql.Open("sqlite", sqliteDSN(dsn))
		if err != nil {
			return nil, fmt.Errorf("open db: %w", err)
		}
		// WAL mode allows concurrent readers alongside the one writer SQLite
		// ever permits, so the pool doesn't need to be pinned to a single
		// connection the way a plain rollback-journal database would.
		sqlDB.SetMaxOpenConns(maxConns)
		sqlDB.SetMaxIdleConns(minConns)
	default:
		return nil, fmt.Errorf("unknown DB_DRIVER %q", driver)
	}

	sqlDB.SetConnMaxLifetime(maxConnLifetime)
	sqlDB.SetConnMaxIdleTime(maxConnIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	slog.Info("database pool ready", "driver", driver, "max_conns", maxConns, "min_conns", minConns)
	return sqlDB, nil
}

// sqliteDSN turns a plain SQLite file path into a modernc.org/sqlite DSN
// with the pragmas Victus needs applied to every connection the pool opens
// (a bare "PRAGMA ..." Exec after Open would only affect whichever single
// connection happened to run it, not the rest of the pool): foreign key
// enforcement (SQLite defaults this off — Victus's schema relies on
// ON DELETE CASCADE), WAL journaling (concurrent readers alongside the one
// writer), and a busy timeout (so a lock collision retries instead of
// immediately erroring).
func sqliteDSN(path string) string {
	return fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)", path, sqliteBusyTimeoutMS)
}

// slogTracer adapts pgx's tracelog.Logger interface to log/slog.
type slogTracer struct{}

func (slogTracer) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	args := make([]any, 0, len(data)*2)
	for k, v := range data {
		args = append(args, k, v)
	}
	switch level {
	case tracelog.LogLevelError:
		slog.ErrorContext(ctx, msg, args...)
	case tracelog.LogLevelWarn:
		slog.WarnContext(ctx, msg, args...)
	case tracelog.LogLevelDebug, tracelog.LogLevelTrace:
		slog.DebugContext(ctx, msg, args...)
	default:
		slog.InfoContext(ctx, msg, args...)
	}
}
