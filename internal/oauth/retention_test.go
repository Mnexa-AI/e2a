package oauth_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mustExec wraps a pool.Exec call and fails the test on error. Used
// to keep the table-driven retention seeds readable.
func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// countRows is a 1-line int scalar shortcut for assertion-of-presence
// in the test below.
func countRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("countRows %q: %v", sql, err)
	}
	return n
}

// TestCleanupExpired: every row whose lifetime is up gets removed,
// every still-live row is left alone. We seed both sides of each
// boundary so a wrong WHERE clause shows up as a count mismatch.
func TestCleanupExpired(t *testing.T) {
	st, _, pool, userID, clientID := setup(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	past := now.Add(-2 * time.Hour)
	longPast := now.Add(-90 * 24 * time.Hour)

	// Auth codes — only expires_at gates deletion.
	mustExec(t, pool, `
		INSERT INTO oauth_auth_codes (signature, request_id, client_id, user_id,
		    request, requested_at, expires_at, active)
		VALUES
		    ('ac-expired', 'r1', $1, $2, '{}'::jsonb, $3, $4, false),
		    ('ac-live',    'r2', $1, $2, '{}'::jsonb, $3, $5, true)
	`, clientID, userID, past, past, now.Add(60*time.Second))

	mustExec(t, pool, `
		INSERT INTO oauth_pkce_requests (signature, request_id, client_id,
		    request, expires_at)
		VALUES
		    ('pk-expired', 'r1', $1, '{}'::jsonb, $2),
		    ('pk-live',    'r2', $1, '{}'::jsonb, $3)
	`, clientID, past, now.Add(60*time.Second))

	// Access tokens — expired + outside grace (24h) deletes; expired
	// but within grace stays; live stays.
	mustExec(t, pool, `
		INSERT INTO oauth_access_tokens (signature, request_id, client_id,
		    user_id, request, requested_at, expires_at, revoked_at)
		VALUES
		    ('at-old',   'r1', $1, $2, '{}'::jsonb, $3, $3, NULL),
		    ('at-grace', 'r2', $1, $2, '{}'::jsonb, $4, $4, NULL),
		    ('at-live',  'r3', $1, $2, '{}'::jsonb, $5, $6, NULL)
	`, clientID, userID, longPast, past, now, now.Add(1*time.Hour))

	// Refresh tokens — both expires_at and revoked_at can trigger
	// deletion; never-expires (NULL expires_at) stays.
	mustExec(t, pool, `
		INSERT INTO oauth_refresh_tokens (signature, request_id, client_id,
		    user_id, request, requested_at, expires_at, active, revoked_at)
		VALUES
		    ('rt-old',         'r1', $1, $2, '{}'::jsonb, $3, $3, false, NULL),
		    ('rt-revoked-old', 'r2', $1, $2, '{}'::jsonb, $3, NULL, false, $3),
		    ('rt-live',        'r3', $1, $2, '{}'::jsonb, $4, $5, true, NULL),
		    ('rt-eternal',     'r4', $1, $2, '{}'::jsonb, $4, NULL, true, NULL)
	`, clientID, userID, longPast, now, now.Add(30*24*time.Hour))

	res, err := st.CleanupExpired(ctx, now)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}

	if res.AuthCodesDeleted != 1 {
		t.Errorf("AuthCodesDeleted = %d, want 1", res.AuthCodesDeleted)
	}
	if res.PKCERequestsDeleted != 1 {
		t.Errorf("PKCERequestsDeleted = %d, want 1", res.PKCERequestsDeleted)
	}
	if res.AccessTokensDeleted != 1 {
		t.Errorf("AccessTokensDeleted = %d, want 1 (only at-old past 24h grace)", res.AccessTokensDeleted)
	}
	if res.RefreshTokensDeleted != 2 {
		t.Errorf("RefreshTokensDeleted = %d, want 2 (rt-old + rt-revoked-old)", res.RefreshTokensDeleted)
	}

	// Live rows must remain.
	for _, want := range []struct {
		table, sig string
	}{
		{"oauth_auth_codes", "ac-live"},
		{"oauth_pkce_requests", "pk-live"},
		{"oauth_access_tokens", "at-live"},
		{"oauth_access_tokens", "at-grace"},
		{"oauth_refresh_tokens", "rt-live"},
		{"oauth_refresh_tokens", "rt-eternal"},
	} {
		n := countRows(t, pool, "SELECT count(*) FROM "+want.table+" WHERE signature=$1", want.sig)
		if n != 1 {
			t.Errorf("%s.%s should survive: count=%d", want.table, want.sig, n)
		}
	}
}

