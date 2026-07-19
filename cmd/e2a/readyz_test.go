package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/migrations"
)

// migratedTestDB returns a test pool prepared through the production migration
// runner. The explicit second call verifies the startup path is idempotent before
// /readyz and /selftest query the schema_migrations tracker.
func migratedTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testutil.TestDB(t)
	if err := identity.RunMigrations(context.Background(), pool, migrations.FS, identity.ModeAuto); err != nil {
		t.Fatalf("record migrations: %v", err)
	}
	return pool
}

func TestLatestMigration(t *testing.T) {
	got := latestMigration()
	if got == "" {
		t.Fatal("latestMigration() = empty, want a migration filename")
	}
	if !strings.HasSuffix(got, ".sql") {
		t.Errorf("latestMigration() = %q, want a .sql filename", got)
	}
	// 037 is the floor at the time of writing; later migrations sort after it.
	if got < "037_account_class.sql" {
		t.Errorf("latestMigration() = %q, want >= 037_account_class.sql", got)
	}
}

func TestReadyzHandler_Ready(t *testing.T) {
	pool := migratedTestDB(t)
	rec := httptest.NewRecorder()
	readyzHandler(pool)(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out struct{ Status string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != "ready" {
		t.Errorf("status = %q, want ready", out.Status)
	}
}

func TestReadyzHandler_DBUnreachable(t *testing.T) {
	// A closed pool makes Ping fail → /readyz reports 503 not_ready. Use a
	// dedicated pool (not the shared test pool) so closing it is safe.
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testutil.TestDBURL())
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	pool.Close()

	rec := httptest.NewRecorder()
	readyzHandler(pool)(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var out struct{ Status, Reason string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Status != "not_ready" {
		t.Errorf("status = %q, want not_ready (reason=%q)", out.Status, out.Reason)
	}
}
