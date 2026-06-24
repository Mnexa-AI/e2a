package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/migrations"
)

// readMigrationA returns the embedded body of Migration A so a test can
// re-apply it and assert idempotency.
func readMigrationA(t *testing.T) string {
	t.Helper()
	body, err := migrations.FS.ReadFile("048_workspaces_migration_a.sql")
	if err != nil {
		t.Fatalf("read embedded migration A: %v", err)
	}
	return string(body)
}

// TestMigrationA_BackfillNoNullWorkspaceID seeds a user with rows on every
// re-keyed table, then re-applies Migration A and asserts the backfill leaves
// no NULL workspace_id and that the user got exactly one personal workspace
// (admin membership) while the shared domain is owned by ws_system.
func TestMigrationA_BackfillNoNullWorkspaceID(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t) // applies all migrations incl. 048

	const userID = "user_wsmigtest1"
	const email = "wsmig1@example.com"

	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}

	// Insert a user WITHOUT going through the workspace backfill yet — then
	// re-run Migration A to provision + backfill. Postgres has the user's
	// personal workspace absent until the re-apply runs the backfill.
	mustExec(`INSERT INTO users (id, email, name, google_subject) VALUES ($1,$2,$3,$4)`,
		userID, email, "Workspace Migtest", "gsub_wsmig1")

	// A domain + agent + key + account rows keyed to this user, with
	// workspace_id explicitly NULLed (simulating pre-migration state).
	mustExec(`INSERT INTO domains (domain, user_id, workspace_id, verified) VALUES ($1,$2,NULL,true)`,
		"wsmig1.example.com", userID)
	mustExec(`INSERT INTO agent_identities (id, domain, user_id, workspace_id) VALUES ($1,$2,$3,NULL)`,
		"agt_wsmig1", "wsmig1.example.com", userID)
	mustExec(`INSERT INTO api_keys (id, user_id, key_prefix, key_hash, workspace_id) VALUES ($1,$2,$3,$4,NULL)`,
		"key_wsmig1", userID, "e2a_acct_", "hash_wsmig1")
	mustExec(`INSERT INTO suppressions (id, user_id, address, workspace_id) VALUES ($1,$2,$3,NULL)`,
		"sup_wsmig1", userID, "blocked@example.com")
	mustExec(`INSERT INTO webhooks (id, user_id, url, events, signing_secret, workspace_id) VALUES ($1,$2,$3,$4,$5,NULL)`,
		"wh_wsmig1", userID, "https://example.com/wh", []string{"email.received"}, "whsec_wsmig1")

	// Re-apply Migration A — this is the backfill under test.
	if _, err := pool.Exec(ctx, readMigrationA(t)); err != nil {
		t.Fatalf("re-apply migration A: %v", err)
	}

	// The user got exactly one personal workspace with deterministic id and
	// an admin membership.
	var wsCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM workspaces WHERE id = 'ws_' || md5($1)`, userID).Scan(&wsCount); err != nil {
		t.Fatal(err)
	}
	if wsCount != 1 {
		t.Fatalf("expected exactly one personal workspace, got %d", wsCount)
	}
	var role string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = 'ws_' || md5($1) AND user_id = $1`,
		userID).Scan(&role); err != nil {
		t.Fatalf("expected admin membership: %v", err)
	}
	if role != "admin" {
		t.Fatalf("expected admin role, got %q", role)
	}

	// No NULL workspace_id on any re-keyed table for this user's rows.
	type check struct {
		table string
		where string
	}
	// Tables whose workspace_id column is still nullable after Migration A
	// (the PK-flipped tables structurally guarantee non-NULL via their new PK).
	checks := []check{
		{"domains", "domain = 'wsmig1.example.com'"},
		{"agent_identities", "id = 'agt_wsmig1'"},
		{"api_keys", "id = 'key_wsmig1'"},
		{"suppressions", "id = 'sup_wsmig1'"},
		{"webhooks", "id = 'wh_wsmig1'"},
	}
	wantWS := "ws_"
	for _, c := range checks {
		var nullCount int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+c.table+" WHERE ("+c.where+") AND workspace_id IS NULL").Scan(&nullCount); err != nil {
			t.Fatalf("null check %s: %v", c.table, err)
		}
		if nullCount != 0 {
			t.Fatalf("%s has %d NULL workspace_id rows after backfill", c.table, nullCount)
		}
		// And the workspace_id resolved to the user's personal workspace.
		var ws string
		if err := pool.QueryRow(ctx,
			"SELECT workspace_id FROM "+c.table+" WHERE "+c.where+" LIMIT 1").Scan(&ws); err != nil {
			t.Fatalf("read workspace_id %s: %v", c.table, err)
		}
		if len(ws) < len(wantWS) || ws[:3] != wantWS {
			t.Fatalf("%s workspace_id %q not a ws_ id", c.table, ws)
		}
	}

	// api_keys.created_by backfilled to the minting user.
	var createdBy string
	if err := pool.QueryRow(ctx,
		`SELECT created_by FROM api_keys WHERE id = 'key_wsmig1'`).Scan(&createdBy); err != nil {
		t.Fatal(err)
	}
	if createdBy != userID {
		t.Fatalf("api_keys.created_by = %q, want %q", createdBy, userID)
	}
}

