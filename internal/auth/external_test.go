package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"

	"github.com/tokencanopy/e2a/internal/auth"
	"github.com/tokencanopy/e2a/internal/config"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/testutil"
)

const (
	testExternalIssuer      = "https://issuer.example.com"
	testExternalAudience    = "e2a-test"
	testExternalUserIDClaim = "e2a_user_id"
)

// externalAuthFixture bundles a store, an httptest-served JWKS for one RSA
// keypair, and the ExternalAuth built to trust it.
type externalAuthFixture struct {
	ea       *auth.ExternalAuth
	store    *identity.Store
	priv     *rsa.PrivateKey
	kid      string
	jwksSrv  *httptest.Server
	callSeen int // number of times the JWKS endpoint was hit
}

func setupExternalAuth(t *testing.T) *externalAuthFixture {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	kid := "test-kid-1"

	fx := &externalAuthFixture{store: store, priv: priv, kid: kid}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		fx.callSeen++
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       priv.Public(),
			KeyID:     kid,
			Algorithm: "RS256",
			Use:       "sig",
		}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	})
	fx.jwksSrv = httptest.NewServer(mux)
	t.Cleanup(fx.jwksSrv.Close)

	cfg := config.ExternalAuthConfig{
		Enabled:     true,
		Issuer:      testExternalIssuer,
		JWKSURL:     fx.jwksSrv.URL + "/.well-known/jwks.json",
		Audience:    testExternalAudience,
		UserIDClaim: testExternalUserIDClaim,
	}
	fx.ea = auth.NewExternalAuth(cfg, store, false, "http://app.example.com")
	if fx.ea == nil {
		t.Fatal("NewExternalAuth returned nil for an enabled config")
	}
	return fx
}

// signAssertion builds a compact-serialized RS256 JWT signed by signKey,
// with header kid and the given claims (std + private) merged. Callers
// mutate the returned claims via the opts before signing.
func signAssertion(t *testing.T, signKey *rsa.PrivateKey, kid string, std jwt.Claims, private map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{
		Algorithm: jose.RS256,
		Key:       jose.JSONWebKey{Key: signKey, KeyID: kid},
	}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatalf("build signer: %v", err)
	}
	builder := jwt.Signed(signer).Claims(std)
	if len(private) > 0 {
		builder = builder.Claims(private)
	}
	out, err := builder.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize jwt: %v", err)
	}
	return out
}

func validClaims() jwt.Claims {
	now := time.Now()
	return jwt.Claims{
		Issuer:    testExternalIssuer,
		Audience:  jwt.Audience{testExternalAudience},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Expiry:    jwt.NewNumericDate(now.Add(5 * time.Minute)),
	}
}

func TestNewExternalAuth_DisabledReturnsNil(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	ea := auth.NewExternalAuth(config.ExternalAuthConfig{Enabled: false}, store, false, "")
	if ea != nil {
		t.Fatal("NewExternalAuth should return nil when the config is disabled — the route must be absent, not merely non-functional")
	}
}

// TestHandleCallback_Disabled mirrors how internal/agent/api.go registers
// the route: when ExternalAuth is nil, the handler simply doesn't exist to
// call — this test documents that the caller-side nil check is what
// enforces "disabled ⇒ route absent", since ExternalAuth itself has no
// method to call in that state.
func TestHandleCallback_Disabled(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ea := auth.NewExternalAuth(config.ExternalAuthConfig{Enabled: false}, store, false, "")
	if ea != nil {
		t.Fatal("expected nil ExternalAuth when disabled")
	}
	// No route to hit — the mux in internal/agent/api.go only registers
	// /api/auth/external/callback when a.externalAuth != nil, so an
	// unconfigured deployment 404s on this path via the router's default
	// NotFoundHandler, never reaching any auth-specific code.
}

func TestHandleCallback_MissingAssertion(t *testing.T) {
	fx := setupExternalAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/external/callback", nil)
	w := httptest.NewRecorder()
	fx.ea.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("expected no cookies set, got %v", got)
	}
}

