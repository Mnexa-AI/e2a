package oauth_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mnexa-AI/e2a/internal/oauth"
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
	// but within grace stays; live stays. Revoked tokens follow the
	// same grace clock: revoked_at < now-24h deletes, recent revoke
	// stays. The mix below covers all four cases (live / in-grace /
	// out-of-grace / revoked-recent / revoked-old).
	mustExec(t, pool, `
		INSERT INTO oauth_access_tokens (signature, request_id, client_id,
		    user_id, request, requested_at, expires_at, revoked_at)
		VALUES
		    ('at-old',           'r1', $1, $2, '{}'::jsonb, $3, $3, NULL),
		    ('at-grace',         'r2', $1, $2, '{}'::jsonb, $4, $4, NULL),
		    ('at-live',          'r3', $1, $2, '{}'::jsonb, $5, $6, NULL),
		    ('at-revoked-recent','r4', $1, $2, '{}'::jsonb, $5, $6, $5),
		    ('at-revoked-old',   'r5', $1, $2, '{}'::jsonb, $3, $3, $3)
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
	if res.AccessTokensDeleted != 2 {
		t.Errorf("AccessTokensDeleted = %d, want 2 (at-old + at-revoked-old past 24h grace)", res.AccessTokensDeleted)
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
		{"oauth_access_tokens", "at-revoked-recent"},
		{"oauth_refresh_tokens", "rt-live"},
		{"oauth_refresh_tokens", "rt-eternal"},
	} {
		n := countRows(t, pool, "SELECT count(*) FROM "+want.table+" WHERE signature=$1", want.sig)
		if n != 1 {
			t.Errorf("%s.%s should survive: count=%d", want.table, want.sig, n)
		}
	}
}

// TestCleanupExpired_PrunesAbandonedDCRClients: anonymous-registered
// clients older than 90 days with no live tokens get removed; clients
// with surviving tokens (any of the three child tables) stay; operator-
// curated clients are exempt regardless of age.
func TestCleanupExpired_PrunesAbandonedDCRClients(t *testing.T) {
	st, _, pool, userID, _ := setup(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-100 * 24 * time.Hour) // outside the 90d grace
	young := now.Add(-30 * 24 * time.Hour)

	// Seed: 4 clients to cover the matrix.
	mustExec(t, pool, `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, grant_types,
		     response_types, scopes, audiences, token_endpoint_auth_method,
		     public, created_via, created_at)
		VALUES
		    ('mcp_old_abandoned', 'old abandoned', ARRAY['http://localhost/cb'],
		     ARRAY['authorization_code'], ARRAY['code'], ARRAY['agent'], ARRAY[]::TEXT[],
		     'none', TRUE, 'dcr', $1),
		    ('mcp_old_with_token', 'old still in use', ARRAY['http://localhost/cb'],
		     ARRAY['authorization_code'], ARRAY['code'], ARRAY['agent'], ARRAY[]::TEXT[],
		     'none', TRUE, 'dcr', $1),
		    ('mcp_young_abandoned', 'young abandoned', ARRAY['http://localhost/cb'],
		     ARRAY['authorization_code'], ARRAY['code'], ARRAY['agent'], ARRAY[]::TEXT[],
		     'none', TRUE, 'dcr', $2),
		    ('mcp_admin_old', 'operator curated', ARRAY['http://localhost/cb'],
		     ARRAY['authorization_code'], ARRAY['code'], ARRAY['agent'], ARRAY[]::TEXT[],
		     'none', TRUE, 'admin', $1)
	`, old, young)

	// mcp_old_with_token has a refresh row → must survive even though
	// it's past the 90d threshold.
	mustExec(t, pool, `
		INSERT INTO oauth_refresh_tokens (signature, request_id, client_id,
		    user_id, request, requested_at, expires_at, active)
		VALUES ('rt-attached', 'rA', 'mcp_old_with_token', $1, '{}'::jsonb, $2, $3, true)
	`, userID, now, now.Add(30*24*time.Hour))

	res, err := st.CleanupExpired(ctx, now)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if res.ClientsDeleted != 1 {
		t.Fatalf("ClientsDeleted = %d, want 1 (only mcp_old_abandoned)", res.ClientsDeleted)
	}

	// Spot-check the survivors.
	for _, want := range []struct {
		id   string
		stay bool
	}{
		{"mcp_old_abandoned", false},
		{"mcp_old_with_token", true},
		{"mcp_young_abandoned", true},
		{"mcp_admin_old", true},
	} {
		n := countRows(t, pool, `SELECT count(*) FROM oauth_clients WHERE client_id = $1`, want.id)
		switch {
		case want.stay && n != 1:
			t.Errorf("%s should survive: count = %d", want.id, n)
		case !want.stay && n != 0:
			t.Errorf("%s should be deleted: count = %d", want.id, n)
		}
	}
}

// TestExportConnectionsForUser: returns one entry per CONSENT
// (client_id, agent_email) — not per refresh token row. Within a
// consent we aggregate IssuedAt (earliest), ExpiresAt (latest),
// RevokedAt (only when ALL rotations are revoked).
func TestExportConnectionsForUser(t *testing.T) {
	st, store, pool, userA, clientID := setup(t)
	ctx := context.Background()
	userB := seedUser(t, store, "userB")

	earlier := time.Now().UTC().Truncate(time.Second).Add(-72 * time.Hour)
	later := earlier.Add(48 * time.Hour)
	expEarly := earlier.Add(30 * 24 * time.Hour)
	expLate := later.Add(30 * 24 * time.Hour)

	// Consent #1 (userA + a@e.dev): two rotations, both still active.
	// The earliest IssuedAt + latest ExpiresAt are what should
	// surface in the aggregate; no RevokedAt because neither row is
	// revoked.
	// Consent #2 (userA + b@e.dev): two rotations, BOTH revoked at
	// the later timestamp — RevokedAt should be reported.
	// Half-revoked consent (rt-A5 + rt-A6 with same agent_email c@e.dev,
	// one revoked, one active): RevokedAt must stay NULL because at
	// least one row can still hand out tokens.
	// userB row: must not appear in userA's output.
	mustExec(t, pool, `
		INSERT INTO oauth_refresh_tokens (signature, request_id, client_id,
		    user_id, request, requested_at, expires_at, active, revoked_at, created_at)
		VALUES
		    ('rt-A1', 'r1', $1, $2,
		     jsonb_build_object('session', jsonb_build_object('agent_email','a@e.dev')),
		     $3, $4, true, NULL, $3),
		    ('rt-A2', 'r2', $1, $2,
		     jsonb_build_object('session', jsonb_build_object('agent_email','a@e.dev')),
		     $5, $6, true, NULL, $5),

		    ('rt-A3', 'r3', $1, $2,
		     jsonb_build_object('session', jsonb_build_object('agent_email','b@e.dev')),
		     $3, $4, false, $5, $3),
		    ('rt-A4', 'r4', $1, $2,
		     jsonb_build_object('session', jsonb_build_object('agent_email','b@e.dev')),
		     $5, $6, false, $5, $5),

		    ('rt-A5', 'r5', $1, $2,
		     jsonb_build_object('session', jsonb_build_object('agent_email','c@e.dev')),
		     $3, $4, false, $5, $3),
		    ('rt-A6', 'r6', $1, $2,
		     jsonb_build_object('session', jsonb_build_object('agent_email','c@e.dev')),
		     $5, $6, true, NULL, $5),

		    ('rt-B1', 'r7', $1, $7, '{}'::jsonb, $3, $4, true, NULL, $3)
	`, clientID, userA, earlier, expEarly, later, expLate, userB)

	got, err := st.ExportConnectionsForUser(ctx, userA)
	if err != nil {
		t.Fatalf("ExportConnectionsForUser: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 consents for userA (a/b/c@e.dev), got %d: %+v", len(got), got)
	}
	byEmail := map[string]ConnectionEntryFields{
		"a@e.dev": {wantRevoked: false, wantClientID: clientID, wantClientName: "test client", wantScope: "agent"},
		"b@e.dev": {wantRevoked: true, wantClientID: clientID, wantClientName: "test client", wantScope: "agent"},
		"c@e.dev": {wantRevoked: false, wantClientID: clientID, wantClientName: "test client", wantScope: "agent"},
	}
	for _, c := range got {
		want, ok := byEmail[c.AgentEmail]
		if !ok {
			t.Errorf("unexpected agent_email in export: %q", c.AgentEmail)
			continue
		}
		if c.ClientID != want.wantClientID {
			t.Errorf("[%s] ClientID = %q, want %q", c.AgentEmail, c.ClientID, want.wantClientID)
		}
		if c.ClientName != want.wantClientName {
			t.Errorf("[%s] ClientName = %q, want %q", c.AgentEmail, c.ClientName, want.wantClientName)
		}
		if c.Scope != want.wantScope {
			t.Errorf("[%s] Scope = %q, want %q", c.AgentEmail, c.Scope, want.wantScope)
		}
		if !c.IssuedAt.Equal(earlier) {
			t.Errorf("[%s] IssuedAt = %v, want MIN(earlier=%v)", c.AgentEmail, c.IssuedAt, earlier)
		}
		if c.ExpiresAt == nil || !c.ExpiresAt.Equal(expLate) {
			t.Errorf("[%s] ExpiresAt = %v, want MAX(expLate=%v)", c.AgentEmail, c.ExpiresAt, expLate)
		}
		switch want.wantRevoked {
		case true:
			if c.RevokedAt == nil {
				t.Errorf("[%s] RevokedAt = nil, want non-nil (all rotations revoked)", c.AgentEmail)
			}
		case false:
			if c.RevokedAt != nil {
				t.Errorf("[%s] RevokedAt = %v, want nil (at least one rotation active)", c.AgentEmail, *c.RevokedAt)
			}
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

// Regression: prior versions of the test seeded the session JSONB
// directly via `jsonb_build_object('agent_email', ...)`. The query
// at the time read `request->'session'->>'AgentEmail'` (Go field
// name). The test passed against the hand-built JSONB but production
// rows — written by fosite via `json.Marshal(*Session)` — landed
// with the lowercase `agent_email` json-tag, so every real
// connection exported `agent_email: ""`. This test serializes a real
// Session via the production marshal path and asserts the export
// round-trips, locking the contract against future drift.
func TestExportConnectionsForUser_UsesProductionSessionShape(t *testing.T) {
	st, _, pool, userID, clientID := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	exp := now.Add(time.Hour)

	sess := &oauth.Session{
		UserID:     userID,
		AgentEmail: "round-trip@e.dev",
		Subject:    userID,
	}
	sessJSON, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	// Build the `request` JSONB the same way fosite would: an
	// envelope with a `session` field that holds the marshaled
	// Session. Anything else (form, scopes, …) is irrelevant to
	// this query.
	envelope := map[string]any{"session": json.RawMessage(sessJSON)}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	mustExec(t, pool, `
		INSERT INTO oauth_refresh_tokens (signature, request_id, client_id,
		    user_id, request, requested_at, expires_at, active, created_at)
		VALUES ('rt-prod', 'r-prod', $1, $2, $3::jsonb, $4, $5, true, $4)
	`, clientID, userID, envelopeJSON, now, exp)

	got, err := st.ExportConnectionsForUser(ctx, userID)
	if err != nil {
		t.Fatalf("ExportConnectionsForUser: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 connection, got %d", len(got))
	}
	if got[0].AgentEmail != "round-trip@e.dev" {
		t.Errorf("AgentEmail = %q, want %q — the JSONB key the query reads must match the json tag fosite writes",
			got[0].AgentEmail, "round-trip@e.dev")
	}
}

// ConnectionEntryFields is the test-side expectation shape for a
// single consent group. Pulled out so the table assertions stay
// readable.
type ConnectionEntryFields struct {
	wantRevoked    bool
	wantClientID   string
	wantClientName string
	wantScope      string
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
