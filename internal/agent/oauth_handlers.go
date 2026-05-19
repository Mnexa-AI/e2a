package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
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

// ───────────────────────── Authorize + Consent (RFC 6749 §4.1) ─────────────────────────

// oauthAuthorizeParams is the parsed + validated form of an
// authorization request. The same struct is used for both GET /authorize
// (params in query string) and the re-passed hidden form values in
// POST /consent — re-parsing on POST defends against client_id / scope
// tampering between the two requests.
type oauthAuthorizeParams struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Scope               string
	State               string
}

// pkceChallengePattern enforces the RFC 7636 §4.2 char set: unreserved
// (ALPHA / DIGIT / "-" / "." / "_" / "~"). Length 43–128 since the
// S256 method always produces a 43-char base64url-encoded SHA-256
// digest, but RFC allows up to 128 to leave room for future methods.
var pkceChallengePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// parseAuthorizeParams pulls the OAuth params from r.Form (which works
// for both query strings on GET and form bodies on POST after
// ParseForm). Returns *oauthAuthorizeParams + nil on success.
func parseAuthorizeParams(values url.Values) *oauthAuthorizeParams {
	return &oauthAuthorizeParams{
		ResponseType:        strings.TrimSpace(values.Get("response_type")),
		ClientID:            strings.TrimSpace(values.Get("client_id")),
		RedirectURI:         strings.TrimSpace(values.Get("redirect_uri")),
		CodeChallenge:       strings.TrimSpace(values.Get("code_challenge")),
		CodeChallengeMethod: strings.TrimSpace(values.Get("code_challenge_method")),
		Scope:               strings.TrimSpace(values.Get("scope")),
		State:               values.Get("state"),
	}
}

// validateAuthorizeParamsShape checks the params we can validate without
// hitting the DB. Used before the redirect_uri is known to be safe.
// Returns a descriptive error suitable for a 400 response — RFC 6749
// §4.1.2.1 requires we *not* redirect to an unverified URI, so these
// must surface as direct HTTP errors, not as redirect-with-error.
func validateAuthorizeParamsShape(p *oauthAuthorizeParams) error {
	if p.ClientID == "" {
		return errors.New("client_id is required")
	}
	if p.RedirectURI == "" {
		return errors.New("redirect_uri is required")
	}
	if err := validateRedirectURI(p.RedirectURI); err != nil {
		return err
	}
	return nil
}

// validateAuthorizeParamsLogical checks params that, if invalid, are
// safe to surface via redirect-with-error (because the redirect_uri
// already verified-good). Per RFC 6749 §4.1.2.1.
func validateAuthorizeParamsLogical(p *oauthAuthorizeParams) (errCode, errDesc string) {
	if p.ResponseType != "code" {
		return "unsupported_response_type", "response_type must be 'code'"
	}
	if p.CodeChallenge == "" {
		return "invalid_request", "code_challenge is required (PKCE mandatory)"
	}
	if !pkceChallengePattern.MatchString(p.CodeChallenge) {
		return "invalid_request", "code_challenge format invalid (must be unreserved-chars, 43–128)"
	}
	if p.CodeChallengeMethod != "" && p.CodeChallengeMethod != "S256" {
		return "invalid_request", "code_challenge_method must be 'S256'"
	}
	// Scope: only "e2a" supported. Empty defaults to "e2a".
	if p.Scope != "" && p.Scope != "e2a" {
		return "invalid_scope", `only scope "e2a" is supported`
	}
	return "", ""
}

