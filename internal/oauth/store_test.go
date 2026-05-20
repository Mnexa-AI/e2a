package oauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// seedUser returns a freshly-inserted user ID. Needed because the
// oauth_authorization_codes / oauth_tokens tables FK on users.id.
func seedUser(t *testing.T, store *identity.Store) string {
	t.Helper()
	user, err := store.CreateOrGetUser(context.Background(),
		"oauth-test-"+oauth.NewChainID()+"@example.com",
		"OAuth Test", "google-test-"+oauth.NewChainID())
	if err != nil {
		t.Fatal(err)
	}
	return user.ID
}

// seedClient registers a client and returns its ID. Most tests need
// a client_id to satisfy the codes/tokens FK.
func seedClient(t *testing.T, s *oauth.Store) string {
	t.Helper()
	c := &oauth.Client{
		ClientID:     oauth.NewClientID(),
		ClientName:   "test-client",
		RedirectURIs: []string{"http://127.0.0.1:54321/callback"},
		ClientType:   "public",
		CreatedVia:   "dcr",
	}
	if err := s.RegisterClient(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c.ClientID
}

// ────────────────────────── token prefix shape ──────────────────────────

func TestIDGenerators_HaveCorrectPrefixesAndLengths(t *testing.T) {
	cases := []struct {
		name    string
		got     string
		prefix  string
		hexLen  int
	}{
		{"client_id", oauth.NewClientID(), "mcp_", 12},
		{"auth_code", oauth.NewAuthCode(), "oace_", 32},
		{"access_token", oauth.NewAccessToken(), "ate2a_", 32},
		{"refresh_token", oauth.NewRefreshToken(), "rte2a_", 32},
		{"chain_id", oauth.NewChainID(), "rch_", 24},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.HasPrefix(c.got, c.prefix) {
				t.Fatalf("expected prefix %q, got %q", c.prefix, c.got)
			}
			rest := strings.TrimPrefix(c.got, c.prefix)
			if len(rest) != c.hexLen {
				t.Fatalf("expected %d hex chars after prefix, got %d (%q)", c.hexLen, len(rest), rest)
			}
		})
	}
}

func TestIDGenerators_AreUnique(t *testing.T) {
	// 1000 generations should never collide given 128-bit entropy.
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := oauth.NewAccessToken()
		if seen[id] {
			t.Fatalf("collision after %d iterations: %s", i, id)
		}
		seen[id] = true
	}
}

// ────────────────────────── clients ──────────────────────────

func TestRegisterClient_PublicAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)

	c := &oauth.Client{
		ClientID:     oauth.NewClientID(),
		ClientName:   "Claude Code",
		RedirectURIs: []string{"http://127.0.0.1:54321/callback"},
		ClientType:   "public",
		Metadata:     json.RawMessage(`{"software_id":"claude-code"}`),
		CreatedVia:   "dcr",
	}
	if err := store.RegisterClient(ctx, c); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetClient(ctx, c.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientName != "Claude Code" || got.ClientType != "public" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.RedirectURIs) != 1 || got.RedirectURIs[0] != "http://127.0.0.1:54321/callback" {
		t.Errorf("redirect_uris: %v", got.RedirectURIs)
	}
	if got.ClientSecretHash != "" {
		t.Errorf("public client should have empty secret hash, got %q", got.ClientSecretHash)
	}
}

func TestRegisterClient_Confidential(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)

	c := &oauth.Client{
		ClientID:         oauth.NewClientID(),
		ClientName:       "admin-tool",
		RedirectURIs:     []string{"https://internal.example.com/cb"},
		ClientType:       "confidential",
		ClientSecretHash: "argon2id$v=19$m=65536$saltsalt$hash",
		CreatedVia:       "admin",
	}
	if err := store.RegisterClient(ctx, c); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetClient(ctx, c.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientSecretHash != c.ClientSecretHash {
		t.Errorf("secret hash round-trip mismatch")
	}
	if got.CreatedVia != "admin" {
		t.Errorf("created_via: %q", got.CreatedVia)
	}
}

func TestGetClient_NotFound(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)

	_, err := store.GetClient(ctx, "mcp_nonexistent")
	if !errors.Is(err, oauth.ErrClientNotFound) {
		t.Fatalf("expected ErrClientNotFound, got %v", err)
	}
}

// ────────────────────────── authorization codes ──────────────────────────

