package sendramp_test

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/tokencanopy/e2a/internal/sendramp"
)

func TestSweepPrunesHistoricalRowsOnly(t *testing.T) {
	store, pool, userID, domain, first := seedRampMessage(t, "sweep")
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	oldDay := now.AddDate(0, 0, -40)
	schedule := sendramp.NewSchedule(50, 100, 2)
	terminal := first
	active := createMessageForAgent(t, pool, "agent@"+domain, "sweep-active")
	if !reserve(t, store, userID, domain, terminal, 1, oldDay, schedule).Allowed {
		t.Fatal("terminal reserve denied")
	}
	if err := store.Release(context.Background(), terminal); err != nil {
		t.Fatal(err)
	}
	if !reserve(t, store, userID, domain, active, 1, oldDay, schedule).Allowed {
		t.Fatal("active reserve denied")
	}
	if _, err := pool.Exec(context.Background(), `UPDATE sending_ramp_reservations SET updated_at=$2 WHERE message_id=$1`, terminal, now.AddDate(0, 0, -8)); err != nil {
		t.Fatal(err)
	}
	if err := store.Sweep(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var terminalRows, activeRows, counterRows int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM sending_ramp_reservations WHERE message_id=$1`, terminal).Scan(&terminalRows)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM sending_ramp_reservations WHERE message_id=$1`, active).Scan(&activeRows)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM domain_send_counters WHERE user_id=$1 AND domain='example.com' AND day=$2`, userID, oldDay).Scan(&counterRows)
	if terminalRows != 0 || activeRows != 1 || counterRows != 1 {
		t.Fatalf("terminal=%d active=%d counter=%d", terminalRows, activeRows, counterRows)
	}
}

func TestMaintenanceJobsRegistersDailyMaintenancePeriodic(t *testing.T) {
	workers := river.NewWorkers()
	periodic := sendramp.NewMaintenanceJobs(nil).RegisterJobs(workers)
	if len(periodic) != 1 {
		t.Fatalf("periodic jobs=%d, want 1", len(periodic))
	}
}

func TestMaintenanceWorkerRunsSweep(t *testing.T) {
	store, pool, userID, domain, messageID := seedRampMessage(t, "maintenance-worker")
	oldDay := time.Now().UTC().AddDate(0, 0, -40)
	if !reserve(t, store, userID, domain, messageID, 1, oldDay, sendramp.DefaultSchedule).Allowed {
		t.Fatal("reserve denied")
	}
	if err := store.Release(context.Background(), messageID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE sending_ramp_reservations SET updated_at=now()-interval '8 days' WHERE message_id=$1`, messageID); err != nil {
		t.Fatal(err)
	}
	if err := sendramp.NewMaintenanceWorker(store).Work(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM sending_ramp_reservations WHERE message_id=$1`, messageID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("terminal reservation rows=%d, want 0", rows)
	}
}