// redirectWithOAuthError 302s the user-agent back to redirect_uri with
// an error code in the query, per RFC 6749 §4.1.2.1. Use this only when
// redirect_uri has been validated against the registered client.
func redirectWithOAuthError(w http.ResponseWriter, r *http.Request, redirectURI, errCode, state string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		// Should be unreachable — caller has already validated.
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("error", errCode)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// redirectMatchesRegistered does an exact-string match per OAuth 2.1
// guidance. No prefix matching, no scheme-only matching — every
// registered URI is enumerated explicitly.
func redirectMatchesRegistered(want string, registered []string) bool {
	for _, r := range registered {
		if r == want {
			return true
		}
	}
	return false
}

// handleOAuthAuthorize is the authorization endpoint (RFC 6749 §4.1.1).
//
// Flow:
//  1. Parse + shape-validate query params. If client_id / redirect_uri
//     are bad → direct 400 (cannot safely redirect).
//  2. Load client and verify redirect_uri is in its registered set.
//     If not → direct 400.
//  3. Logically validate remaining params. If any fails → redirect to
//     the now-known-safe redirect_uri with error=… &state=… (§4.1.2.1).
//  4. Check session cookie. If absent → 302 to /api/auth/login so the
//     user signs in with Google. (Return-to-authorize after login lands
//     in v0.3 PR B; for now the user re-launches the MCP flow.)
//  5. If session present → 302 to {publicURL}/oauth/consent with the
//     authorize params encoded as query string. The web app renders
//     the consent UI and POSTs back to /api/oauth/consent.
func (a *API) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if a.oauthStore == nil {
		http.NotFound(w, r)
		return
	}
	if a.publicURL == "" {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "OAuth flow not configured: http.public_url is unset")
		return
	}

	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "could not parse query string")
		return
	}
	p := parseAuthorizeParams(r.Form)

	if err := validateAuthorizeParamsShape(p); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	client, err := a.oauthStore.GetClient(r.Context(), p.ClientID)
	if err != nil {
		// Per RFC: client_id mismatch is a direct error, not a redirect.
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "unknown client_id")
		return
	}
	if !redirectMatchesRegistered(p.RedirectURI, client.RedirectURIs) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri does not match any registered URI")
		return
	}

	if errCode, errDesc := validateAuthorizeParamsLogical(p); errCode != "" {
		_ = errDesc // logged via the OAuth error code; description is descriptive only
		redirectWithOAuthError(w, r, p.RedirectURI, errCode, p.State)
		return
	}

	// Session check. Without userAuth wired we can't authenticate the
	// browser — fail closed.
	if a.userAuth == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "user auth not configured on this deployment")
		return
	}
	if user := a.userAuth.AuthenticateRequest(r); user == nil {
		// PR B will add return_to here. For now: kick to login so the
		// user at least lands on a working sign-in page.
		http.Redirect(w, r, strings.TrimRight(a.publicURL, "/")+"/api/auth/login", http.StatusFound)
		return
	}

	// 302 to consent UI (web/), passing all params through. The consent
	// page itself reads /api/auth/me + /api/dashboard/agents to populate
	// the agent dropdown and pre-fill the suggested slug.
	consentURL, _ := url.Parse(strings.TrimRight(a.publicURL, "/") + "/oauth/consent")
	q := consentURL.Query()
	q.Set("response_type", p.ResponseType)
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("code_challenge", p.CodeChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", "e2a")
	if p.State != "" {
		q.Set("state", p.State)
	}
	consentURL.RawQuery = q.Encode()
	http.Redirect(w, r, consentURL.String(), http.StatusFound)
}

