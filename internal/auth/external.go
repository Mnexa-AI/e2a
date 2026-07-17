package auth

// This file implements a generic, config-gated external-auth (federated
// JWT) login flow: any OIDC/JWT issuer that can mint a signed assertion
// naming an existing e2a user may hand that assertion to HandleCallback to
// establish a session. Nothing here is specific to any particular identity
// provider — the issuer, JWKS URL, audience, and user-id-claim name are all
// operator-supplied config (config.ExternalAuthConfig). It reuses the exact
// same session primitive the Google OAuth flow uses (store.CreateUserSession
// + the e2a_session cookie, see auth.go) so a session established this way
// is indistinguishable, downstream, from one established via Google.
//
// It is off by default and additive: NewExternalAuth returns nil when the
// feature isn't enabled, and the caller (internal/agent/api.go) registers
// the callback route only when it is non-nil — an unconfigured deployment
// gains no new attack surface. It never creates a user: an assertion naming
// an id with no matching `users` row is rejected (401/403), never
// provisioned.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"

	"github.com/tokencanopy/e2a/internal/config"
	"github.com/tokencanopy/e2a/internal/identity"
)

// externalAuthAssertionParam is the query parameter carrying the signed JWT
// presented to HandleCallback. Named explicitly (rather than a generic
// "token") since it plays the role of a SAML/JWT-bearer "assertion" —
// something a third party vouches for, not a credential this server issued.
const externalAuthAssertionParam = "assertion"

// externalAuthSigningAlg is the only JWS algorithm HandleCallback accepts
// from an external issuer. Pinning to RS256 rather than trusting whatever
// `alg` header a token happens to carry closes the classic alg-confusion
// class of attack (e.g. an attacker crafting an HS256 token "signed" with
// the issuer's own RSA *public* key, treating it as an HMAC secret) — the
// same posture internal/agentauth.VerifyToken takes for e2a's own tokens.
const externalAuthSigningAlg = "RS256"

// externalAuthClockSkew tolerates small clock drift between the issuer and
// this server when validating exp/nbf/iat, mirroring agentauth's verifier.
const externalAuthClockSkew = 60 * time.Second

// ExternalAuth holds the wiring for the external-auth callback handler. The
// zero value is never used directly — always construct with NewExternalAuth,
// which returns nil when the feature is disabled.
type ExternalAuth struct {
	cfg     config.ExternalAuthConfig
	store   *identity.Store
	secure  bool   // true in production (Secure cookie flag), mirrors auth.UserAuth
	baseURL string // frontend origin for the post-login redirect; empty ⇒ relative redirect
	jwks    *externalJWKSCache
}

// NewExternalAuth builds an ExternalAuth from cfg. Returns nil when the
// feature is disabled (cfg.Enabled == false) — callers must check for nil
// and, when nil, must not register the callback route at all (rather than
// registering a handler that always 404s), so the route is genuinely absent
// from the mux, not merely non-functional.
//
// cfg is assumed already validated (config.Config.Validate enforces that
// Issuer/JWKSURL/Audience/UserIDClaim are non-empty whenever Enabled is
// true) — NewExternalAuth does not re-validate.
func NewExternalAuth(cfg config.ExternalAuthConfig, store *identity.Store, production bool, baseURL string) *ExternalAuth {
	if !cfg.Enabled {
		return nil
	}
	return &ExternalAuth{
		cfg:     cfg,
		store:   store,
		secure:  production,
		baseURL: baseURL,
		jwks:    newExternalJWKSCache(cfg.JWKSURL, http.DefaultClient),
	}
}

// HandleCallback verifies a signed JWT assertion from the configured
// external issuer and, if valid, establishes an e2a session for the
// existing user named by the configured user-id claim.
//
// Verification (all required, in order): signature against a key in the
// issuer's JWKS selected by the assertion's `kid` header; `iss` equals the
// configured issuer; `aud` contains the configured audience; `exp` is
// present and not expired. On any failure the request is rejected with no
// session created and no information about which check failed leaked to
// the response body (the reason is logged server-side only).
func (ea *ExternalAuth) HandleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	assertion := r.URL.Query().Get(externalAuthAssertionParam)
	if assertion == "" {
		http.Error(w, "missing assertion", http.StatusBadRequest)
		return
	}

	userID, err := ea.verifyAssertion(ctx, assertion)
	if err != nil {
		log.Printf("[auth] external auth: rejected assertion: %v", err)
		http.Error(w, "invalid assertion", http.StatusUnauthorized)
		return
	}

	// Load the claimed user. This NEVER creates a user — an assertion
	// naming an id with no matching row is a hard rejection, not an
	// implicit signup (unlike the Google flow's CreateOrGetUser).
	user, err := ea.store.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		log.Printf("[auth] external auth: assertion named unknown user id %q", userID)
		http.Error(w, "user not found", http.StatusForbidden)
		return
	}

	sessionToken, err := ea.store.CreateUserSession(ctx, user.ID)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	ea.setSessionCookie(w, sessionToken)
	http.Redirect(w, r, ea.baseURL+"/dashboard", http.StatusFound)
}

