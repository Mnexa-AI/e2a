package oauth

import (
	"crypto/sha256"
	"fmt"
	"io"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	enigma "github.com/ory/fosite/token/hmac"
	"golang.org/x/crypto/hkdf"
)

// oauthSigningKeyLabel is the HKDF info string that scopes the derived
// subkey to OAuth-token signing. Bumping the version suffix lets us
// rotate the signing material (invalidating every outstanding access /
// refresh / authorize code) without changing the master secret used
// elsewhere in the server.
const oauthSigningKeyLabel = "e2a-oauth-token-signing-v1"

// deriveOAuthSigningKey produces a 32-byte HMAC key dedicated to fosite
// from the master secret used for X-E2A-Auth-* header signing. Key
// separation matters here: the master also signs HITL approval links
// and email headers, and rotating one signing domain shouldn't tear
// down the other two. HKDF-SHA256 with a per-purpose `info` label
// gives us a stable, deterministic per-domain key without any extra
// config knob.
//
// The 32-byte length matches fosite's HMACSHA512/256 minimum
// (token/hmac/hmacsha.go: minimumSecretLength). The master length is
// re-validated here because the config layer only enforces ≥32 in
// production mode — a short dev secret would otherwise reach fosite
// and panic at the first Generate.
func deriveOAuthSigningKey(master []byte) ([]byte, error) {
	if len(master) < 32 {
		return nil, fmt.Errorf("oauth: master hmac secret is %d bytes, need ≥32", len(master))
	}
	kdf := hkdf.New(sha256.New, master, nil, []byte(oauthSigningKeyLabel))
	out := make([]byte, 32)
	if _, err := io.ReadFull(kdf, out); err != nil {
		return nil, fmt.Errorf("oauth: derive signing key: %w", err)
	}
	return out, nil
}

// Token / code lifetimes. Exported as constants so handler code that
// needs to write them onto the session before persistence can stay in
// lockstep with fosite's strategy expectations.
const (
	AccessTokenLifespan   = time.Hour
	RefreshTokenLifespan  = 30 * 24 * time.Hour
	AuthorizeCodeLifespan = time.Minute
)

// NewProvider constructs the fosite.OAuth2Provider that backs every
// OAuth endpoint in this server. Wires:
//
//   - the e2a-prefixed HMAC strategy (ate2a_ / rte2a_ / oace_ tokens)
//   - the four grant handlers we use (auth code, refresh, PKCE, revoke)
//   - lifespans matching the legacy hand-rolled backend
//   - PKCE-S256 mandatory (no plain), public clients only at /token
//
// hmacSecret is the *master* signing key (typically
// cfg.Signing.HMACSecret) — same one X-E2A-Auth-* email headers and
// HITL magic links sign with. We never hand fosite the master
// directly; deriveOAuthSigningKey produces a per-purpose 32-byte
// subkey via HKDF-SHA256, so a future rotation of the OAuth signing
// material (bump the label suffix to v2) doesn't churn the other
// signing domains and vice versa. Returns an error rather than
// panicking if the master is shorter than 32 bytes — operators get
// a loud failure at startup instead of a confusing fosite panic on
// the first /token request.
//
// issuerURL is the canonical issuer used in:
//   - the discovery doc
//   - the RFC 9207 `iss` parameter on authorize responses
//   - future JWT `iss` claims if we ever switch from opaque to JWT
//
// Must match what clients fetch via /.well-known. Operators MUST set
// http.public_url; we read it once at startup, no per-request override.
func NewProvider(storage *Storage, issuerURL string, hmacSecret []byte) (fosite.OAuth2Provider, error) {
	oauthSecret, err := deriveOAuthSigningKey(hmacSecret)
	if err != nil {
		return nil, err
	}
	cfg := &fosite.Config{
		// Token / code lifetimes — fosite writes these into the session's
		// ExpiresAt map before persistence; our storage adapter reads
		// them back out as the row's expires_at column.
		AccessTokenLifespan:   AccessTokenLifespan,
		RefreshTokenLifespan:  RefreshTokenLifespan,
		AuthorizeCodeLifespan: AuthorizeCodeLifespan,

		// HMAC signing material. fosite gets the HKDF-derived subkey,
		// never the master. deriveOAuthSigningKey already validated
		// length above, so by the time we get here oauthSecret is
		// guaranteed to be a fixed 32-byte slice.
		GlobalSecret:         oauthSecret,
		RotatedGlobalSecrets: nil,

		// PKCE-S256 mandatory. EnforcePKCE=true makes fosite reject any
		// authorize request without code_challenge; EnforcePKCEForPublic
		// is redundant under EnforcePKCE but documents intent for the
		// public-clients-only posture. EnablePKCEPlainChallengeMethod=
		// false rejects code_challenge_method=plain (we only advertise
		// S256 in discovery; this enforces it at the protocol layer).
		EnforcePKCE:                    true,
		EnforcePKCEForPublicClients:    true,
		EnablePKCEPlainChallengeMethod: false,

		// Scope matching: e2a's scopes are "agent" (runtime/inbox tier, the
		// DCR-public default) and "account" (admin). ExactScope means a
		// requested scope must literally equal one the client registered;
		// HierarchicScope (the alternative) would let "agent:inbox" match
		// "agent" as a parent — we don't want that drift today.
		ScopeStrategy:            fosite.ExactScopeStrategy,
		AudienceMatchingStrategy: fosite.DefaultAudienceMatchingStrategy,

		// Issue refresh tokens on EVERY authorize-code exchange. fosite's
		// default ("offline" / "offline_access" required in granted
		// scopes) is an OIDC convention; for our pure-OAuth MCP use
		// case we want refresh on every grant. Empty list = always.
		RefreshTokenScopes: []string{},

		// Issuer for RFC 9207 (iss param on authorize response) and any
		// future JWT iss claim. The discovery doc returns the same
		// string; centralizing here means a deployment can't drift
		// between what discovery advertises and what tokens carry.
		IDTokenIssuer: issuerURL,

		// Don't leak server-side error context into client-facing
		// error_description fields. fosite's default is true but for a
		// public-facing AS we want the redacted output.
		SendDebugMessagesToClients: false,
	}

	hmac := &enigma.HMACStrategy{Config: cfg}
	strategy := newPrefixedStrategy(hmac, cfg)

	return compose.Compose(
		cfg,
		storage,
		strategy,
		// The four grant flows we support. Everything else
		// (implicit, client_credentials, resource owner password,
		// JWT bearer, OIDC, introspection, PAR) is intentionally
		// not wired. fosite returns "unsupported_grant_type" /
		// "unsupported_response_type" for those by default.
		compose.OAuth2AuthorizeExplicitFactory,    // authorize-code grant
		compose.OAuth2RefreshTokenGrantFactory,    // refresh-token grant
		compose.OAuth2PKCEFactory,                 // PKCE (S256-only here)
		compose.OAuth2TokenRevocationFactory,      // RFC 7009 /revoke endpoint
		// Token-introspection HANDLER (not endpoint). Wires the
		// CoreValidator that authenticateUser's bearer dispatch uses
		// via provider.IntrospectToken(). We don't expose the RFC
		// 7662 /introspect endpoint — adding the factory only
		// registers the in-process validator.
		compose.OAuth2TokenIntrospectionFactory,
	), nil
}