// generateSlugSuffix returns a 6-hex-char nanoid-style suffix used to
// disambiguate auto-generated agent slugs (e.g. "claude-code-a1b2c3").
// 24 bits is plenty given the slug uniqueness constraint is per shared
// domain and collisions only need to be statistically negligible during
// a single retry window.
func generateSlugSuffix() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("oauth: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// slugifyClientName lowercases and sanitizes a client name into a
// slug-safe prefix. Falls back to "agent" when the name produces an
// empty slug (all punctuation / non-ASCII).
func slugifyClientName(name string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			// Collapse any non-alnum into single hyphen; skip leading hyphens
			if b.Len() > 0 && !prevHyphen {
				b.WriteRune('-')
				prevHyphen = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		out = "agent"
	}
	// Cap the prefix so the final "{prefix}-{6hex}" stays under the
	// 40-char slug limit (slug rule: 2–40 chars). 30 + 1 + 6 = 37.
	if len(out) > 30 {
		out = strings.TrimRight(out[:30], "-")
	}
	return out
}

// generateDefaultAgentSlug returns "{slug(client_name)}-{6hex}" — the
// pre-populated default we'd suggest if the user clicks "Create new
// inbox" without typing anything. Users can override entirely.
func generateDefaultAgentSlug(clientName string) string {
	return slugifyClientName(clientName) + "-" + generateSlugSuffix()
}

// handleOAuthConsent processes the consent form (RFC 6749 §4.1.2). The
// form is POSTed by the web consent UI; we re-validate the OAuth params
// (anti-tamper), check the user's session, then either redirect with
// an authorization code (action=allow) or with error=access_denied
// (action=deny).
//
// On allow + agent_choice=create_new the agent is created in the same
// request so the issued code can already carry a valid agent_email —
// downstream tool calls don't need to handle "code valid but agent
// missing" as a separate case.
func (a *API) handleOAuthConsent(w http.ResponseWriter, r *http.Request) {
	if a.oauthStore == nil {
		http.NotFound(w, r)
		return
	}
	if a.userAuth == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "user auth not configured on this deployment")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "could not parse form body")
		return
	}
	p := parseAuthorizeParams(r.Form)

	// Shape + client_id + redirect_uri match. Same chain as /authorize
	// — we don't trust the form was honestly re-passed.
	if err := validateAuthorizeParamsShape(p); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	client, err := a.oauthStore.GetClient(r.Context(), p.ClientID)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "unknown client_id")
		return
	}
	if !redirectMatchesRegistered(p.RedirectURI, client.RedirectURIs) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri does not match any registered URI")
		return
	}
	if errCode, errDesc := validateAuthorizeParamsLogical(p); errCode != "" {
		_ = errDesc
		redirectWithOAuthError(w, r, p.RedirectURI, errCode, p.State)
		return
	}

	user := a.userAuth.AuthenticateRequest(r)
	if user == nil {
		writeOAuthError(w, http.StatusUnauthorized, "access_denied", "session required — log in before consenting")
		return
	}

	action := r.PostFormValue("action")
	if action == "deny" {
		redirectWithOAuthError(w, r, p.RedirectURI, "access_denied", p.State)
		return
	}
	if action != "allow" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "action must be 'allow' or 'deny'")
		return
	}

	agentChoice := r.PostFormValue("agent_choice")
	var agentEmail string
	switch {
	case strings.HasPrefix(agentChoice, "existing:"):
		email := strings.TrimPrefix(agentChoice, "existing:")
		agent, err := a.store.GetAgentByEmail(r.Context(), email)
		if err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "chosen agent does not exist")
			return
		}
		if agent.UserID != user.ID {
			writeOAuthError(w, http.StatusForbidden, "access_denied", "you do not own that agent")
			return
		}
		agentEmail = email

	case agentChoice == "create_new":
		if a.sharedDomain == "" {
			writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "shared-domain auto-create is not configured")
			return
		}
		slug := strings.TrimSpace(r.PostFormValue("new_agent_slug"))
		if slug == "" {
			// Default — used when web/ submits with the placeholder
			// unmodified. We re-resolve client.ClientName here (rather
			// than trusting the form) so a tampered form can't poison
			// the slug.
			slug = generateDefaultAgentSlug(client.ClientName)
		}
		if err := validateSlug(slug); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid slug: "+err.Error())
			return
		}
		agentEmail = slug + "@" + a.sharedDomain
		// Local-mode agent (no webhook). PR B's UI will let the user
		// switch this later.
		if _, err := a.store.CreateAgent(r.Context(), agentEmail, a.sharedDomain, "", "", "local", user.ID); err != nil {
			if strings.Contains(err.Error(), "duplicate") {
				writeOAuthError(w, http.StatusConflict, "invalid_request", "that slug is already taken — pick another")
				return
			}
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to create agent")
			return
		}

	default:
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "agent_choice must be 'existing:<email>' or 'create_new'")
		return
	}

	// Mint the authorization code. AuthCodeLifetime is 60s — the client
	// must POST /api/oauth/token immediately.
	authCode := &oauth.AuthorizationCode{
		Code:                oauth.NewAuthCode(),
		ClientID:            p.ClientID,
		UserID:              user.ID,
		AgentEmail:          agentEmail,
		RedirectURI:         p.RedirectURI,
		CodeChallenge:       p.CodeChallenge,
		CodeChallengeMethod: "S256",
		Scope:               "e2a",
		ExpiresAt:           time.Now().Add(oauth.AuthCodeLifetime),
	}
	if err := a.oauthStore.IssueCode(r.Context(), authCode); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to issue authorization code")
		return
	}

	redirectURL, _ := url.Parse(p.RedirectURI)
	q := redirectURL.Query()
	q.Set("code", authCode.Code)
	if p.State != "" {
		q.Set("state", p.State)
	}
	redirectURL.RawQuery = q.Encode()
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

