package db

import (
	"database/sql"
	"fmt"

	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/db/sqliteadapter"
)

// NewQuerier returns the sqlc.Querier for driver ("postgres" or "sqlite")
// backed by sqlDB — business logic depends only on this interface, never on
// the concrete sqlc/sqlite package, so it doesn't need to know which
// backend it's talking to.
func NewQuerier(driver string, sqlDB *sql.DB) (sqlc.Querier, error) {
	switch driver {
	case "postgres":
		return sqlc.New(sqlDB), nil
	case "sqlite":
		return sqliteadapter.New(sqlDB), nil
	default:
		return nil, fmt.Errorf("unknown DB_DRIVER %q", driver)
	}
}

// NewTxQuerier is NewQuerier for a transaction in progress.
func NewTxQuerier(driver string, tx *sql.Tx) (sqlc.Querier, error) {
	switch driver {
	case "postgres":
		return sqlc.New(tx), nil
	case "sqlite":
		return sqliteadapter.New(tx), nil
	default:
		return nil, fmt.Errorf("unknown DB_DRIVER %q", driver)
	}
}
