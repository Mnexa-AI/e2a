package testutil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestContractServerRepeatedStartClearsRiverStateAndPreservesMigrations(t *testing.T) {
	dbURL := requireReachableContractTestDB(t)
	ctx := context.Background()
	first, err := StartContractServer(ctx, dbURL)
	if err != nil {
		t.Fatalf("start first contract server after successful DB preflight: %v", err)
	}
	t.Cleanup(func() { _ = first.Close(context.Background()) })

	var migrationCount int
	if err := first.DBPool.QueryRow(ctx, `SELECT count(*) FROM river_migration`).Scan(&migrationCount); err != nil {
		t.Fatalf("count River migrations: %v", err)
	}
	if migrationCount == 0 {
		t.Fatal("River migration ledger is empty")
	}
	if _, err := first.DBPool.Exec(ctx, `INSERT INTO river_job (args, kind) VALUES ('{}', 'contract_stale_job')`); err != nil {
		t.Fatalf("insert stale River job: %v", err)
	}
	_ = first.Close(ctx)

	second, err := StartContractServer(ctx, dbURL)
	if err != nil {
		t.Fatalf("restart contract server: %v", err)
	}
	t.Cleanup(func() { _ = second.Close(context.Background()) })

	var jobCount, migrationCountAfter int
	if err := second.DBPool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&jobCount); err != nil {
		t.Fatalf("count River jobs after restart: %v", err)
	}
	if err := second.DBPool.QueryRow(ctx, `SELECT count(*) FROM river_migration`).Scan(&migrationCountAfter); err != nil {
		t.Fatalf("count River migrations after restart: %v", err)
	}
	if jobCount != 0 {
		t.Fatalf("River jobs after restart = %d, want 0", jobCount)
	}
	if migrationCountAfter != migrationCount {
		t.Fatalf("River migration ledger count after restart = %d, want %d", migrationCountAfter, migrationCount)
	}
}

// requireReachableContractTestDB is the test's only skip gate. Once this
// check succeeds, migration, River reset, and server startup errors are
// regressions and the caller must fail rather than classifying them as DB
// unavailability. It must go through OpenPreparedTestDB — a raw ping of the
// derived per-package URL fails with 3D000 on every cold database (CI's
// service container is always cold), which would silently skip this
// regression gate forever instead of self-provisioning like every other
// harness consumer.
func requireReachableContractTestDB(t *testing.T) string {
	t.Helper()
	dbURL := TestDBURL()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := OpenPreparedTestDB(ctx, dbURL)
	if err != nil {
		var prepErr *testDBPreparationError
		if errors.As(err, &prepErr) {
			t.Fatalf("prepare contract test database: %v", err)
		}
		t.Skipf("private test database unavailable: %v", err)
	}
	pool.Close()
	return dbURL
}