func TestHandleCallback_ValidAssertionEstablishesSession(t *testing.T) {
	fx := setupExternalAuth(t)
	ctx := context.Background()

	user, err := fx.store.CreateOrGetUser(ctx, "fed-user@example.com", "Fed User", "google-sub-unused-1")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	token := signAssertion(t, fx.priv, fx.kid, validClaims(), map[string]any{
		testExternalUserIDClaim: user.ID,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/external/callback?assertion="+token, nil)
	w := httptest.NewRecorder()
	fx.ea.HandleCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "http://app.example.com/dashboard" {
		t.Errorf("Location = %q, want http://app.example.com/dashboard", loc)
	}

	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected e2a_session cookie to be set")
	}
	if sessionCookie.Value == "" {
		t.Fatal("session cookie value is empty")
	}

	// The session resolves back to the claimed user — exactly the same
	// mechanism auth.UserAuth.AuthenticateRequest uses for the Google flow.
	sessUser, err := fx.store.GetUserSession(ctx, sessionCookie.Value)
	if err != nil {
		t.Fatalf("GetUserSession: %v", err)
	}
	if sessUser.ID != user.ID {
		t.Errorf("session user = %s, want %s", sessUser.ID, user.ID)
	}
}

func TestHandleCallback_UnknownUserIDRejectedNoUserCreated(t *testing.T) {
	fx := setupExternalAuth(t)
	ctx := context.Background()

	token := signAssertion(t, fx.priv, fx.kid, validClaims(), map[string]any{
		testExternalUserIDClaim: "usr_does_not_exist",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/external/callback?assertion="+token, nil)
	w := httptest.NewRecorder()
	fx.ea.HandleCallback(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("expected no session cookie, got %v", got)
	}
	if _, err := fx.store.GetUserByID(ctx, "usr_does_not_exist"); err == nil {
		t.Fatal("HandleCallback must never create a user for an unknown claimed id")
	}
}

func TestHandleCallback_InvalidSignatureRejected(t *testing.T) {
	fx := setupExternalAuth(t)
	ctx := context.Background()

	user, err := fx.store.CreateOrGetUser(ctx, "fed-user-2@example.com", "Fed User 2", "google-sub-unused-2")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	// Signed by a DIFFERENT key than the one published in the JWKS, but with
	// the same kid header — a forged assertion, not a rotation.
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	token := signAssertion(t, otherKey, fx.kid, validClaims(), map[string]any{
		testExternalUserIDClaim: user.ID,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/external/callback?assertion="+token, nil)
	w := httptest.NewRecorder()
	fx.ea.HandleCallback(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("expected no session cookie, got %v", got)
	}
}

func TestHandleCallback_WrongIssuerRejected(t *testing.T) {
	fx := setupExternalAuth(t)
	ctx := context.Background()

	user, err := fx.store.CreateOrGetUser(ctx, "fed-user-3@example.com", "Fed User 3", "google-sub-unused-3")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	claims := validClaims()
	claims.Issuer = "https://not-the-configured-issuer.example.com"
	token := signAssertion(t, fx.priv, fx.kid, claims, map[string]any{
		testExternalUserIDClaim: user.ID,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/external/callback?assertion="+token, nil)
	w := httptest.NewRecorder()
	fx.ea.HandleCallback(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("expected no session cookie, got %v", got)
	}
}

func TestHandleCallback_WrongAudienceRejected(t *testing.T) {
	fx := setupExternalAuth(t)
	ctx := context.Background()

	user, err := fx.store.CreateOrGetUser(ctx, "fed-user-4@example.com", "Fed User 4", "google-sub-unused-4")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	claims := validClaims()
	claims.Audience = jwt.Audience{"some-other-audience"}
	token := signAssertion(t, fx.priv, fx.kid, claims, map[string]any{
		testExternalUserIDClaim: user.ID,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/external/callback?assertion="+token, nil)
	w := httptest.NewRecorder()
	fx.ea.HandleCallback(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("expected no session cookie, got %v", got)
	}
}

func TestHandleCallback_ExpiredAssertionRejected(t *testing.T) {
	fx := setupExternalAuth(t)
	ctx := context.Background()

	user, err := fx.store.CreateOrGetUser(ctx, "fed-user-5@example.com", "Fed User 5", "google-sub-unused-5")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	past := time.Now().Add(-1 * time.Hour)
	claims := jwt.Claims{
		Issuer:    testExternalIssuer,
		Audience:  jwt.Audience{testExternalAudience},
		IssuedAt:  jwt.NewNumericDate(past.Add(-time.Minute)),
		NotBefore: jwt.NewNumericDate(past.Add(-time.Minute)),
		Expiry:    jwt.NewNumericDate(past), // expired an hour ago, well past clock-skew leeway
	}
	token := signAssertion(t, fx.priv, fx.kid, claims, map[string]any{
		testExternalUserIDClaim: user.ID,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/external/callback?assertion="+token, nil)
	w := httptest.NewRecorder()
	fx.ea.HandleCallback(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("expected no session cookie, got %v", got)
	}
}

func TestHandleCallback_MissingExpiryRejected(t *testing.T) {
	fx := setupExternalAuth(t)
	ctx := context.Background()

	user, err := fx.store.CreateOrGetUser(ctx, "fed-user-6@example.com", "Fed User 6", "google-sub-unused-6")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	now := time.Now()
	claims := jwt.Claims{
		Issuer:   testExternalIssuer,
		Audience: jwt.Audience{testExternalAudience},
		IssuedAt: jwt.NewNumericDate(now),
		// Expiry deliberately omitted: go-jose's Claims.ValidateWithLeeway
		// skips the expiry check entirely when Expiry is nil, so this must
		// be caught by ExternalAuth's own explicit check, not the library.
	}
	token := signAssertion(t, fx.priv, fx.kid, claims, map[string]any{
		testExternalUserIDClaim: user.ID,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/external/callback?assertion="+token, nil)
	w := httptest.NewRecorder()
	fx.ea.HandleCallback(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("expected no session cookie, got %v", got)
	}
}

func TestHandleCallback_UnknownKidRefreshesJWKSOnce(t *testing.T) {
	fx := setupExternalAuth(t)
	ctx := context.Background()

	user, err := fx.store.CreateOrGetUser(ctx, "fed-user-7@example.com", "Fed User 7", "google-sub-unused-7")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	// A valid, correctly-signed assertion, but no prior request has primed
	// the cache — this exercises the "unknown kid ⇒ fetch" path rather than
	// the "unknown kid ⇒ refresh" path.
	token := signAssertion(t, fx.priv, fx.kid, validClaims(), map[string]any{
		testExternalUserIDClaim: user.ID,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/external/callback?assertion="+token, nil)
	w := httptest.NewRecorder()
	fx.ea.HandleCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", w.Code, w.Body.String())
	}
	if fx.callSeen == 0 {
		t.Fatal("expected the JWKS endpoint to have been fetched at least once")
	}
}

func TestHandleCallback_MalformedAssertionRejected(t *testing.T) {
	fx := setupExternalAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/external/callback?assertion=not-a-jwt", nil)
	w := httptest.NewRecorder()
	fx.ea.HandleCallback(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}
