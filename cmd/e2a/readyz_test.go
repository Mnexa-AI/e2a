package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/testutil"
)

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
	pool := testutil.TestDB(t) // applies all embedded migrations
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