func TestAtomicConsumeCode_FreshExchange(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	code := oauth.NewAuthCode()
	if err := store.IssueCode(ctx, &oauth.AuthorizationCode{
		Code:                code,
		ClientID:            clientID,
		UserID:              userID,
		AgentEmail:          "bot@agents.e2a.dev",
		RedirectURI:         "http://127.0.0.1:54321/callback",
		CodeChallenge:       "challenge-value",
		CodeChallengeMethod: "S256",
		Scope:               "e2a",
		ExpiresAt:           time.Now().Add(60 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	got, result, err := store.AtomicConsumeCode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if result != oauth.ConsumeFresh {
		t.Fatalf("expected ConsumeFresh, got %v", result)
	}
	if got.ClientID != clientID || got.UserID != userID {
		t.Errorf("returned code has wrong owner")
	}
	if got.ConsumedAt == nil {
		t.Errorf("ConsumedAt should be set on fresh consume")
	}
}

func TestAtomicConsumeCode_AlreadyConsumed(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	code := oauth.NewAuthCode()
	if err := store.IssueCode(ctx, &oauth.AuthorizationCode{
		Code: code, ClientID: clientID, UserID: userID,
		RedirectURI: "http://127.0.0.1:54321/callback",
		CodeChallenge: "x", CodeChallengeMethod: "S256",
		Scope: "e2a", ExpiresAt: time.Now().Add(60 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	// First consume: fresh.
	if _, r, err := store.AtomicConsumeCode(ctx, code); err != nil || r != oauth.ConsumeFresh {
		t.Fatalf("first consume: r=%v err=%v", r, err)
	}
	// Replay: must report already-consumed and still return the row
	// (caller needs UserID + ClientID to revoke downstream tokens).
	got, r, err := store.AtomicConsumeCode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if r != oauth.ConsumeAlreadyConsumed {
		t.Fatalf("replay: expected ConsumeAlreadyConsumed, got %v", r)
	}
	if got == nil || got.ClientID != clientID || got.UserID != userID {
		t.Errorf("replay should return the row data for downstream revocation")
	}
}

func TestAtomicConsumeCode_Expired(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	code := oauth.NewAuthCode()
	if err := store.IssueCode(ctx, &oauth.AuthorizationCode{
		Code: code, ClientID: clientID, UserID: userID,
		RedirectURI: "http://127.0.0.1:54321/callback",
		CodeChallenge: "x", CodeChallengeMethod: "S256",
		Scope: "e2a", ExpiresAt: time.Now().Add(-1 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	_, r, err := store.AtomicConsumeCode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if r != oauth.ConsumeExpired {
		t.Fatalf("expected ConsumeExpired, got %v", r)
	}
}

func TestAtomicConsumeCode_NotFound(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	_, r, err := store.AtomicConsumeCode(ctx, "oace_doesnotexist")
	if err != nil {
		t.Fatal(err)
	}
	if r != oauth.ConsumeNotFound {
		t.Fatalf("expected ConsumeNotFound, got %v", r)
	}
}

// ────────────────────────── tokens ──────────────────────────

func newTestToken(clientID, userID string) *oauth.Token {
	now := time.Now()
	refreshExp := now.Add(oauth.RefreshTokenLifetime)
	return &oauth.Token{
		AccessToken:      oauth.NewAccessToken(),
		RefreshToken:     oauth.NewRefreshToken(),
		RefreshChainID:   oauth.NewChainID(),
		ClientID:         clientID,
		UserID:           userID,
		AgentEmail:       "bot@agents.e2a.dev",
		Scope:            "e2a",
		ExpiresAt:        now.Add(oauth.AccessTokenLifetime),
		RefreshExpiresAt: &refreshExp,
	}
}

// TestIssueToken_StoresHashNotPlaintext is the regression guard for the
// at-rest hashing fix. We issue a token, then query the raw column
// directly and confirm:
//   - the access_token_hash column equals SHA-256(plaintext)
//   - the access_token_hash column does NOT equal the plaintext itself
//   - same for refresh_token_hash
//   - there is no column named "access_token" or "refresh_token" we can
//     still query for the plaintext (defense against a partial schema
//     migration)
//
// If anyone reverts the store to write plaintext, or adds a debug
// column that does, this test fails loud.
func TestIssueToken_StoresHashNotPlaintext(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	tok := newTestToken(clientID, userID)
	if err := store.IssueToken(ctx, tok); err != nil {
		t.Fatal(err)
	}

	var storedAccessHash, storedRefreshHash string
	err := pool.QueryRow(ctx, `
		SELECT access_token_hash, refresh_token_hash
		FROM oauth_tokens
		WHERE refresh_chain_id = $1
	`, tok.RefreshChainID).Scan(&storedAccessHash, &storedRefreshHash)
	if err != nil {
		t.Fatalf("raw query: %v", err)
	}

	wantAccessHash := oauth.HashToken(tok.AccessToken)
	if storedAccessHash != wantAccessHash {
		t.Errorf("access_token_hash mismatch: want %q, got %q", wantAccessHash, storedAccessHash)
	}
	if storedAccessHash == tok.AccessToken {
		t.Error("FAIL: access_token_hash column contains the plaintext token (should be SHA-256 hex)")
	}
	if strings.HasPrefix(storedAccessHash, oauth.AccessTokenPrefix) {
		t.Errorf("access_token_hash should not have the bearer prefix: got %q", storedAccessHash)
	}

	wantRefreshHash := oauth.HashToken(tok.RefreshToken)
	if storedRefreshHash != wantRefreshHash {
		t.Errorf("refresh_token_hash mismatch: want %q, got %q", wantRefreshHash, storedRefreshHash)
	}
	if storedRefreshHash == tok.RefreshToken {
		t.Error("FAIL: refresh_token_hash column contains the plaintext token (should be SHA-256 hex)")
	}

	// Defense against partial migration: the old plaintext columns must
	// not exist. Querying them should fail with a "column does not
	// exist" error from Postgres.
	var dummy string
	err = pool.QueryRow(ctx, `SELECT access_token FROM oauth_tokens LIMIT 1`).Scan(&dummy)
	if err == nil {
		t.Error("FAIL: access_token column still exists — plaintext fallback present")
	}
	err = pool.QueryRow(ctx, `SELECT refresh_token FROM oauth_tokens LIMIT 1`).Scan(&dummy)
	if err == nil {
		t.Error("FAIL: refresh_token column still exists — plaintext fallback present")
	}
}

func TestIssueTokenAndLookupByAccess(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	tok := newTestToken(clientID, userID)
	if err := store.IssueToken(ctx, tok); err != nil {
		t.Fatal(err)
	}

	got, err := store.LookupTokenByAccess(ctx, tok.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	// Plaintext access token is reconstructed from the lookup input
	// (we hash it for the WHERE; the column stores only the hash).
	if got.AccessToken != tok.AccessToken {
		t.Errorf("AccessToken should round-trip via input: want %q, got %q", tok.AccessToken, got.AccessToken)
	}
	// Refresh-token plaintext is NOT recoverable from a by-access
	// lookup — we don't store it. Confirm it's empty so callers don't
	// accidentally treat a stale field as a live credential.
	if got.RefreshToken != "" {
		t.Errorf("RefreshToken must NOT be populated on LookupTokenByAccess (cannot reconstruct from hash): got %q", got.RefreshToken)
	}
	if got.AgentEmail != "bot@agents.e2a.dev" {
		t.Errorf("agent_email: %q", got.AgentEmail)
	}
	if !got.IsActive(time.Now()) {
		t.Errorf("freshly-issued token should be active")
	}
}

func TestLookupTokenByAccess_NotFound(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	_, err := store.LookupTokenByAccess(ctx, "ate2a_doesnotexist")
	if !errors.Is(err, oauth.ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}

func TestLookupTokenByRefresh_RoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	tok := newTestToken(clientID, userID)
	if err := store.IssueToken(ctx, tok); err != nil {
		t.Fatal(err)
	}

	got, err := store.LookupTokenByRefresh(ctx, tok.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	// Refresh-token plaintext round-trips via input (we hashed it for
	// the WHERE). Access-token plaintext is NOT recoverable from this
	// lookup path; verify the field is empty so a future caller that
	// reads got.AccessToken doesn't quietly authenticate as nothing.
	if got.RefreshToken != tok.RefreshToken {
		t.Errorf("RefreshToken should round-trip via input: want %q, got %q", tok.RefreshToken, got.RefreshToken)
	}
	if got.AccessToken != "" {
		t.Errorf("AccessToken must NOT be populated on LookupTokenByRefresh: got %q", got.AccessToken)
	}
	// The non-credential fields (chain, client, user) MUST round-trip
	// since handlers depend on them for the refresh-grant decision.
	if got.RefreshChainID != tok.RefreshChainID {
		t.Errorf("RefreshChainID round-trip: want %q, got %q", tok.RefreshChainID, got.RefreshChainID)
	}
}

func TestRotateRefreshToken_HappyPath(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	old := newTestToken(clientID, userID)
	if err := store.IssueToken(ctx, old); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	refreshExp := now.Add(oauth.RefreshTokenLifetime)
	fresh := &oauth.Token{
		AccessToken:      oauth.NewAccessToken(),
		RefreshToken:     oauth.NewRefreshToken(),
		RefreshChainID:   old.RefreshChainID, // same chain — that's the invariant
		ClientID:         clientID,
		UserID:           userID,
		AgentEmail:       old.AgentEmail,
		Scope:            old.Scope,
		ExpiresAt:        now.Add(oauth.AccessTokenLifetime),
		RefreshExpiresAt: &refreshExp,
	}
	if err := store.RotateRefreshToken(ctx, old.RefreshToken, fresh); err != nil {
		t.Fatal(err)
	}

	// The old refresh_token should now be NULL — looking it up by
	// refresh returns ErrTokenNotFound.
	if _, err := store.LookupTokenByRefresh(ctx, old.RefreshToken); !errors.Is(err, oauth.ErrTokenNotFound) {
		t.Errorf("old refresh should be invalidated, got %v", err)
	}
	// The new refresh works.
	if _, err := store.LookupTokenByRefresh(ctx, fresh.RefreshToken); err != nil {
		t.Errorf("new refresh should be live: %v", err)
	}
	// The OLD access token is still active (it's the old session
	// still doing its work; rotation only affects refresh, not access).
	old2, err := store.LookupTokenByAccess(ctx, old.AccessToken)
	if err != nil || !old2.IsActive(now) {
		t.Errorf("old access should remain active until its own expiry")
	}
}

func TestRotateRefreshToken_ReusedRefreshReturnsErrTokenNotFound(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	old := newTestToken(clientID, userID)
	if err := store.IssueToken(ctx, old); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	refreshExp := now.Add(oauth.RefreshTokenLifetime)
	// First rotation: success.
	rot1 := &oauth.Token{
		AccessToken: oauth.NewAccessToken(), RefreshToken: oauth.NewRefreshToken(),
		RefreshChainID: old.RefreshChainID, ClientID: clientID, UserID: userID,
		Scope: "e2a", ExpiresAt: now.Add(time.Hour), RefreshExpiresAt: &refreshExp,
	}
	if err := store.RotateRefreshToken(ctx, old.RefreshToken, rot1); err != nil {
		t.Fatalf("first rotate: %v", err)
	}

	// Attacker (or buggy client) replays the OLD refresh token.
	// Expected: rotation fails with ErrTokenNotFound. The /token
	// endpoint then revokes the entire chain.
	rot2 := &oauth.Token{
		AccessToken: oauth.NewAccessToken(), RefreshToken: oauth.NewRefreshToken(),
		RefreshChainID: old.RefreshChainID, ClientID: clientID, UserID: userID,
		Scope: "e2a", ExpiresAt: now.Add(time.Hour), RefreshExpiresAt: &refreshExp,
	}
	err := store.RotateRefreshToken(ctx, old.RefreshToken, rot2)
	if !errors.Is(err, oauth.ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound on reused refresh, got %v", err)
	}
}

func TestRevokeToken(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	tok := newTestToken(clientID, userID)
	if err := store.IssueToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeToken(ctx, tok.AccessToken); err != nil {
		t.Fatal(err)
	}

	got, err := store.LookupTokenByAccess(ctx, tok.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if got.RevokedAt == nil {
		t.Errorf("revoked_at should be set")
	}
	if got.IsActive(time.Now()) {
		t.Errorf("revoked token should not be active")
	}
	// Refresh is NULLed on revoke, so refresh-grant on it returns
	// ErrTokenNotFound (the right surface for a revoked refresh).
	if _, err := store.LookupTokenByRefresh(ctx, tok.RefreshToken); !errors.Is(err, oauth.ErrTokenNotFound) {
		t.Errorf("refresh on revoked token: expected ErrTokenNotFound, got %v", err)
	}
}

func TestRevokeChainByID(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	// Two rotations in the same chain → 3 tokens total.
	t0 := newTestToken(clientID, userID)
	if err := store.IssueToken(ctx, t0); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	refreshExp := now.Add(oauth.RefreshTokenLifetime)
	t1 := &oauth.Token{
		AccessToken: oauth.NewAccessToken(), RefreshToken: oauth.NewRefreshToken(),
		RefreshChainID: t0.RefreshChainID, ClientID: clientID, UserID: userID,
		Scope: "e2a", ExpiresAt: now.Add(time.Hour), RefreshExpiresAt: &refreshExp,
	}
	if err := store.RotateRefreshToken(ctx, t0.RefreshToken, t1); err != nil {
		t.Fatal(err)
	}
	t2 := &oauth.Token{
		AccessToken: oauth.NewAccessToken(), RefreshToken: oauth.NewRefreshToken(),
		RefreshChainID: t0.RefreshChainID, ClientID: clientID, UserID: userID,
		Scope: "e2a", ExpiresAt: now.Add(time.Hour), RefreshExpiresAt: &refreshExp,
	}
	if err := store.RotateRefreshToken(ctx, t1.RefreshToken, t2); err != nil {
		t.Fatal(err)
	}

	// All three rows live in the same chain. RevokeChainByID hits them all.
	n, err := store.RevokeChainByID(ctx, t0.RefreshChainID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("expected 3 rows revoked (the whole chain), got %d", n)
	}

	// Lookup any chain member: revoked_at set, IsActive false.
	for _, ax := range []string{t0.AccessToken, t1.AccessToken, t2.AccessToken} {
		got, err := store.LookupTokenByAccess(ctx, ax)
		if err != nil {
			t.Fatal(err)
		}
		if got.RevokedAt == nil || got.IsActive(time.Now()) {
			t.Errorf("chain member %s should be revoked", ax)
		}
	}
}

func TestRevokeAllByClientUser(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)
	// A SECOND client+user pair that should be unaffected.
	otherClient := seedClient(t, store)
	otherUser := seedUser(t, idStore)

	// Two tokens in different chains for the target pair.
	target1 := newTestToken(clientID, userID)
	target2 := newTestToken(clientID, userID)
	other := newTestToken(otherClient, otherUser)
	for _, tt := range []*oauth.Token{target1, target2, other} {
		if err := store.IssueToken(ctx, tt); err != nil {
			t.Fatal(err)
		}
	}

	n, err := store.RevokeAllByClientUser(ctx, clientID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 rows revoked, got %d", n)
	}

	// The other pair's token is untouched.
	got, err := store.LookupTokenByAccess(ctx, other.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if got.RevokedAt != nil {
		t.Errorf("unrelated token should not be revoked")
	}
}

// TestLookupToken_ExpiredButNotRevoked confirms a token past its
// access expires_at returns the row (not ErrTokenNotFound). The
// caller's IsActive() check is what decides serve-or-reject.
func TestLookupToken_ExpiredButNotRevoked(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	now := time.Now()
	refreshExp := now.Add(oauth.RefreshTokenLifetime)
	tok := &oauth.Token{
		AccessToken: oauth.NewAccessToken(), RefreshToken: oauth.NewRefreshToken(),
		RefreshChainID: oauth.NewChainID(), ClientID: clientID, UserID: userID,
		Scope: "e2a", ExpiresAt: now.Add(-1 * time.Minute), // expired
		RefreshExpiresAt: &refreshExp,
	}
	if err := store.IssueToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	got, err := store.LookupTokenByAccess(ctx, tok.AccessToken)
	if err != nil {
		t.Fatalf("expired token should still be looked up (caller decides): %v", err)
	}
	if got.IsActive(time.Now()) {
		t.Errorf("expired token should report IsActive=false")
	}
	if got.RevokedAt != nil {
		t.Errorf("expired (not revoked) should have RevokedAt nil")
	}
}

// TestUserCascade ensures FK CASCADE on user deletion takes tokens
// with it — defends against orphaned auth state after user deletion.
func TestUserCascade_TokensAndCodesDeletedWithUser(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := oauth.NewStore(pool)
	idStore := identity.NewStore(pool)
	clientID := seedClient(t, store)
	userID := seedUser(t, idStore)

	tok := newTestToken(clientID, userID)
	if err := store.IssueToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	code := oauth.NewAuthCode()
	if err := store.IssueCode(ctx, &oauth.AuthorizationCode{
		Code: code, ClientID: clientID, UserID: userID,
		RedirectURI: "http://127.0.0.1:54321/cb",
		CodeChallenge: "x", CodeChallengeMethod: "S256",
		Scope: "e2a", ExpiresAt: time.Now().Add(60 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	// Direct cascade-triggering DELETE on the users table. This is what
	// identity.DeleteUser does internally; we test the FK shape here.
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
		t.Fatal(err)
	}

	// Both rows should be gone via ON DELETE CASCADE.
	if _, err := store.LookupTokenByAccess(ctx, tok.AccessToken); !errors.Is(err, oauth.ErrTokenNotFound) {
		t.Errorf("token should be cascade-deleted with user, got %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM oauth_authorization_codes WHERE code = $1", code).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("auth code should be cascade-deleted with user, got count=%d", n)
	}
}
