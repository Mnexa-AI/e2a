package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

// requireReachableContractTestDB is the test's only skip gate. Once this ping
// succeeds, migration, River reset, and server startup errors are regressions
// and the caller must fail rather than classifying them as DB unavailability.
func requireReachableContractTestDB(t *testing.T) string {
	t.Helper()
	dbURL := TestDBURL()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parse E2A_TEST_DATABASE_URL: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open E2A_TEST_DATABASE_URL pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("private test database unavailable: %v", err)
	}
	return dbURL
}
