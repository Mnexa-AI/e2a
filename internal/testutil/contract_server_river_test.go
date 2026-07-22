package testutil

import (
	"context"
	"testing"
)

func TestContractServerRepeatedStartClearsRiverStateAndPreservesMigrations(t *testing.T) {
	ctx := context.Background()
	first, err := StartContractServer(ctx, TestDBURL())
	if err != nil {
		t.Skipf("contract server not available: %v", err)
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

	second, err := StartContractServer(ctx, TestDBURL())
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