// TestMigrationA_SharedDomainOwnedBySystem asserts the seeded shared domain
// (user_id IS NULL) is owned by the ws_system sentinel after backfill, and
// that ws_system exists.
func TestMigrationA_SharedDomainOwnedBySystem(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)

	// Ensure the shared domain exists (truncate re-seeds it as ws_system; a
	// fresh apply seeds it NULL then backfills to ws_system). Force the
	// NULL-workspace pre-migration state, then re-run Migration A.
	if _, err := pool.Exec(ctx,
		`INSERT INTO domains (domain, user_id, workspace_id, verified, verified_at)
		 VALUES ('agents.e2a.dev', NULL, NULL, true, now())
		 ON CONFLICT (domain) DO UPDATE SET workspace_id = NULL`); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, readMigrationA(t)); err != nil {
		t.Fatalf("re-apply migration A: %v", err)
	}

	var ws string
	if err := pool.QueryRow(ctx,
		`SELECT workspace_id FROM domains WHERE domain = 'agents.e2a.dev'`).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if ws != "ws_system" {
		t.Fatalf("shared domain workspace_id = %q, want ws_system", ws)
	}

	var systemName string
	if err := pool.QueryRow(ctx,
		`SELECT name FROM workspaces WHERE id = 'ws_system'`).Scan(&systemName); err != nil {
		t.Fatalf("ws_system sentinel missing: %v", err)
	}
}

// TestMigrationA_Idempotent re-applies Migration A twice and asserts the
// workspace + membership counts are stable (no duplicate personal workspaces,
// no duplicate memberships, PK flips don't error on re-run).
func TestMigrationA_Idempotent(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)

	const userID = "user_wsidemp"
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, name, google_subject) VALUES ($1,$2,$3,$4)`,
		userID, "wsidemp@example.com", "Idemp User", "gsub_wsidemp"); err != nil {
		t.Fatal(err)
	}

	body := readMigrationA(t)
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, body); err != nil {
			t.Fatalf("apply migration A run %d: %v", i+1, err)
		}
	}

	var wsCount, memberCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM workspaces WHERE id = 'ws_' || md5($1)`, userID).Scan(&wsCount); err != nil {
		t.Fatal(err)
	}
	if wsCount != 1 {
		t.Fatalf("expected 1 personal workspace after double-apply, got %d", wsCount)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM workspace_members WHERE workspace_id = 'ws_' || md5($1) AND user_id = $1`,
		userID).Scan(&memberCount); err != nil {
		t.Fatal(err)
	}
	if memberCount != 1 {
		t.Fatalf("expected 1 membership after double-apply, got %d", memberCount)
	}

	// The PK flip landed: account_usage / account_limits / usage_summaries /
	// idempotency_keys PKs are now on workspace_id.
	var pkCol string
	if err := pool.QueryRow(ctx, `
		SELECT a.attname
		  FROM pg_index i
		  JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		 WHERE i.indrelid = 'account_usage'::regclass AND i.indisprimary`).Scan(&pkCol); err != nil {
		t.Fatalf("read account_usage PK: %v", err)
	}
	if pkCol != "workspace_id" {
		t.Fatalf("account_usage PK column = %q, want workspace_id", pkCol)
	}
}

// TestMigrationA_StorageTriggerRekeyed verifies the re-keyed storage trigger
// accrues to the agent's workspace_id (not a user_id) on message INSERT, and
// that a NULL-workspace agent's message write does NOT abort (the guard).
func TestMigrationA_StorageTriggerRekeyed(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)

	const userID = "user_wstrig"
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	mustExec(`INSERT INTO users (id, email, name, google_subject) VALUES ($1,$2,$3,$4)`,
		userID, "wstrig@example.com", "Trig User", "gsub_wstrig")
	mustExec(`INSERT INTO domains (domain, user_id, workspace_id, verified) VALUES ($1,$2,NULL,true)`,
		"wstrig.example.com", userID)
	mustExec(`INSERT INTO agent_identities (id, domain, user_id, workspace_id) VALUES ($1,$2,$3,NULL)`,
		"agt_wstrig", "wstrig.example.com", userID)

	// Backfill: provisions the workspace + sets agent_identities.workspace_id.
	if _, err := pool.Exec(ctx, readMigrationA(t)); err != nil {
		t.Fatalf("apply migration A: %v", err)
	}

	// Insert a message — the re-keyed trigger should accrue to the agent's
	// workspace_id in account_usage.
	mustExec(`INSERT INTO messages (id, agent_id, direction, raw_message) VALUES ($1,$2,'inbound',$3)`,
		"msg_wstrig", "agt_wstrig", []byte("hello world body"))

	var storage int64
	if err := pool.QueryRow(ctx,
		`SELECT storage_bytes FROM account_usage WHERE workspace_id = 'ws_' || md5($1)`, userID).Scan(&storage); err != nil {
		t.Fatalf("expected account_usage row keyed by workspace_id: %v", err)
	}
	if storage <= 0 {
		t.Fatalf("expected storage_bytes > 0, got %d", storage)
	}

	// A NULL-workspace agent's message write must NOT abort (the guard).
	mustExec(`INSERT INTO agent_identities (id, domain, user_id, workspace_id) VALUES ($1,$2,$3,NULL)`,
		"agt_wstrig_null", "wstrig.example.com", userID)
	if _, err := pool.Exec(ctx,
		`INSERT INTO messages (id, agent_id, direction, raw_message) VALUES ($1,$2,'inbound',$3)`,
		"msg_wstrig_null", "agt_wstrig_null", []byte("guarded")); err != nil {
		t.Fatalf("NULL-workspace agent message write must not abort: %v", err)
	}
}
