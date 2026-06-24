package oauth_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/fosite"
)

// seedClient inserts a public DCR client row and returns its ID.
// The CHECK constraints on oauth_clients require token_endpoint_auth_method='none'
// when public=TRUE, which matches what we want for the PKCE-only path
// fosite handles on our behalf.
func seedClient(t *testing.T, pool *pgxpool.Pool, clientID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO oauth_clients
		    (client_id, client_name, redirect_uris, grant_types,
		     response_types, scopes, audiences, token_endpoint_auth_method,
		     public, created_via)
		VALUES ($1, $2, ARRAY['http://localhost/cb'], ARRAY['authorization_code','refresh_token'],
		        ARRAY['code'], ARRAY['agent'], ARRAY[]::TEXT[], 'none',
		        TRUE, 'dcr')
	`, clientID, "test client")
	if err != nil {
		t.Fatalf("seedClient: %v", err)
	}
}

func seedUser(t *testing.T, store *identity.Store, sub string) string {
	t.Helper()
	u, err := store.CreateOrGetUser(context.Background(),
		fmt.Sprintf("%s@example.com", sub), sub, "google-"+sub)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	return u.ID
}

// newRequester builds a minimal fosite.Requester suitable for handing
// to storage Create* methods. It carries an *oauth.Session so the
// storage adapter can extract user_id for the row.
func newRequester(id, clientID, userID string, now time.Time) fosite.Requester {
	c, _ := newClient(clientID)
	return &fosite.Request{
		ID:             id,
		RequestedAt:    now,
		Client:         c,
		RequestedScope: fosite.Arguments{"agent"},
		GrantedScope:   fosite.Arguments{"agent"},
		Form:           map[string][]string{},
		Session: &oauth.Session{
			UserID:     userID,
			AgentEmail: "agent@example.com",
			Subject:    userID,
		},
	}
}

// newClient returns a minimal fosite.Client matching the row seedClient
// inserts. We only need the methods fosite reads during the storage
// path; the storage adapter re-hydrates the canonical row on lookup.
func newClient(id string) (*oauth.Client, error) {
	return &oauth.Client{
		ID:                       id,
		Name:                     "test client",
		RedirectURIs:             []string{"http://localhost/cb"},
		GrantTypeStrings:         []string{"authorization_code", "refresh_token"},
		ResponseTypeStrings:      []string{"code"},
		ScopeStrings:             []string{"mcp"},
		Public:                   true,
		TokenEndpointAuthMethodS: "none",
	}, nil
}

func setup(t *testing.T) (*oauth.Storage, *identity.Store, *pgxpool.Pool, string, string) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	storage := oauth.NewStorage(pool)
	userID := seedUser(t, store, "user1")
	clientID := "mcp_test1"
	seedClient(t, pool, clientID)
	return storage, store, pool, userID, clientID
}

func TestStorage_AuthorizeCode_RoundTrip(t *testing.T) {
	st, _, _, userID, clientID := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	req := newRequester("req-1", clientID, userID, now)
	if err := st.CreateAuthorizeCodeSession(ctx, "code-sig-1", req); err != nil {
		t.Fatalf("CreateAuthorizeCodeSession: %v", err)
	}

	got, err := st.GetAuthorizeCodeSession(ctx, "code-sig-1", &oauth.Session{})
	if err != nil {
		t.Fatalf("GetAuthorizeCodeSession (active): %v", err)
	}
	if got.GetID() != "req-1" {
		t.Errorf("request id = %q, want req-1", got.GetID())
	}
	sess, ok := got.GetSession().(*oauth.Session)
	if !ok {
		t.Fatalf("session type = %T, want *oauth.Session", got.GetSession())
	}
	if sess.UserID != userID {
		t.Errorf("UserID = %q, want %q", sess.UserID, userID)
	}
	if sess.AgentEmail != "agent@example.com" {
		t.Errorf("AgentEmail = %q, want agent@example.com", sess.AgentEmail)
	}

	if err := st.InvalidateAuthorizeCodeSession(ctx, "code-sig-1"); err != nil {
		t.Fatalf("InvalidateAuthorizeCodeSession: %v", err)
	}

	got, err = st.GetAuthorizeCodeSession(ctx, "code-sig-1", &oauth.Session{})
	if !errors.Is(err, fosite.ErrInvalidatedAuthorizeCode) {
		t.Errorf("err = %v, want ErrInvalidatedAuthorizeCode", err)
	}
	if got == nil || got.GetID() != "req-1" {
		t.Errorf("requester missing on invalidated read; got %v", got)
	}
}

func TestStorage_AuthorizeCode_ConcurrentConsume(t *testing.T) {
	st, _, _, userID, clientID := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	req := newRequester("req-conc", clientID, userID, now)
	if err := st.CreateAuthorizeCodeSession(ctx, "code-conc", req); err != nil {
		t.Fatalf("CreateAuthorizeCodeSession: %v", err)
	}

	const N = 16
	var (
		wg          sync.WaitGroup
		successes   atomic.Int32
		invalidated atomic.Int32
		other       atomic.Int32
		start       = make(chan struct{})
	)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start
			err := st.InvalidateAuthorizeCodeSession(ctx, "code-conc")
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, fosite.ErrInvalidatedAuthorizeCode):
				invalidated.Add(1)
			default:
				other.Add(1)
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Errorf("successes = %d, want 1 (only one consume should win)", got)
	}
	if got := invalidated.Load(); got != N-1 {
		t.Errorf("invalidated = %d, want %d", got, N-1)
	}
	if got := other.Load(); got != 0 {
		t.Errorf("other = %d, want 0", got)
	}
}

func TestStorage_RotateRefreshToken_ConcurrentRotate(t *testing.T) {
	st, _, _, userID, clientID := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	req := newRequester("req-rot", clientID, userID, now)
	// Give the refresh a non-zero session expiry so the M1 path uses it
	// (the test specifically for NULL expiry covers the zero-expiry case).
	req.GetSession().(*oauth.Session).SetExpiresAt(fosite.RefreshToken, now.Add(30*24*time.Hour))
	if err := st.CreateRefreshTokenSession(ctx, "refresh-conc", "access-conc", req); err != nil {
		t.Fatalf("CreateRefreshTokenSession: %v", err)
	}
	if err := st.CreateAccessTokenSession(ctx, "access-conc", req); err != nil {
		t.Fatalf("CreateAccessTokenSession: %v", err)
	}

	const N = 16
	var (
		wg        sync.WaitGroup
		successes atomic.Int32
		inactive  atomic.Int32
		other     atomic.Int32
		start     = make(chan struct{})
	)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start
			err := st.RotateRefreshToken(ctx, "req-rot", "refresh-conc")
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, fosite.ErrInactiveToken):
				inactive.Add(1)
			default:
				other.Add(1)
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Errorf("successes = %d, want 1 (only one rotate should win)", got)
	}
	if got := inactive.Load(); got != N-1 {
		t.Errorf("inactive = %d, want %d", got, N-1)
	}
	if got := other.Load(); got != 0 {
		t.Errorf("other = %d, want 0", got)
	}
}

func TestStorage_RotateRefreshToken_CascadesAccess(t *testing.T) {
	st, _, pool, userID, clientID := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	req := newRequester("req-cascade", clientID, userID, now)
	req.GetSession().(*oauth.Session).SetExpiresAt(fosite.RefreshToken, now.Add(30*24*time.Hour))
	if err := st.CreateAccessTokenSession(ctx, "access-cascade", req); err != nil {
		t.Fatalf("CreateAccessTokenSession: %v", err)
	}
	if err := st.CreateRefreshTokenSession(ctx, "refresh-cascade", "access-cascade", req); err != nil {
		t.Fatalf("CreateRefreshTokenSession: %v", err)
	}

	if err := st.RotateRefreshToken(ctx, "req-cascade", "refresh-cascade"); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	var revokedAt *time.Time
	err := pool.QueryRow(ctx,
		`SELECT revoked_at FROM oauth_access_tokens WHERE signature = $1`,
		"access-cascade").Scan(&revokedAt)
	if err != nil {
		t.Fatalf("query access row: %v", err)
	}
	if revokedAt == nil {
		t.Errorf("access token revoked_at = NULL, want non-NULL after rotate")
	}
}

func TestStorage_RefreshToken_ZeroSessionExpiry(t *testing.T) {
	st, _, pool, userID, clientID := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	req := newRequester("req-noexpiry", clientID, userID, now)
	// Deliberately do NOT set ExpiresAt for RefreshToken: simulates
	// the RefreshTokenLifespan=-1 (no expiry) path.

	if err := st.CreateRefreshTokenSession(ctx, "refresh-noexpiry", "", req); err != nil {
		t.Fatalf("CreateRefreshTokenSession: %v", err)
	}

	var expiresAt *time.Time
	err := pool.QueryRow(ctx,
		`SELECT expires_at FROM oauth_refresh_tokens WHERE signature = $1`,
		"refresh-noexpiry").Scan(&expiresAt)
	if err != nil {
		t.Fatalf("query refresh row: %v", err)
	}
	if expiresAt != nil {
		t.Errorf("expires_at = %v, want NULL for zero session expiry", *expiresAt)
	}
}

func TestStorage_AuthorizeCode_HonorsSessionLifespan(t *testing.T) {
	st, _, pool, userID, clientID := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	req := newRequester("req-lifespan", clientID, userID, now)
	custom := now.Add(5 * time.Minute)
	req.GetSession().(*oauth.Session).SetExpiresAt(fosite.AuthorizeCode, custom)

	if err := st.CreateAuthorizeCodeSession(ctx, "code-lifespan", req); err != nil {
		t.Fatalf("CreateAuthorizeCodeSession: %v", err)
	}

	var expiresAt time.Time
	err := pool.QueryRow(ctx,
		`SELECT expires_at FROM oauth_auth_codes WHERE signature = $1`,
		"code-lifespan").Scan(&expiresAt)
	if err != nil {
		t.Fatalf("query code row: %v", err)
	}
	if !expiresAt.Equal(custom) {
		t.Errorf("expires_at = %v, want %v (session-supplied)", expiresAt, custom)
	}
}

func TestStorage_Transactional_Rollback(t *testing.T) {
	st, _, pool, userID, clientID := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	txCtx, err := st.BeginTX(ctx)
	if err != nil {
		t.Fatalf("BeginTX: %v", err)
	}

	req := newRequester("req-rollback", clientID, userID, now)
	if err := st.CreateAuthorizeCodeSession(txCtx, "code-rollback", req); err != nil {
		t.Fatalf("CreateAuthorizeCodeSession in tx: %v", err)
	}

	if err := st.Rollback(txCtx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var count int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM oauth_auth_codes WHERE signature = $1`,
		"code-rollback").Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Errorf("row count = %d, want 0 after rollback", count)
	}
}