// ───────────────────────── Token endpoint (RFC 6749 §4.1.3, §6) ─────────────────────────

// OAuthTokenResponse is the success body for /api/oauth/token. Field
// names follow RFC 6749 §5.1 verbatim.
type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"` // always "Bearer"
	ExpiresIn    int    `json:"expires_in"` // seconds; access-token lifetime
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// pkceVerifierPattern enforces RFC 7636 §4.1 — same unreserved char set
// as the challenge, length 43–128.
var pkceVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// verifyPKCE_S256 confirms BASE64URL-NO-PAD(SHA256(verifier)) ==
// challenge using a constant-time comparison so timing channels can't
// be used to brute-force the challenge.
func verifyPKCE_S256(verifier, challenge string) bool {
	if !pkceVerifierPattern.MatchString(verifier) {
		return false
	}
	h := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// writeTokenResponse emits the RFC 6749 §5.1 success body, including
// the no-store cache directive required by §5.1 (so caches and dev
// tools don't accidentally persist the access token).
func writeTokenResponse(w http.ResponseWriter, t *oauth.Token) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, OAuthTokenResponse{
		AccessToken:  t.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(oauth.AccessTokenLifetime / time.Second),
		RefreshToken: t.RefreshToken,
		Scope:        t.Scope,
	})
}

// handleOAuthToken dispatches by grant_type. Both grant types are
// public-client only (no client_secret); identity is bound via PKCE on
// the auth_code grant and via possession of the refresh on the refresh
// grant.
//
// Per RFC 6749 §3.2, the request MUST be application/x-www-form-urlencoded.
func (a *API) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if a.oauthStore == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "could not parse form body")
		return
	}
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		a.handleTokenAuthCode(w, r)
	case "refresh_token":
		a.handleTokenRefresh(w, r)
	case "":
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "grant_type is required")
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"only authorization_code and refresh_token grants are supported")
	}
}

// handleTokenAuthCode implements the authorization_code grant (RFC 6749 §4.1.3).
// Order matters:
//  1. Atomically consume the code. A replay (already-consumed) triggers
//     RFC 6749 §10.5 defense — revoke all tokens for the same
//     (client_id, user_id) pair and return invalid_grant.
//  2. Bind: client_id and redirect_uri on the request must match the
//     code's recorded values.
//  3. PKCE: verify code_verifier ⇒ recomputed challenge matches.
//  4. Issue tokens (access + refresh) with a fresh refresh_chain_id.
func (a *API) handleTokenAuthCode(w http.ResponseWriter, r *http.Request) {
	code := r.PostFormValue("code")
	redirectURI := r.PostFormValue("redirect_uri")
	clientID := r.PostFormValue("client_id")
	codeVerifier := r.PostFormValue("code_verifier")

	if code == "" || redirectURI == "" || clientID == "" || codeVerifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"code, redirect_uri, client_id, and code_verifier are required")
		return
	}

	authCode, state, err := a.oauthStore.AtomicConsumeCode(r.Context(), code)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "code consume failed")
		return
	}
	switch state {
	case oauth.ConsumeFresh:
		// proceed
	case oauth.ConsumeAlreadyConsumed:
		// RFC 6749 §10.5 — revoke every token issued via this (client,
		// user) pair. We have the row even though it was already
		// consumed; use its client_id/user_id, not the request's, so a
		// replayer can't redirect the revocation to a different account.
		if authCode != nil {
			_, _ = a.oauthStore.RevokeAllByClientUser(r.Context(), authCode.ClientID, authCode.UserID)
		}
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
			"authorization code already used; associated tokens revoked")
		return
	case oauth.ConsumeExpired:
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code expired")
		return
	case oauth.ConsumeNotFound:
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "unknown authorization code")
		return
	}

	if authCode.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id does not match the code")
		return
	}
	if authCode.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the code")
		return
	}
	if !verifyPKCE_S256(codeVerifier, authCode.CodeChallenge) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match code_challenge")
		return
	}

	now := time.Now()
	refreshExp := now.Add(oauth.RefreshTokenLifetime)
	tok := &oauth.Token{
		AccessToken:      oauth.NewAccessToken(),
		RefreshToken:     oauth.NewRefreshToken(),
		RefreshChainID:   oauth.NewChainID(),
		ClientID:         authCode.ClientID,
		UserID:           authCode.UserID,
		AgentEmail:       authCode.AgentEmail,
		Scope:            authCode.Scope,
		ExpiresAt:        now.Add(oauth.AccessTokenLifetime),
		RefreshExpiresAt: &refreshExp,
	}
	if err := a.oauthStore.IssueToken(r.Context(), tok); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to issue tokens")
		return
	}
	writeTokenResponse(w, tok)
}

