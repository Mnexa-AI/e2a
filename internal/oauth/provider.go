package oauth

import (
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	enigma "github.com/ory/fosite/token/hmac"
)

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
// hmacSecret is the global signing key for fosite's HMAC token
// strategy. Must be ≥32 bytes; fosite panics otherwise on the first
// Generate call. We reuse the cfg.Signing.HMACSecret already used
// elsewhere in the server so operators don't have to manage two keys.
//
// issuerURL is the canonical issuer used in:
//   - the discovery doc
//   - the RFC 9207 `iss` parameter on authorize responses
//   - future JWT `iss` claims if we ever switch from opaque to JWT
//
// Must match what clients fetch via /.well-known. Operators MUST set
// http.public_url; we read it once at startup, no per-request override.
func NewProvider(storage *Storage, issuerURL string, hmacSecret []byte) fosite.OAuth2Provider {
	cfg := &fosite.Config{
		// Token / code lifetimes — fosite writes these into the session's
		// ExpiresAt map before persistence; our storage adapter reads
		// them back out as the row's expires_at column.
		AccessTokenLifespan:   AccessTokenLifespan,
		RefreshTokenLifespan:  RefreshTokenLifespan,
		AuthorizeCodeLifespan: AuthorizeCodeLifespan,

		// HMAC signing material. fosite requires ≥32 bytes; if the
		// operator's HMACSecret is shorter, fosite would panic on the
		// first Generate. We don't expand short keys here — the bug
		// belongs in the config layer where the value is set.
		GlobalSecret:         hmacSecret,
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

		// Scope matching: e2a's single scope is "mcp"; ExactScope means
		// the requested scope must literally equal a registered scope.
		// HierarchicScope (the alternative) would let "mcp:inbox" match
		// "mcp" as a parent — we don't want that drift today.
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
		compose.OAuth2AuthorizeExplicitFactory, // authorize-code grant
		compose.OAuth2RefreshTokenGrantFactory, // refresh-token grant
		compose.OAuth2PKCEFactory,              // PKCE (S256-only here)
		compose.OAuth2TokenRevocationFactory,   // RFC 7009 /revoke
	)
}