func TestStorage_Client_NotFound(t *testing.T) {
	st, _, _, _, _ := setup(t)
	ctx := context.Background()

	_, err := st.GetClient(ctx, "mcp_does_not_exist")
	if !errors.Is(err, fosite.ErrInvalidClient) {
		t.Errorf("err = %v, want fosite.ErrInvalidClient", err)
	}
}

func TestStorage_Session_RoundTrip(t *testing.T) {
	st, _, _, userID, clientID := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	req := newRequester("req-session", clientID, userID, now)
	s := req.GetSession().(*oauth.Session)
	s.AgentEmail = "specific-agent@bot.example.com"
	s.Username = "owner@example.com"
	exp := now.Add(time.Hour)
	s.SetExpiresAt(fosite.AccessToken, exp)
	refExp := now.Add(30 * 24 * time.Hour)
	s.SetExpiresAt(fosite.RefreshToken, refExp)

	if err := st.CreateAccessTokenSession(ctx, "tok-session", req); err != nil {
		t.Fatalf("CreateAccessTokenSession: %v", err)
	}

	out := &oauth.Session{}
	got, err := st.GetAccessTokenSession(ctx, "tok-session", out)
	if err != nil {
		t.Fatalf("GetAccessTokenSession: %v", err)
	}
	if got.GetID() != "req-session" {
		t.Errorf("ID = %q, want req-session", got.GetID())
	}
	if out.UserID != userID {
		t.Errorf("UserID = %q, want %q", out.UserID, userID)
	}
	if out.AgentEmail != "specific-agent@bot.example.com" {
		t.Errorf("AgentEmail = %q, want specific-agent@bot.example.com", out.AgentEmail)
	}
	if out.Username != "owner@example.com" {
		t.Errorf("Username = %q, want owner@example.com", out.Username)
	}
	if t1 := out.GetExpiresAt(fosite.AccessToken); !t1.Equal(exp) {
		t.Errorf("AccessToken expiry = %v, want %v", t1, exp)
	}
	if t1 := out.GetExpiresAt(fosite.RefreshToken); !t1.Equal(refExp) {
		t.Errorf("RefreshToken expiry = %v, want %v", t1, refExp)
	}
}