// handleTokenRefresh implements the refresh_token grant (RFC 6749 §6)
// with rotation per OAuth 2.1 / §10.4 reuse defense.
//
// Behavior:
//  1. Look up the refresh. If not found at all, return invalid_grant
//     — we can't distinguish "never existed" from "already rotated";
//     full chain-level reuse defense on rotated rows is tracked as a
//     follow-up (would require schema work to keep rotated values
//     indexed). Rotation still provides single-use defense.
//  2. If found but revoked or refresh-expired: revoke the entire
//     chain (§10.4) and return invalid_grant.
//  3. If client_id mismatch: revoke chain and return invalid_grant
//     — an unexpected client presenting this refresh is malicious.
//  4. Atomically rotate: NULL the old refresh, INSERT the new row
//     under the same chain_id.
func (a *API) handleTokenRefresh(w http.ResponseWriter, r *http.Request) {
	refreshToken := r.PostFormValue("refresh_token")
	clientID := r.PostFormValue("client_id")
	if refreshToken == "" || clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"refresh_token and client_id are required")
		return
	}

	oldTok, err := a.oauthStore.LookupTokenByRefresh(r.Context(), refreshToken)
	if err != nil {
		// Either never existed or already rotated. Both surface as
		// invalid_grant; chain-level revocation on the rotated case
		// requires schema support not yet present.
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token invalid")
		return
	}

	// client_id binding (§10.4). Treat mismatch as hostile and burn
	// the chain — a different client presenting this refresh means
	// either token confusion at the client or theft.
	if oldTok.ClientID != clientID {
		_, _ = a.oauthStore.RevokeChainByID(r.Context(), oldTok.RefreshChainID)
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id does not match the refresh token")
		return
	}

	now := time.Now()
	expired := oldTok.RefreshExpiresAt != nil && now.After(*oldTok.RefreshExpiresAt)
	if oldTok.RevokedAt != nil || expired {
		// Presenting a refresh that's revoked or past TTL is suspect.
		// Revoke the chain so any sibling tokens are torn down too.
		_, _ = a.oauthStore.RevokeChainByID(r.Context(), oldTok.RefreshChainID)
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token revoked or expired")
		return
	}

	refreshExp := now.Add(oauth.RefreshTokenLifetime)
	newTok := &oauth.Token{
		AccessToken:      oauth.NewAccessToken(),
		RefreshToken:     oauth.NewRefreshToken(),
		RefreshChainID:   oldTok.RefreshChainID, // inherit chain
		ClientID:         oldTok.ClientID,
		UserID:           oldTok.UserID,
		AgentEmail:       oldTok.AgentEmail,
		Scope:            oldTok.Scope,
		ExpiresAt:        now.Add(oauth.AccessTokenLifetime),
		RefreshExpiresAt: &refreshExp,
	}
	if err := a.oauthStore.RotateRefreshToken(r.Context(), refreshToken, newTok); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to rotate refresh token")
		return
	}
	writeTokenResponse(w, newTok)
}
