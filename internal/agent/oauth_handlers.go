package agent

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/oauth"
)

// OAuthAuthorizationServerMetadata is the RFC 8414 response shape for
// GET /.well-known/oauth-authorization-server. Field names use snake_case
// per the RFC. Only fields the e2a server actually advertises are listed
// — omitting an OPTIONAL field (e.g. introspection_endpoint, jwks_uri) is
// itself a signal to clients that the feature isn't supported.
type OAuthAuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	RevocationEndpoint                string   `json:"revocation_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string `json:"scopes_supported"`
}

// handleOAuthDiscovery serves the RFC 8414 authorization-server metadata
// document so MCP clients (and any other OAuth 2.1 client) can locate the
// authorize/token/register/revoke endpoints from a single well-known URL.
//
// Unauthenticated by design — RFC 8414 §3 requires the document be
// publicly retrievable. Cache for 1h: values are stable for the lifetime
// of a deployment.
//
// 404 when OAuth isn't configured on this deployment (no oauthStore wired
// in via SetOAuthStore) or when http.public_url isn't set, since the
// metadata MUST contain absolute URLs and we'd rather hide the endpoint
// than emit values derived from request headers (X-Forwarded-Host
// spoofing → issuer confusion).
func (a *API) handleOAuthDiscovery(w http.ResponseWriter, r *http.Request) {
	if a.oauthStore == nil || a.publicURL == "" {
		http.NotFound(w, r)
		return
	}
	base := strings.TrimRight(a.publicURL, "/")
	meta := OAuthAuthorizationServerMetadata{
		Issuer:                            base,
		AuthorizationEndpoint:             base + "/api/oauth/authorize",
		TokenEndpoint:                     base + "/api/oauth/token",
		RegistrationEndpoint:              base + "/api/oauth/register",
		RevocationEndpoint:                base + "/api/oauth/revoke",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
		ScopesSupported:                   []string{"e2a"},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, meta)
}

// ───────────────────────── Dynamic Client Registration (RFC 7591) ─────────────────────────

// OAuthRegisterRequest is the RFC 7591 §2 client metadata POSTed to
// /api/oauth/register. We only accept the fields v0.3 supports —
// unknown fields are tolerated (forward-compat with RFC 7591 extensions)
// but ignored. Public clients only: token_endpoint_auth_method must be
// "none" or omitted.
type OAuthRegisterRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
}

// OAuthRegisterResponse is the RFC 7591 §3.2.1 success response. Echoes
// back the metadata the server actually stored (after defaults applied)
// plus the assigned client_id and issuance timestamp. No client_secret
// is returned — v0.3 supports public clients only.
type OAuthRegisterResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	Scope                   string   `json:"scope"`
}

// OAuthError is the RFC 7591 §3.2.2 / RFC 6749 §5.2 error response.
// Always use this shape for OAuth-endpoint errors (rather than plain
// http.Error) so RFC-compliant clients can parse machine-readable
// error codes instead of free-form strings.
type OAuthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// writeOAuthError sends the RFC-shaped JSON error with the given status.
func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	writeJSON(w, OAuthError{Error: code, ErrorDescription: desc})
}

// validateRedirectURI enforces our redirect_uri allow-list per RFC 8252
// §7 (native apps) and the OAuth 2.1 draft (web). Allowed:
//   - https://… (web apps, must have host)
//   - http://localhost[:port]/… and http://127.0.0.1[:port]/… and
//     http://[::1][:port]/… (loopback for desktop dev; OAuth 2.1 §10.3.3)
//   - custom-scheme://… (native apps registering a private-use URI)
//
// Rejected:
//   - http://anything-non-loopback (would expose codes in transit)
//   - URIs with fragments (RFC 6749 §3.1.2)
//   - URIs without scheme/authority for http(s)
func validateRedirectURI(raw string) error {
	if raw == "" {
		return errOAuthInvalid("redirect_uri cannot be empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errOAuthInvalid("redirect_uri is not a valid URI")
	}
	if u.Fragment != "" {
		return errOAuthInvalid("redirect_uri must not contain a fragment")
	}
	switch u.Scheme {
	case "":
		return errOAuthInvalid("redirect_uri must include a scheme")
	case "https":
		if u.Host == "" {
			return errOAuthInvalid("https redirect_uri must include a host")
		}
		return nil
	case "http":
		// Only loopback is allowed for http://.
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		return errOAuthInvalid("http redirect_uri must use a loopback host (localhost, 127.0.0.1, or ::1)")
	default:
		// Custom scheme (e.g. "myapp://callback"). Must have a non-empty
		// scheme; that's all RFC 8252 §7.1 requires.
		return nil
	}
}

// errOAuthInvalid is a sentinel-like helper to make validation read
// linearly; the caller maps these to HTTP 400 invalid_client_metadata.
type oauthValidationError struct{ msg string }

func (e *oauthValidationError) Error() string { return e.msg }

func errOAuthInvalid(msg string) error { return &oauthValidationError{msg: msg} }

// handleOAuthRegister implements RFC 7591 Dynamic Client Registration.
// Anonymous endpoint (RFC 7591 §2 — "open registration"). Per-IP rate
// limited because anyone on the internet can hit it.
//
// 404 when OAuth isn't configured (parity with discovery).
// 429 when the per-IP limit is exceeded.
// 400 invalid_client_metadata for bad input (RFC 7591 §3.2.2).
// 201 on success with the full registered metadata (RFC 7591 §3.2.1).
func (a *API) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	if a.oauthStore == nil {
		http.NotFound(w, r)
		return
	}
	if !a.dcrLimit.Allow(clientIP(r)) {
		writeOAuthError(w, http.StatusTooManyRequests, "rate_limited", "too many registrations from this IP; try again later")
		return
	}

	var req OAuthRegisterRequest
	if err := readJSON(w, r, &req, maxRequestBytesSmall); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "request body must be JSON")
		return
	}

	if strings.TrimSpace(req.ClientName) == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "client_name is required")
		return
	}
	if len(req.ClientName) > 200 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "client_name must be ≤200 characters")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect_uri is required")
		return
	}
	if len(req.RedirectURIs) > 10 {
		// Soft cap to keep one client from filling a row with hundreds
		// of URIs. Real apps register 1-3.
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "too many redirect_uris (max 10)")
		return
	}
	for _, raw := range req.RedirectURIs {
		if err := validateRedirectURI(raw); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
	}

	// Defaults. RFC 7591 §2 lets the server fill in unspecified
	// metadata; we default to the only combination v0.3 supports.
	if len(req.GrantTypes) == 0 {
		req.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	if len(req.ResponseTypes) == 0 {
		req.ResponseTypes = []string{"code"}
	}
	if req.TokenEndpointAuthMethod == "" {
		req.TokenEndpointAuthMethod = "none"
	}
	if req.Scope == "" {
		req.Scope = "e2a"
	}

	// Capability enforcement — reject anything outside what we actually
	// implement, so a client gets a clear error at registration time
	// rather than a confusing one at /token time.
	for _, gt := range req.GrantTypes {
		if gt != "authorization_code" && gt != "refresh_token" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported grant_type: "+gt)
			return
		}
	}
	for _, rt := range req.ResponseTypes {
		if rt != "code" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported response_type: "+rt)
			return
		}
	}
	if req.TokenEndpointAuthMethod != "none" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata",
			`only token_endpoint_auth_method="none" (public clients with PKCE) is supported in v0.3`)
		return
	}
	if req.Scope != "e2a" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", `only scope="e2a" is supported`)
		return
	}

	client := &oauth.Client{
		ClientID:     oauth.NewClientID(),
		ClientName:   req.ClientName,
		RedirectURIs: req.RedirectURIs,
		ClientType:   "public",
		CreatedVia:   "dcr",
	}
	if err := a.oauthStore.RegisterClient(r.Context(), client); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to register client")
		return
	}
	// Use the handler's wall clock for client_id_issued_at — RegisterClient
	// doesn't read created_at back. The DB's DEFAULT now() will differ by
	// at most milliseconds; this field is informational per RFC 7591 §3.2.1.
	issuedAt := time.Now().Unix()

	resp := OAuthRegisterResponse{
		ClientID:                client.ClientID,
		ClientIDIssuedAt:        issuedAt,
		ClientName:              client.ClientName,
		RedirectURIs:            client.RedirectURIs,
		GrantTypes:              req.GrantTypes,
		ResponseTypes:           req.ResponseTypes,
		TokenEndpointAuthMethod: req.TokenEndpointAuthMethod,
		Scope:                   req.Scope,
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, resp)
}