// setSessionCookie sets the same e2a_session cookie the Google flow sets
// (name, path, flags, max-age) — the session mechanism is shared; only how
// the session gets established differs. Kept as a small local copy of
// auth.go's UserAuth.setCookie (rather than a shared refactor) so the
// existing, already-tested Google callback path is untouched by this
// addition.
func (ea *ExternalAuth) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   ea.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionMaxAge.Seconds()),
	})
}

// verifyAssertion runs the full verification chain and, on success, returns
// the raw value of the configured user-id claim (not yet resolved against
// the users table — that's the caller's job).
func (ea *ExternalAuth) verifyAssertion(ctx context.Context, assertion string) (string, error) {
	parsed, err := jwt.ParseSigned(assertion)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	if len(parsed.Headers) != 1 || parsed.Headers[0].Algorithm != externalAuthSigningAlg {
		return "", fmt.Errorf("unexpected JWS alg")
	}
	kid := parsed.Headers[0].KeyID
	if kid == "" {
		return "", fmt.Errorf("assertion missing kid")
	}

	key, err := ea.jwks.key(ctx, kid)
	if err != nil {
		return "", fmt.Errorf("resolve signing key: %w", err)
	}

	var std jwt.Claims
	var private map[string]any
	if err := parsed.Claims(key.Key, &std, &private); err != nil {
		return "", fmt.Errorf("signature: %w", err)
	}

	// jwt.Claims.ValidateWithLeeway silently SKIPS the expiry check when the
	// token carries no `exp` claim at all (nil Expiry ⇒ "not required by the
	// caller"). That's the right default for a library, but wrong for us:
	// the spec requires exp to be checked, so a token with no exp must be
	// rejected outright rather than treated as non-expiring.
	if std.Expiry == nil {
		return "", fmt.Errorf("assertion missing exp claim")
	}
	if err := std.ValidateWithLeeway(jwt.Expected{
		Issuer:   ea.cfg.Issuer,
		Audience: jwt.Audience{ea.cfg.Audience},
		Time:     time.Now(),
	}, externalAuthClockSkew); err != nil {
		return "", fmt.Errorf("claims: %w", err)
	}

	raw, ok := private[ea.cfg.UserIDClaim]
	if !ok {
		return "", fmt.Errorf("assertion missing claim %q", ea.cfg.UserIDClaim)
	}
	userID, ok := raw.(string)
	if !ok || userID == "" {
		return "", fmt.Errorf("claim %q is not a non-empty string", ea.cfg.UserIDClaim)
	}
	return userID, nil
}

// externalJWKSMinRefreshInterval rate-limits JWKS re-fetches triggered by an
// unknown kid, so a client that repeatedly presents a bogus kid can't be
// used to flood the issuer's JWKS endpoint through this server.
const externalJWKSMinRefreshInterval = 5 * time.Second

// externalJWKSMaxBodyBytes caps how much of the JWKS response this server
// will buffer. A real JWKS is a handful of KB; refusing to read past this
// bound protects against a misbehaving or malicious endpoint streaming an
// unbounded body.
const externalJWKSMaxBodyBytes = 1 << 20 // 1 MiB

// externalJWKSCache fetches and caches a remote JWKS, resolving a key by its
// `kid`. On a cache miss it refetches once (rate-limited by
// externalJWKSMinRefreshInterval) to pick up a key the issuer rotated in
// since the last fetch. Safe for concurrent use.
type externalJWKSCache struct {
	url    string
	client *http.Client

	mu        sync.Mutex
	keys      jose.JSONWebKeySet
	fetchedAt time.Time
}

func newExternalJWKSCache(jwksURL string, client *http.Client) *externalJWKSCache {
	if client == nil {
		client = http.DefaultClient
	}
	return &externalJWKSCache{url: jwksURL, client: client}
}

// key returns the JWK with the given kid, fetching (or, on a cache miss,
// refreshing) the JWKS as needed.
func (c *externalJWKSCache) key(ctx context.Context, kid string) (jose.JSONWebKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if k, ok := lookupKID(c.keys, kid); ok {
		return k, nil
	}

	if !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < externalJWKSMinRefreshInterval {
		return jose.JSONWebKey{}, fmt.Errorf("unknown kid %q (jwks last refreshed %s ago, not retrying yet)", kid, time.Since(c.fetchedAt).Round(time.Millisecond))
	}
	if err := c.fetchLocked(ctx); err != nil {
		return jose.JSONWebKey{}, err
	}
	if k, ok := lookupKID(c.keys, kid); ok {
		return k, nil
	}
	return jose.JSONWebKey{}, fmt.Errorf("unknown kid %q after jwks refresh", kid)
}

func lookupKID(set jose.JSONWebKeySet, kid string) (jose.JSONWebKey, bool) {
	for _, k := range set.Keys {
		if k.KeyID == kid {
			return k, true
		}
	}
	return jose.JSONWebKey{}, false
}

func (c *externalJWKSCache) fetchLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("build jwks request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks endpoint returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, externalJWKSMaxBodyBytes))
	if err != nil {
		return fmt.Errorf("read jwks response: %w", err)
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("parse jwks response: %w", err)
	}
	c.keys = set
	c.fetchedAt = time.Now()
	return nil
}