// TestExportConnectionsForUser: returns one entry per refresh token
// the user owns, joined with the client metadata, with token signatures
// excluded. Cross-user rows are not visible.
func TestExportConnectionsForUser(t *testing.T) {
	st, store, pool, userA, clientID := setup(t)
	ctx := context.Background()
	userB := seedUser(t, store, "userB")

	now := time.Now().UTC().Truncate(time.Second)

	mustExec(t, pool, `
		INSERT INTO oauth_refresh_tokens (signature, request_id, client_id,
		    user_id, request, requested_at, expires_at, active)
		VALUES
		    ('rt-A1', 'r1', $1, $2,
		     jsonb_build_object('session', jsonb_build_object('AgentEmail','a@e.dev')),
		     $4, $5, true),
		    ('rt-A2', 'r2', $1, $2,
		     jsonb_build_object('session', jsonb_build_object('AgentEmail','b@e.dev')),
		     $4, $5, true),
		    ('rt-B1', 'r3', $1, $3, '{}'::jsonb, $4, $5, true)
	`, clientID, userA, userB, now, now.Add(30*24*time.Hour))

	got, err := st.ExportConnectionsForUser(ctx, userA)
	if err != nil {
		t.Fatalf("ExportConnectionsForUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries for userA, got %d", len(got))
	}
	wantEmails := map[string]bool{"a@e.dev": true, "b@e.dev": true}
	for _, c := range got {
		if !wantEmails[c.AgentEmail] {
			t.Errorf("unexpected agent_email in export: %q", c.AgentEmail)
		}
		if c.ClientID != clientID {
			t.Errorf("ClientID = %q, want %q", c.ClientID, clientID)
		}
		if c.ClientName != "test client" {
			t.Errorf("ClientName = %q, want %q", c.ClientName, "test client")
		}
		if c.Scope != "mcp" {
			t.Errorf("Scope = %q, want %q", c.Scope, "mcp")
		}
	}

	// userB sees only its one row.
	gotB, err := st.ExportConnectionsForUser(ctx, userB)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotB) != 1 {
		t.Errorf("userB connections = %d, want 1", len(gotB))
	}
}

// TestCountUserOAuthRows: returns the right scalar counts and is
// scoped to one user. Also verifies the schema CASCADE that
// handleDeleteUserData relies on for actual deletion.
func TestCountUserOAuthRows(t *testing.T) {
	st, store, pool, userA, clientID := setup(t)
	ctx := context.Background()
	userB := seedUser(t, store, "userB")

	now := time.Now().UTC().Truncate(time.Second)
	exp := now.Add(time.Hour)

	mustExec(t, pool, `
		INSERT INTO oauth_auth_codes (signature, request_id, client_id, user_id,
		    request, requested_at, expires_at, active)
		VALUES ('ac-A',  'r1', $1, $2, '{}'::jsonb, $3, $4, true),
		       ('ac-B',  'r2', $1, $5, '{}'::jsonb, $3, $4, true)
	`, clientID, userA, now, exp, userB)
	mustExec(t, pool, `
		INSERT INTO oauth_access_tokens (signature, request_id, client_id,
		    user_id, request, requested_at, expires_at)
		VALUES ('at-A1', 'r1', $1, $2, '{}'::jsonb, $3, $4),
		       ('at-A2', 'r2', $1, $2, '{}'::jsonb, $3, $4)
	`, clientID, userA, now, exp)
	mustExec(t, pool, `
		INSERT INTO oauth_refresh_tokens (signature, request_id, client_id,
		    user_id, request, requested_at, expires_at, active)
		VALUES ('rt-A',  'r1', $1, $2, '{}'::jsonb, $3, $4, true)
	`, clientID, userA, now, exp)

	c, err := st.CountUserOAuthRows(ctx, userA)
	if err != nil {
		t.Fatalf("CountUserOAuthRows: %v", err)
	}
	if c.AuthCodes != 1 || c.AccessTokens != 2 || c.RefreshTokens != 1 {
		t.Errorf("counts = %+v, want {1,2,1}", c)
	}

	// Schema CASCADE on user delete: handleDeleteUserData reports
	// counts via CountUserOAuthRows, then DeleteUserData cascades; if
	// FK ON DELETE behavior ever drifts to RESTRICT/SET NULL the count
	// would lie. Verify CASCADE here.
	mustExec(t, pool, `DELETE FROM users WHERE id = $1`, userA)
	for _, tbl := range []string{"oauth_auth_codes", "oauth_access_tokens", "oauth_refresh_tokens"} {
		if n := countRows(t, pool, "SELECT count(*) FROM "+tbl+" WHERE user_id = $1", userA); n != 0 {
			t.Errorf("CASCADE should have wiped %s for user, got %d rows", tbl, n)
		}
	}
}
