package db_test

import (
	"testing"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/dbtest"
)

func TestNewPool_MigrationsApplied(t *testing.T) {
	sqlDB := dbtest.NewDB(t)

	rows, err := sqlDB.Query("SELECT key FROM nutrients ORDER BY sort_order")
	if err != nil {
		t.Fatalf("query nutrients: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scan: %v", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}

	if len(keys) == 0 {
		t.Fatal("expected seeded nutrients, got none — did migrations run?")
	}
	if keys[0] != "calories" {
		t.Errorf("expected first nutrient (by sort_order) to be calories, got %q", keys[0])
	}
}

func TestMigrateStatus_DoesNotError(t *testing.T) {
	_, dsn := dbtest.NewDBWithDSN(t)

	if err := db.MigrateStatus(t.Context(), dbtest.Driver(), dsn); err != nil {
		t.Fatalf("migrate status: %v", err)
	}
}

func TestMigrateDownUp_RoundTrips(t *testing.T) {
	_, dsn := dbtest.NewDBWithDSN(t)

	if err := db.MigrateDown(t.Context(), dbtest.Driver(), dsn); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := db.Migrate(t.Context(), dbtest.Driver(), dsn); err != nil {
		t.Fatalf("migrate back up: %v", err)
	}
}
