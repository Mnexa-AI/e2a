package agent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/approvaltoken"
	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/hitlnotify"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/ratelimit"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/google/go-github/v72/github"
	"github.com/gorilla/mux"
	"github.com/ory/fosite"
)

// writeJSON encodes payload as the response body. Logs encoding errors
// rather than swallowing them — an Encode failure usually means the
// client closed the connection mid-response or the payload contains a
// non-marshalable value, both of which are useful to surface in logs
// when debugging truncated responses.
func writeJSON(w http.ResponseWriter, payload any) {
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("[api] json encode failed: %v", err)
	}
}

// readJSON wraps the request body in a MaxBytesReader and decodes into
// dst. Use this for every JSON-decoding handler to bound memory and
// reject obviously oversized payloads early. We deliberately do not
// DisallowUnknownFields — adding it would break existing clients that
// send forward-compatible extra fields, and the SDKs publish typed
// requests so unknown-fields strictness adds little defense.
func readJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	return json.NewDecoder(r.Body).Decode(dst)
}

// Standard request body limits. Most agent/domain payloads are tiny;
// /send carries a full email body which can include large HTML +
// attachments base64-inlined, so it gets the largest cap. Anything over
// these limits is almost certainly malicious or a bug.
const (
	maxRequestBytesSmall = 64 * 1024        // 64 KB — domain/agent CRUD, HITL approve
	maxRequestBytesSend  = 25 * 1024 * 1024 // 25 MB — outbound /send + reply (matches typical SMTP attachment limits)
)

// ValidateWebhookImageURL — see ValidateWebhookURL.
//
// ValidateWebhookURL checks that a webhook URL is safe to call (SSRF protection).
func ValidateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme != "https" {
		return fmt.Errorf("webhook URL must use HTTPS")
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("webhook URL must have a host")
	}

	// Reject IP addresses directly — require domain names
	if ip := net.ParseIP(host); ip != nil {
		return fmt.Errorf("webhook URL must use a domain name, not an IP address")
	}

	// Resolve and reject private/loopback IPs
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve webhook host %q: %w", host, err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("webhook URL must not resolve to a private/loopback address")
		}
	}

	return nil
}

var (
	slugPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)
	reservedSlugs = map[string]bool{
		"admin": true, "postmaster": true, "abuse": true, "noreply": true,
		"no-reply": true, "mailer-daemon": true, "info": true, "help": true,
		"demo": true, "test": true, "www": true, "mail": true, "agent": true,
		"api": true, "system": true, "root": true,
	}
)

func validateSlug(slug string) error {
	if len(slug) < 2 || len(slug) > 40 {
		return fmt.Errorf("slug must be 2–40 characters")
	}
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("slug must be lowercase alphanumeric with hyphens, no leading/trailing hyphens")
	}
	if reservedSlugs[slug] {
		return fmt.Errorf("slug %q is reserved", slug)
	}
	return nil
}

type API struct {
	store          *identity.Store
	sender         *outbound.Sender
	smtpRelay      *outbound.SMTPRelay
	userAuth       *auth.UserAuth
	usage          usage.UsageTracker
	smtpDomain     string
	fromDomain     string
	// sharedDomain enables slug-based agent registration when non-empty.
	// See config.Config.SharedDomain for the rationale.
	sharedDomain   string
	// publicURL is the externally visible base URL of the API. Surfaced
	// via GET /api/v1/info so CLI/SDK clients can populate absolute
	// links without each user configuring it. Empty when the operator
	// hasn't set http.public_url.
	publicURL      string
	production     bool
	sendLimit      *ratelimit.Limiter
	regLimit       *ratelimit.Limiter
	pollLimit      *ratelimit.Limiter
	feedbackLimit  *ratelimit.Limiter
	dcrLimit       *ratelimit.Limiter // OAuth Dynamic Client Registration — anonymous endpoint, per-IP
	approvalSigner *approvaltoken.Signer  // optional; if nil, magic-link endpoints return 404
	notifier       *hitlnotify.Notifier   // optional; if nil, holdForApproval doesn't send notification email
	oauthProvider  fosite.OAuth2Provider  // optional; if nil, /api/oauth/* endpoints return 404
	oauthStorage   *oauth.Storage         // optional; consent handler needs Pool() for cross-package tx
}

// SetApprovalSigner wires in the magic-link signer after construction so
// callers (and tests) that don't need HITL magic-link endpoints don't
// have to know about it. When nil, handleApproveMagicLink /
// handleRejectMagicLink respond with 404.
func (a *API) SetApprovalSigner(s *approvaltoken.Signer) { a.approvalSigner = s }

// SetNotifier wires in the HITL notifier. When nil, holdForApproval
// still persists the pending message but doesn't fire the email — useful
// for tests that don't want the async SMTP traffic.
func (a *API) SetNotifier(n *hitlnotify.Notifier) { a.notifier = n }

// SetOAuthProvider wires in the fosite-backed OAuth provider. When
// nil, /api/oauth/* endpoints return 404 (matches the
// SetApprovalSigner / SetNotifier pattern of "optional capability,
// silently absent when not wired"). Operators who don't want OAuth
// enabled simply don't call this.
func (a *API) SetOAuthProvider(p fosite.OAuth2Provider) { a.oauthProvider = p }

// SetOAuthStorage wires in the OAuth storage handle separately from
// the provider. The consent handler needs Pool() to begin a pgx tx
// that spans the agent-create (identity pkg) and the auth-code insert
// (fosite → oauth pkg). Provider-only callers (e.g. /token) don't need
// it, but it's required for /consent to work; setting one without the
// other is a misconfiguration the consent handler surfaces as a 503.
func (a *API) SetOAuthStorage(s *oauth.Storage) { a.oauthStorage = s }

func NewAPI(store *identity.Store, sender *outbound.Sender, smtpRelay *outbound.SMTPRelay, userAuth *auth.UserAuth, usage usage.UsageTracker, smtpDomain, fromDomain, sharedDomain, publicURL string, production bool) *API {
	return &API{
		store:         store,
		sender:        sender,
		smtpRelay:     smtpRelay,
		userAuth:      userAuth,
		usage:         usage,
		smtpDomain:    smtpDomain,
		fromDomain:    fromDomain,
		sharedDomain:  sharedDomain,
		publicURL:     publicURL,
		production:    production,
		sendLimit:     ratelimit.New(1*time.Minute, 60), // 60 sends per agent per minute
		regLimit:      ratelimit.New(1*time.Hour, 20),   // 20 registrations per IP per hour
		pollLimit:     ratelimit.New(1*time.Minute, 60), // 60 poll requests per user per minute
		feedbackLimit: ratelimit.New(1*time.Hour, 10),   // 10 feedback submissions per IP per hour
		dcrLimit:      ratelimit.New(1*time.Hour, 10),   // 10 OAuth client registrations per IP per hour
	}
}

func (a *API) RegisterRoutes(r *mux.Router) {
	// --- Public SDK/CLI contract: /api/v1/... ---
	r.HandleFunc("/api/v1/agents", a.handleListAgents).Methods("GET")
	r.HandleFunc("/api/v1/agents", a.handleRegisterAgent).Methods("POST")
	r.HandleFunc("/api/v1/agents/{email}", a.handleGetAgent).Methods("GET")
	r.HandleFunc("/api/v1/agents/{email}", a.handleUpdateAgent).Methods("PUT")
	r.HandleFunc("/api/v1/agents/{email}", a.handleDeleteAgent).Methods("DELETE")
	r.HandleFunc("/api/v1/send", a.handleSendEmail).Methods("POST")
	r.HandleFunc("/api/v1/agents/{email}/messages", a.handleGetMessages).Methods("GET")
	r.HandleFunc("/api/v1/agents/{email}/messages/{id}", a.handleGetMessage).Methods("GET")
	r.HandleFunc("/api/v1/agents/{email}/messages/{id}/reply", a.handleReplyToMessage).Methods("POST")
	r.HandleFunc("/api/v1/domains", a.handleListDomains).Methods("GET")
	r.HandleFunc("/api/v1/domains", a.handleRegisterDomain).Methods("POST")
	r.HandleFunc("/api/v1/domains/{domain}/verify", a.handleVerifyDomain).Methods("POST")
	r.HandleFunc("/api/v1/domains/{domain}", a.handleUpdateDomain).Methods("PATCH")
	r.HandleFunc("/api/v1/domains/{domain}", a.handleDeleteDomain).Methods("DELETE")

	r.HandleFunc("/api/v1/agents/{email}/test", a.handleSendTestEmail).Methods("POST")

	// User data rights — right-of-access (export) and right-of-deletion.
	// Both scoped to the authenticated user; no path parameter so there's
	// no way to target someone else's data.
	r.HandleFunc("/api/v1/users/me/export", a.handleExportUserData).Methods("GET")
	r.HandleFunc("/api/v1/users/me", a.handleDeleteUserData).Methods("DELETE")

	// Per-user webhook signing secrets — multi-secret rotation, fully
	// user-managed (create + delete; no auto-rotation, no TTL).
	r.HandleFunc("/api/v1/users/me/signing-secrets", a.handleListSigningSecrets).Methods("GET")
	r.HandleFunc("/api/v1/users/me/signing-secrets", a.handleCreateSigningSecret).Methods("POST")
	r.HandleFunc("/api/v1/users/me/signing-secrets/{id}", a.handleDeleteSigningSecret).Methods("DELETE")

	// HITL approval endpoints — scoped to the user (not a single agent) so
	// reviewers can see pending messages across all their agents at once.
	r.HandleFunc("/api/v1/messages", a.handleListMessages).Methods("GET")
	r.HandleFunc("/api/v1/messages/{id}", a.handleGetOutboundMessage).Methods("GET")
	r.HandleFunc("/api/v1/messages/{id}/approve", a.handleApprovePendingMessage).Methods("POST")
	r.HandleFunc("/api/v1/messages/{id}/reject", a.handleRejectPendingMessage).Methods("POST")

	// Magic-link endpoints. GET renders a token-gated confirmation page
	// with a POST form; POST executes the action. Splitting this way
	// keeps email-client URL scanners (Gmail, Outlook Safe Links,
	// corporate mail gateways) from triggering side effects just by
	// previewing the link.
	r.HandleFunc("/api/v1/approve", a.handleApproveMagicLinkGet).Methods("GET")
	r.HandleFunc("/api/v1/approve", a.handleApproveMagicLinkPost).Methods("POST")
	r.HandleFunc("/api/v1/reject", a.handleRejectMagicLinkGet).Methods("GET")
	r.HandleFunc("/api/v1/reject", a.handleRejectMagicLinkPost).Methods("POST")

	// Deployment discovery — unauthenticated, used by CLI/SDK to learn the
	// shared domain and other deployment-specific values without requiring
	// each user to set them by hand.
	r.HandleFunc("/api/v1/info", a.handleInfo).Methods("GET")

	// --- Non-versioned operational endpoints ---
	r.HandleFunc("/api/health", a.handleHealth).Methods("GET", "HEAD")
	r.HandleFunc("/api/feedback", a.handleFeedback).Methods("POST")

	// OAuth 2.1 / RFC 6749 endpoints. Handlers 404 when
	// SetOAuthProvider wasn't called, so registering unconditionally
	// is safe.
	r.HandleFunc("/api/oauth/authorize", a.handleOAuthAuthorize).Methods("GET")
	r.HandleFunc("/api/oauth/consent", a.handleOAuthConsent).Methods("POST")
	r.HandleFunc("/api/oauth/token", a.handleOAuthToken).Methods("POST")
	r.HandleFunc("/api/oauth/revoke", a.handleOAuthRevoke).Methods("POST")
	r.HandleFunc("/api/oauth/register", a.handleOAuthRegister).Methods("POST")
	r.HandleFunc("/api/oauth/clients/{client_id}", a.handleOAuthGetClient).Methods("GET")
	r.HandleFunc("/.well-known/oauth-authorization-server", a.handleOAuthDiscovery).Methods("GET")

	// User auth (Google OAuth for agent developers)
	if a.userAuth != nil {
		r.HandleFunc("/api/auth/login", a.userAuth.HandleLogin).Methods("GET")
		r.HandleFunc("/api/auth/callback", a.userAuth.HandleCallback).Methods("GET")
		r.HandleFunc("/api/auth/logout", a.userAuth.HandleLogout).Methods("POST")
		r.HandleFunc("/api/auth/me", a.userAuth.HandleMe).Methods("GET")
		r.HandleFunc("/api/auth/me", a.userAuth.HandleUpdateMe).Methods("PATCH")

		// Dashboard
		r.HandleFunc("/api/dashboard/stats", a.userAuth.HandleDashboardStats).Methods("GET")
		r.HandleFunc("/api/dashboard/agents", a.userAuth.HandleDashboardAgents).Methods("GET")
		r.HandleFunc("/api/dashboard/agents/{email}", a.userAuth.HandleUpdateAgent).Methods("PUT")
		r.HandleFunc("/api/dashboard/agents/{email}", a.userAuth.HandleDeleteAgent).Methods("DELETE")
		r.HandleFunc("/api/dashboard/agents/{email}/activity", a.userAuth.HandleAgentActivity).Methods("GET")

		// API Keys
		r.HandleFunc("/api/keys", a.userAuth.HandleCreateAPIKey).Methods("POST")
		r.HandleFunc("/api/keys", a.userAuth.HandleListAPIKeys).Methods("GET")
		r.HandleFunc("/api/keys/{id}", a.userAuth.HandleDeleteAPIKey).Methods("DELETE")
	}
}

// RegisterWSRoute registers the WebSocket endpoint for local-mode agents.
func (a *API) RegisterWSRoute(r *mux.Router, handle http.HandlerFunc) {
	r.HandleFunc("/api/v1/agents/{email}/ws", handle)
}

// errOAuthBearerInvalid is returned by authenticateUser when an
// ate2a_-prefixed bearer fails validation (revoked, expired, unknown,
// or provider not wired). writeAuthError checks errors.Is on this to
// decide whether the WWW-Authenticate challenge should include the
// OAuth-specific error params per RFC 6750 §3.1.
var errOAuthBearerInvalid = errors.New("oauth bearer invalid")

// stripBearerScheme removes the "Bearer " prefix from an Authorization
// header value. RFC 6750 §2.1 specifies the scheme name as case-
// INSENSITIVE; a literal `TrimPrefix(h, "Bearer ")` would silently fail
// on `bearer ate2a_…` or `BEARER ate2a_…`. Returns the raw header value
// when no Bearer scheme was used (lets the legacy unprefixed API-key
// path continue to work).
func stripBearerScheme(h string) string {
	parts := strings.SplitN(h, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return h
}

// authenticateUser extracts and validates the bearer credential from
// the request, returning the owning user.
//
// Dispatch is by token prefix:
//   - ate2a_  → OAuth access token (fosite-validated via the configured
//     provider). Rejected if missing, revoked, expired, or the
//     provider isn't wired.
//   - anything else (typically e2a_, but we accept legacy unprefixed
//     keys too) → API key (api_keys table).
//
// If no Authorization header is present, falls back to the session
// cookie used by the web dashboard.
func (a *API) authenticateUser(r *http.Request) (*identity.User, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		bearer := stripBearerScheme(authHeader)
		if strings.HasPrefix(bearer, oauth.AccessTokenPrefix) {
			return a.lookupUserByOAuthToken(r, bearer)
		}
		return a.store.GetUserByAPIKey(r.Context(), bearer)
	}
	// Fall back to session cookie auth
	if a.userAuth != nil {
		if user := a.userAuth.AuthenticateRequest(r); user != nil {
			return user, nil
		}
	}
	return nil, fmt.Errorf("authorization required")
}

// lookupUserByOAuthToken validates an ate2a_-prefixed bearer via
// fosite's IntrospectToken (which derives the signature using the
// same strategy that issued the token, looks up the row via our
// Storage, and checks revoked/expired). On success we read the
// e2a-specific user_id from the session and resolve to the user
// record. Every failure mode wraps errOAuthBearerInvalid so the
// response layer reliably classifies these as OAuth-bearer rejections.
func (a *API) lookupUserByOAuthToken(r *http.Request, bearer string) (*identity.User, error) {
	if a.oauthProvider == nil {
		// OAuth not enabled on this deployment. Fail closed rather
		// than fall through to the API-key path (which would compare
		// the ate2a_ token against the api_keys hash and miss — a
		// slower path to the same 401, with a less actionable log).
		return nil, fmt.Errorf("%w: provider not configured", errOAuthBearerInvalid)
	}
	session := &oauth.Session{}
	tu, ar, err := a.oauthProvider.IntrospectToken(r.Context(), bearer, fosite.AccessToken, session)
	if err != nil {
		// Preserve fosite's typed error via %w so writeAuthError can
		// errors.Is(...) against fosite.ErrTokenExpired below.
		return nil, fmt.Errorf("%w: %w", errOAuthBearerInvalid, err)
	}
	// Defense in depth: fosite v0.49's IntrospectToken with
	// tokenUse=AccessToken doesn't HARD-reject a refresh-token row —
	// it falls back to refresh-token validation if access fails. We
	// rely on table separation in storage to keep them disjoint, but
	// an explicit check at the seam means a future fosite/storage
	// refactor can't silently break the type guard.
	if tu != fosite.AccessToken {
		return nil, fmt.Errorf("%w: token is not an access token (got %q)", errOAuthBearerInvalid, tu)
	}
	// Trust the session, not the request — the session was loaded
	// from the DB row fosite hydrated.
	sess, ok := ar.GetSession().(*oauth.Session)
	if !ok || sess.UserID == "" {
		return nil, fmt.Errorf("%w: session missing user_id", errOAuthBearerInvalid)
	}
	u, err := a.store.GetUserByID(r.Context(), sess.UserID)
	if err != nil {
		// Wrap so writeAuthError emits the OAuth challenge instead of
		// a bare 401 (the bearer that got us here was valid; the user
		// row vanished out from under it).
		return nil, fmt.Errorf("%w: user lookup: %v", errOAuthBearerInvalid, err)
	}
	return u, nil
}

// writeAuthError writes a 401 response with an RFC 6750 §3
// WWW-Authenticate challenge.
//
// Every 401 on an endpoint that accepts Bearer auth MUST advertise
// the Bearer scheme so clients know how to retry (§3, first paragraph).
// When the failing credential WAS an OAuth bearer (sentinel err wrap
// or `ate2a_` prefix observed on the request), we additionally emit
// the §3.1 error params so MCP clients can trigger the OAuth re-flow.
//
// We deliberately do NOT distinguish "revoked" from "unknown token" in
// error_description: that distinction would be a token-existence
// oracle (an attacker with a candidate ate2a_ string could probe
// whether it once existed by reading the description). fosite's
// `ErrTokenExpired` is the one signal we expose because it fires
// from the strategy's expiry check, never from the storage layer, so
// "expired" doesn't leak existence.
//
// TODO(slice: RFC 9728 resource metadata): when the protected-resource
// metadata document lands, add `resource_metadata="<url>"` here so MCP
// clients can auto-discover the authorization server.
func (a *API) writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	bearer := stripBearerScheme(r.Header.Get("Authorization"))
	isOAuthFailure := errors.Is(err, errOAuthBearerInvalid) || strings.HasPrefix(bearer, oauth.AccessTokenPrefix)

	challenge := `Bearer realm="e2a"`
	if isOAuthFailure {
		desc := "the access token is invalid"
		if errors.Is(err, fosite.ErrTokenExpired) {
			desc = "the access token has expired"
		}
		challenge = `Bearer realm="e2a", error="invalid_token", error_description="` + desc + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

// resolveAgentForUser loads an agent by email address and verifies the user owns it.
func (a *API) resolveAgentForUser(r *http.Request, email string, user *identity.User) (*identity.AgentIdentity, error) {
	agent, err := a.store.GetAgentByEmail(r.Context(), email)
	if err != nil {
		return nil, fmt.Errorf("agent not found")
	}
	if agent.UserID != user.ID {
		return nil, fmt.Errorf("forbidden")
	}
	return agent, nil
}

type RegisterAgentRequest struct {
	Email      string `json:"email"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	WebhookURL string `json:"webhook_url"`
	AgentMode  string `json:"agent_mode"`
} // @name RegisterAgentRequest

type RegisterAgentResponse struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
	Email  string `json:"email"`
} // @name RegisterAgentResponse

// handleRegisterAgent creates a new agent.
// @Summary      Register a new agent
// @Description  Register a new agent with a custom domain or, on deployments where slug registration is enabled, a slug on the shared domain. Use `slug` for instant onboarding (no DNS needed), or `email` for a custom domain (requires domain to be registered and verified first).
// @Tags         Agents
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body RegisterAgentRequest true "Agent registration details"
// @Success      201 {object} RegisterAgentResponse
// @Failure      400 {string} string "Invalid request"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      409 {string} string "Agent already exists"
// @Failure      429 {string} string "Rate limit exceeded"
// @Router       /api/v1/agents [post]
func (a *API) handleRegisterAgent(w http.ResponseWriter, r *http.Request) {
	if !a.regLimit.Allow(clientIP(r)) {
		http.Error(w, "rate limit exceeded — try again later", http.StatusTooManyRequests)
		return
	}

	var req RegisterAgentRequest
	if err := readJSON(w, r, &req, maxRequestBytesSmall); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Shared-domain registration via slug
	isSharedDomain := false
	if req.Slug != "" {
		if a.sharedDomain == "" {
			http.Error(w, "shared-domain registration is not configured", http.StatusBadRequest)
			return
		}
		if err := validateSlug(req.Slug); err != nil {
			http.Error(w, fmt.Sprintf("invalid slug: %v", err), http.StatusBadRequest)
			return
		}
		req.Email = req.Slug + "@" + a.sharedDomain
		isSharedDomain = true
	}

	// Require authentication (API key or session) for agent registration.
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	if req.Email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}

	if req.WebhookURL != "" {
		if err := ValidateWebhookURL(req.WebhookURL); err != nil {
			http.Error(w, fmt.Sprintf("invalid webhook URL: %v", err), http.StatusBadRequest)
			return
		}
	}

	// Extract DNS domain from agent email.
	// For shared-domain agents, the domain is the configured shared domain.
	// For custom-domain agents, the domain is the DNS domain part of the email.
	var domain string
	if isSharedDomain {
		domain = a.sharedDomain
	} else {
		parts := strings.SplitN(req.Email, "@", 2)
		if len(parts) != 2 || parts[1] == "" {
			http.Error(w, "invalid email address", http.StatusBadRequest)
			return
		}
		domain = parts[1]
	}

	// For custom domains, verify the domain is registered and verified by this user.
	if !isSharedDomain {
		domainRecord, err := a.store.LookupDomain(r.Context(), domain, user.ID)
		if err != nil {
			http.Error(w, "register and verify your domain first", http.StatusBadRequest)
			return
		}
		if !domainRecord.Verified {
			http.Error(w, "verify your domain first", http.StatusBadRequest)
			return
		}
	}

	// Validate agent mode
	agentMode := req.AgentMode
	if agentMode == "" {
		agentMode = "cloud"
	}
	if agentMode != "cloud" && agentMode != "local" {
		http.Error(w, "agent_mode must be 'cloud' or 'local'", http.StatusBadRequest)
		return
	}
	if agentMode == "cloud" && req.WebhookURL == "" {
		http.Error(w, "webhook_url is required for cloud agent mode", http.StatusBadRequest)
		return
	}

	agent, err := a.store.CreateAgent(r.Context(), req.Email, domain, req.Name, req.WebhookURL, agentMode, user.ID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			http.Error(w, "agent already registered for this domain", http.StatusConflict)
			return
		}
		http.Error(w, fmt.Sprintf("failed to register agent: %v", err), http.StatusInternalServerError)
		return
	}

	resp := RegisterAgentResponse{
		ID:     agent.ID,
		Domain: agent.Domain,
		Email:  agent.Email,
	}

	log.Printf("[api] agent registered: email=%s webhook=%s shared=%t", req.Email, req.WebhookURL, isSharedDomain)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, resp)
}

// handleListAgents returns all agents owned by the authenticated user.
// @Summary      List your registered agents
// @Description  Returns all agents owned by the authenticated user.
// @Tags         Agents
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} ListAgentsResponse
// @Failure      401 {string} string "Missing or invalid API key"
// @Router       /api/v1/agents [get]
func (a *API) handleListAgents(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	agents, err := a.store.ListAgentsByUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "failed to list agents", http.StatusInternalServerError)
		return
	}

	resp := make([]AgentInfo, 0, len(agents))
	for _, ag := range agents {
		resp = append(resp, agentInfoFromIdentity(&ag))
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]interface{}{
		"agents": resp,
	})
}

// handleGetAgent returns details for a single agent.
// @Summary      Get agent details
// @Description  Fetch details for a specific agent owned by the authenticated user.
// @Tags         Agents
// @Produce      json
// @Security     BearerAuth
// @Param        email path string true "Agent email address" example(my-bot@example.com)
// @Success      200 {object} AgentInfo
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      403 {string} string "Agent not owned by this user"
// @Router       /api/v1/agents/{email} [get]
func (a *API) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	email := mux.Vars(r)["email"]

	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	ag, err := a.resolveAgentForUser(r, email, user)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, agentInfoFromIdentity(ag))
}

// agentInfoFromIdentity converts an internal AgentIdentity to the public AgentInfo
// response type, stripping fields like user_id and public that are not part of the
// documented API contract.
func agentInfoFromIdentity(ag *identity.AgentIdentity) AgentInfo {
	return AgentInfo{
		ID:                   ag.ID,
		Domain:               ag.Domain,
		Email:                ag.EmailAddress(),
		Name:                 ag.Name,
		WebhookURL:           ag.WebhookURL,
		AgentMode:            ag.AgentMode,
		DomainVerified:       ag.DomainVerified,
		CreatedAt:            ag.CreatedAt,
		HITLEnabled:          ag.HITLEnabled,
		HITLTTLSeconds:       ag.HITLTTLSeconds,
		HITLExpirationAction: ag.HITLExpirationAction,
	}
}

// handleUpdateAgent updates fields on an agent owned by the authenticated
// user. Uses pointer-typed fields in the request body so a PUT can carry
// any subset of (agent_mode, webhook_url, hitl_enabled, hitl_ttl_seconds,
// hitl_expiration_action) without forcing callers to re-send the others.
//
// Mirrors the dashboard's /api/dashboard/agents/{email} PUT, but
// authenticated via API key so CLI + SDK callers can drive the same
// config surface.
// @Summary      Update an agent
// @Description  Updates an agent you own. Fields are optional; only the ones you send are applied, so callers can PATCH a single setting (for example, toggle HITL on) without re-supplying the others. When changing to cloud mode, webhook_url becomes required. HITL TTL is server-capped at 604800 seconds (7 days). Returns the post-update agent state so callers can confirm what landed.
// @Tags         Agents
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        email path string true "Agent email address" example(my-bot@example.com)
// @Param        request body UpdateAgentRequest true "Fields to update"
// @Success      200 {object} AgentInfo
// @Failure      400 {string} string "Validation error (e.g. bad agent_mode, TTL out of range)"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      403 {string} string "Agent not owned by this user"
// @Failure      404 {string} string "Agent not found"
// @Router       /api/v1/agents/{email} [put]
func (a *API) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	email := mux.Vars(r)["email"]

	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	ag, err := a.resolveAgentForUser(r, email, user)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		WebhookURL           *string `json:"webhook_url"`
		AgentMode            *string `json:"agent_mode"`
		HITLEnabled          *bool   `json:"hitl_enabled"`
		HITLTTLSeconds       *int    `json:"hitl_ttl_seconds"`
		HITLExpirationAction *string `json:"hitl_expiration_action"`
	}
	if err := readJSON(w, r, &req, maxRequestBytesSmall); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	touched := false

	if req.AgentMode != nil {
		mode := *req.AgentMode
		if mode != "cloud" && mode != "local" {
			http.Error(w, "agent_mode must be 'cloud' or 'local'", http.StatusBadRequest)
			return
		}
		webhook := ""
		if req.WebhookURL != nil {
			webhook = *req.WebhookURL
		}
		if mode == "cloud" && webhook == "" {
			http.Error(w, "webhook_url is required when switching to cloud mode", http.StatusBadRequest)
			return
		}
		if err := a.store.UpdateAgentMode(r.Context(), ag.ID, user.ID, mode, webhook); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		touched = true
	} else if req.WebhookURL != nil {
		if err := a.store.UpdateAgentWebhook(r.Context(), ag.ID, user.ID, *req.WebhookURL); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		touched = true
	}

	if req.HITLEnabled != nil || req.HITLTTLSeconds != nil || req.HITLExpirationAction != nil {
		enabled := ag.HITLEnabled
		if req.HITLEnabled != nil {
			enabled = *req.HITLEnabled
		}
		ttl := ag.HITLTTLSeconds
		if req.HITLTTLSeconds != nil {
			ttl = *req.HITLTTLSeconds
		}
		action := ag.HITLExpirationAction
		if req.HITLExpirationAction != nil {
			action = *req.HITLExpirationAction
		}
		if err := a.store.UpdateAgentHITL(r.Context(), ag.ID, user.ID, enabled, ttl, action); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		touched = true
	}

	if !touched {
		http.Error(w, "no recognized fields in request", http.StatusBadRequest)
		return
	}

	// Re-read so the response shows the final, authoritative state —
	// lets the CLI confirm what actually landed.
	updated, err := a.store.GetAgentByID(r.Context(), ag.ID)
	if err != nil {
		http.Error(w, "failed to reload agent", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, agentInfoFromIdentity(updated))
}

// handleDeleteAgent deletes an agent owned by the authenticated user.
// @Summary      Delete an agent
// @Description  Delete an agent owned by the authenticated user. The agent email is cleared from any local config.
// @Tags         Agents
// @Produce      json
// @Security     BearerAuth
// @Param        email path string true "Agent email address" example(my-bot@example.com)
// @Success      200 {object} map[string]string "status: deleted"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      403 {string} string "Agent not owned by this user"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/agents/{email} [delete]
func (a *API) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	email := mux.Vars(r)["email"]

	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	agent, err := a.resolveAgentForUser(r, email, user)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := a.store.DeleteAgent(r.Context(), agent.ID, user.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"status": "deleted"})
}

// --- Domain Management ---

// handleRegisterDomain registers a new domain for the authenticated user.
// @Summary      Register a domain
// @Description  Register a custom domain for use with agents. Returns DNS records to configure and a verification token.
// @Tags         Domains
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body RegisterDomainRequest true "Domain to register"
// @Success      201 {object} Domain
// @Failure      400 {string} string "Invalid request"
// @Failure      401 {string} string "Missing or invalid API key"
// @Router       /api/v1/domains [post]
func (a *API) handleRegisterDomain(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	var req RegisterDomainRequest
	if err := readJSON(w, r, &req, maxRequestBytesSmall); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}
	// The configured shared domain is reserved — users cannot claim it
	// as a custom domain, since it backs slug-based agent registration
	// for everyone.
	if a.sharedDomain != "" && strings.EqualFold(req.Domain, a.sharedDomain) {
		http.Error(w, "reserved domain", http.StatusBadRequest)
		return
	}

	domainRecord, err := a.store.ClaimOrCreateDomain(r.Context(), req.Domain, user.ID)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to register domain: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, a.domainInfoFromRecord(domainRecord))
}

// handleVerifyDomain verifies domain ownership via TXT record.
// @Summary      Verify domain ownership
// @Description  Verify domain ownership by checking for the expected TXT record in DNS.
// @Tags         Domains
// @Produce      json
// @Security     BearerAuth
// @Param        domain path string true "Domain name"
// @Success      200 {object} VerifyDomainResponse
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      404 {string} string "Domain not found"
// @Failure      412 {string} string "TXT record not found"
// @Router       /api/v1/domains/{domain}/verify [post]
func (a *API) handleVerifyDomain(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	domain := mux.Vars(r)["domain"]

	domainRecord, err := a.store.LookupDomain(r.Context(), domain, user.ID)
	if err != nil {
		http.Error(w, "domain not found", http.StatusNotFound)
		return
	}

	if domainRecord.Verified {
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, VerifyDomainResponse{
			Domain:     domainRecord.Domain,
			Verified:   true,
			VerifiedAt: domainRecord.VerifiedAt,
		})
		return
	}

	// Touch last_checked_at regardless of whether the probe succeeds —
	// the column is "when did we last try", separate from verified_at
	// which only moves on success. Best-effort: a failed touch shouldn't
	// block the verify response (the row is locked-down to the user, so
	// the only realistic failure is a transient DB issue).
	if err := a.store.TouchDomainLastChecked(r.Context(), domain, user.ID); err != nil {
		log.Printf("[api] touch last_checked_at for %s: %v", domain, err)
	}

	// In dev mode, skip DNS verification
	if !a.production {
		log.Printf("[api] dev mode: skipping DNS check for %s", domain)
	} else {
		// Look up TXT records for the domain
		txtRecords, err := net.LookupTXT(domain)
		if err != nil {
			http.Error(w, fmt.Sprintf("DNS lookup failed for %s: %v", domain, err), http.StatusBadRequest)
			return
		}

		found := false
		for _, txt := range txtRecords {
			if strings.Contains(txt, domainRecord.VerificationToken) {
				found = true
				break
			}
		}

		if !found {
			http.Error(w, fmt.Sprintf("TXT record not found. Expected: %s", domainRecord.VerificationToken), http.StatusPreconditionFailed)
			return
		}
	}

	if err := a.store.VerifyDomain(r.Context(), domain, user.ID); err != nil {
		http.Error(w, fmt.Sprintf("failed to verify domain: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("[api] domain verified: %s", domain)

	// Re-fetch to get verified_at timestamp
	domainRecord, err = a.store.LookupDomain(r.Context(), domain, user.ID)
	if err != nil {
		// Verification succeeded but re-fetch failed; return basic success
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, VerifyDomainResponse{
			Domain:   domain,
			Verified: true,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, VerifyDomainResponse{
		Domain:     domainRecord.Domain,
		Verified:   true,
		VerifiedAt: domainRecord.VerifiedAt,
	})
}

// handleUpdateDomain serves PATCH /api/v1/domains/{domain}. Currently
// supports a single mutable field — is_primary — surfaced for the
// dashboard's "Set as primary" button. The partial unique index on
// (user_id) WHERE is_primary=true makes the multi-statement swap-
// then-set transaction necessary; SetDomainPrimary handles that
// atomically. Other domain fields (verification token, verified
// timestamp) are managed by the dedicated verify path and aren't
// settable here.
// @Summary      Update a domain
// @Description  Update mutable fields on a domain. The only supported field today is `is_primary` — passing `true` promotes this domain and clears the flag from any previously-primary domain in a single transaction. Passing `false` is a no-op (use SetDomainPrimary on a different domain to demote this one).
// @Tags         Domains
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        domain path string true "Domain name"
// @Param        request body UpdateDomainRequest true "Fields to update"
// @Success      200 {object} DomainInfo
// @Failure      400 {string} string "Invalid request"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      404 {string} string "Domain not found"
// @Router       /api/v1/domains/{domain} [patch]
func (a *API) handleUpdateDomain(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	domain := mux.Vars(r)["domain"]

	var req struct {
		IsPrimary *bool `json:"is_primary,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.IsPrimary == nil {
		http.Error(w, "no updatable fields provided", http.StatusBadRequest)
		return
	}
	if !*req.IsPrimary {
		// Demote is a no-op — to switch primary, promote a different
		// domain. This keeps the partial unique index meaningful:
		// exactly one primary or zero, never a "no domain is primary
		// because I demoted the only one" footgun.
		http.Error(w, "to switch primary, PATCH the new primary domain instead", http.StatusBadRequest)
		return
	}

	if err := a.store.SetDomainPrimary(r.Context(), domain, user.ID); err != nil {
		if errors.Is(err, identity.ErrDomainNotFound) {
			http.Error(w, "domain not found", http.StatusNotFound)
			return
		}
		log.Printf("[api] SetDomainPrimary %s: %v", domain, err)
		http.Error(w, "failed to update domain", http.StatusInternalServerError)
		return
	}

	d, err := a.store.LookupDomain(r.Context(), domain, user.ID)
	if err != nil {
		// Should be unreachable — SetDomainPrimary just succeeded against
		// this row. Still, return a useful error rather than nil-dereffing
		// in domainInfoFromRecord.
		http.Error(w, "failed to read back domain", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, a.domainInfoFromRecord(d))
}

// handleListDomains lists all domains owned by the authenticated user.
// @Summary      List your domains
// @Description  Returns all domains owned by the authenticated user.
// @Tags         Domains
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} ListDomainsResponse
// @Failure      401 {string} string "Missing or invalid API key"
// @Router       /api/v1/domains [get]
func (a *API) handleListDomains(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	domains, err := a.store.ListDomainsByUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "failed to list domains", http.StatusInternalServerError)
		return
	}

	resp := make([]DomainInfo, 0, len(domains))
	for _, d := range domains {
		resp = append(resp, a.domainInfoFromRecord(&d))
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, ListDomainsResponse{Domains: resp})
}

// handleDeleteDomain deletes a domain owned by the authenticated user.
// @Summary      Delete a domain
// @Description  Delete a domain. Fails if agents still exist on this domain.
// @Tags         Domains
// @Produce      json
// @Security     BearerAuth
// @Param        domain path string true "Domain name"
// @Success      204 "Domain deleted"
// @Failure      400 {string} string "Agents still exist on this domain"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      404 {string} string "Domain not found"
// @Router       /api/v1/domains/{domain} [delete]
func (a *API) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	domain := mux.Vars(r)["domain"]

	// Check that the domain exists and is owned by this user
	_, err = a.store.LookupDomain(r.Context(), domain, user.ID)
	if err != nil {
		http.Error(w, "domain not found", http.StatusNotFound)
		return
	}

	// Check if agents still exist on the domain
	hasAgents, err := a.store.HasAgentsOnDomain(r.Context(), domain, user.ID)
	if err != nil {
		http.Error(w, "failed to check domain agents", http.StatusInternalServerError)
		return
	}
	if hasAgents {
		http.Error(w, "cannot delete domain while agents exist — delete agents first", http.StatusBadRequest)
		return
	}

	if err := a.store.DeleteDomain(r.Context(), domain, user.ID); err != nil {
		if errors.Is(err, identity.ErrDomainHasAgents) {
			http.Error(w, "cannot delete domain while agents exist — delete agents first", http.StatusBadRequest)
			return
		}
		if errors.Is(err, identity.ErrDomainNotFound) {
			http.Error(w, "domain not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("failed to delete domain: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// domainInfoFromRecord converts an internal Domain to the public DomainInfo response type.
func (a *API) domainInfoFromRecord(d *identity.Domain) DomainInfo {
	mxPriority := 10
	return DomainInfo{
		Domain:            d.Domain,
		Verified:          d.Verified,
		VerificationToken: d.VerificationToken,
		DNSRecords: DNSRecords{
			MX:  DNSRecord{Host: "@", Value: a.smtpDomain, Priority: &mxPriority},
			TXT: DNSRecord{Host: "@", Value: d.VerificationToken},
		},
		CreatedAt:     d.CreatedAt,
		VerifiedAt:    d.VerifiedAt,
		IsPrimary:     d.IsPrimary,
		LastCheckedAt: d.LastCheckedAt,
		AgentCount:    d.AgentCount,
	}
}

// holdForApproval persists a fully composed outbound SendRequest as a
// pending_approval message and writes a 202 response. It is the shared
// branch taken by handleSendEmail, handleReplyToMessage, and
// handleSendTestEmail when agent.HITLEnabled is true.
//
// replyToEmailMessageID is the inbound Message-ID being replied to, or "".
// msgType is one of "send", "reply", or "test".
func (a *API) holdForApproval(w http.ResponseWriter, r *http.Request, agent *identity.AgentIdentity, req outbound.SendRequest, msgType, replyToEmailMessageID string) {
	var attachmentsJSON []byte
	if len(req.Attachments) > 0 {
		b, err := json.Marshal(req.Attachments)
		if err != nil {
			log.Printf("[api] hitl: marshal attachments: %v", err)
			http.Error(w, "failed to serialize attachments", http.StatusInternalServerError)
			return
		}
		attachmentsJSON = b
	}

	msg, err := a.store.CreatePendingOutboundMessage(
		r.Context(), agent.ID,
		req.To, req.CC, req.BCC,
		req.Subject, req.Body, req.HTMLBody,
		attachmentsJSON,
		msgType, req.ConversationID, replyToEmailMessageID,
		agent.HITLTTLSeconds,
	)
	if err != nil {
		log.Printf("[api] hitl: create pending message: agent=%s err=%v", agent.ID, err)
		http.Error(w, "failed to hold message for approval", http.StatusInternalServerError)
		return
	}

	slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
	log.Printf("[mail:%s] dir=outbound type=%s status=pending_approval from=%s to=%v slug=%s conv_id=%s subject=%q approval_expires_at=%s",
		msg.ID, msgType, agent.EmailAddress(), req.To, slug, req.ConversationID, req.Subject, msg.ApprovalExpiresAt.Format(time.RFC3339))

	// Fire the reviewer notification asynchronously. Failures are logged
	// inside the notifier and must never block the 202 response — the
	// pending row is already persisted and the expiration worker will
	// finalize it even if every notification email bounces.
	a.notifier.NotifyPendingApprovalAsync(msg, agent)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]interface{}{
		"status":              "pending_approval",
		"message_id":          msg.ID,
		"approval_expires_at": msg.ApprovalExpiresAt,
	})
}

// --- Send Email ---

// handleSendEmail sends a new email from the authenticated user's agent.
// @Summary      Send a new email
// @Description  Send an email from your agent to any recipient. Your agent must be domain-verified. Messages are delivered via SMTP. Rate limited to 60 sends per agent per minute. Pass conversation_id to tag the message as part of a thread. When the owning agent has HITL (human-in-the-loop) enabled, the server responds with 202 Accepted and status="pending_approval" instead — the message is held until a reviewer approves it via the dashboard, CLI, or magic link, or until the approval TTL expires and the configured expiration action fires.
// @Tags         Email
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body SendEmailRequest true "Email to send"
// @Success      200 {object} SendEmailResponse "Message sent immediately"
// @Success      202 {object} SendEmailResponse "Message held for human approval"
// @Failure      400 {string} string "Missing required fields"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      403 {string} string "Agent domain not verified"
// @Failure      429 {string} string "Rate limit exceeded"
// @Router       /api/v1/send [post]
func (a *API) handleSendEmail(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	var req outbound.SendRequest
	if err := readJSON(w, r, &req, maxRequestBytesSend); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Subject == "" || req.Body == "" {
		http.Error(w, "subject and body are required", http.StatusBadRequest)
		return
	}
	if len(req.To) == 0 && len(req.CC) == 0 {
		http.Error(w, "at least one recipient in to or cc is required", http.StatusBadRequest)
		return
	}
	if err := validateConversationID(req.ConversationID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Resolve agent from "from" field, or auto-select if user has exactly one agent
	var agent *identity.AgentIdentity
	if req.From != "" {
		agent, err = a.resolveAgentForUser(r, req.From, user)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid from: %v", err), http.StatusBadRequest)
			return
		}
	} else {
		agents, err := a.store.ListAgentsByUser(r.Context(), user.ID)
		if err != nil || len(agents) == 0 {
			http.Error(w, "from field required (no agents found)", http.StatusBadRequest)
			return
		}
		if len(agents) > 1 {
			http.Error(w, "from field required when user has multiple agents", http.StatusBadRequest)
			return
		}
		agent = &agents[0]
	}

	if !a.sendLimit.Allow(agent.ID) {
		http.Error(w, "rate limit exceeded — max 60 sends per minute", http.StatusTooManyRequests)
		return
	}

	if !agent.DomainVerified {
		http.Error(w, "agent domain must be verified before sending", http.StatusForbidden)
		return
	}

	selfSend := isSelfSend(req, agent.EmailAddress())

	// HITL applies to self-sends too — the gate is "did a human
	// review this outbound message" regardless of recipient. The
	// approval-finalize path (see hitl_api.go / hitl_magic_api.go)
	// detects the self-send shape on the held message and routes
	// the delivery through the loopback short-circuit instead of
	// outbound.Sender, which would otherwise strip the agent's own
	// address from the recipient list and error.
	if agent.HITLEnabled {
		a.holdForApproval(w, r, agent, req, "send", "")
		return
	}

	// Record usage (side-effect only — never block on quota)
	if _, err := a.usage.RecordAndCheck(r.Context(), user.ID, agent.ID, agent.Domain, "outbound"); err != nil {
		log.Printf("[api] usage recording error: %v", err)
	}

	if selfSend {
		providerID, err := a.performSelfSend(r.Context(), agent, req)
		if err != nil {
			log.Printf("[api] self-send failed: agent=%s error=%v", agent.EmailAddress(), err)
			http.Error(w, "self-send failed", http.StatusInternalServerError)
			return
		}
		slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
		log.Printf("[mail] dir=outbound type=send method=loopback from=%s to=%s slug=%s conv_id=%s subject=%q provider_id=%s", agent.EmailAddress(), agent.EmailAddress(), slug, req.ConversationID, req.Subject, providerID)
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]string{
			"status":     "sent",
			"message_id": providerID,
			"method":     "loopback",
		})
		return
	}

	result, err := a.sender.Send(agent, req)
	if err != nil {
		if outbound.IsValidationError(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("[api] send failed: agent=%s to=%v error=%v", agent.Domain, req.To, err)
		http.Error(w, fmt.Sprintf("send failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Record outbound message with canonicalized recipients from result
	outMsg, err := a.store.CreateOutboundMessage(r.Context(), agent.ID, result.To, result.CC, result.BCC, req.Subject, "send", result.Method, result.MessageID, req.ConversationID)
	if err != nil {
		log.Printf("[api] failed to record outbound message: %v", err)
	}

	slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
	if outMsg != nil {
		log.Printf("[mail:%s] dir=outbound type=send from=%s to=%v slug=%s conv_id=%s subject=%q", outMsg.ID, agent.EmailAddress(), result.To, slug, req.ConversationID, req.Subject)
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{
		"status":     "sent",
		"message_id": result.MessageID,
		"method":     result.Method,
	})
}

// handleSendTestEmail sends a test email from the platform to the agent's address.
// @Summary      Send a test email
// @Description  Send a test email from the platform to the agent's own address. Useful for verifying inbound delivery is wired up correctly. Requires the agent's domain to be verified. If the agent has HITL enabled the message is held for approval and the response is 202.
// @Tags         Agents
// @Produce      json
// @Security     BearerAuth
// @Param        email path string true "Agent email address" example(my-bot@example.com)
// @Success      200 {object} map[string]string "status and message_id"
// @Success      202 {object} map[string]string "message_id (held for HITL approval)"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      403 {string} string "Agent domain not verified"
// @Failure      404 {string} string "Agent not found"
// @Failure      500 {string} string "Failed to send test email"
// @Router       /api/v1/agents/{email}/test [post]
func (a *API) handleSendTestEmail(w http.ResponseWriter, r *http.Request) {
	agentEmail := mux.Vars(r)["email"]

	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	agent, err := a.resolveAgentForUser(r, agentEmail, user)
	if err != nil {
		http.Error(w, fmt.Sprintf("agent not found: %v", err), http.StatusNotFound)
		return
	}

	if !agent.DomainVerified {
		http.Error(w, "agent domain must be verified before sending test email", http.StatusForbidden)
		return
	}

	envelopeFrom := fmt.Sprintf("noreply@%s", a.fromDomain)
	headerFrom := fmt.Sprintf("%q <%s>", "e2a", envelopeFrom)
	to := []string{agent.EmailAddress()}
	subject := "Test email from e2a"
	body := fmt.Sprintf("This is a test email for %s.\n\nYour agent is set up and ready to receive emails.", agent.EmailAddress())

	if agent.HITLEnabled {
		a.holdForApproval(w, r, agent, outbound.SendRequest{
			To:      to,
			Subject: subject,
			Body:    body,
		}, "test", "")
		return
	}

	message, err := outbound.ComposeMessage(headerFrom, to, nil, subject, body, "text/plain", "", nil, a.fromDomain, "", "")
	if err != nil {
		log.Printf("[api] compose test email failed: %v", err)
		http.Error(w, "failed to compose test email", http.StatusInternalServerError)
		return
	}

	messageID, err := a.smtpRelay.Send(envelopeFrom, to, message)
	if err != nil {
		log.Printf("[api] send test email failed: %v", err)
		http.Error(w, fmt.Sprintf("failed to send test email: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("[api] test email sent to %s (message_id=%s)", agent.EmailAddress(), messageID)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{
		"status":     "sent",
		"message_id": messageID,
	})
}

type ReplyRequest struct {
	Body           string                `json:"body"`
	HTMLBody       string                `json:"html_body,omitempty"`
	ReplyAll       bool                  `json:"reply_all,omitempty"`
	CC             []string              `json:"cc,omitempty"`
	BCC            []string              `json:"bcc,omitempty"`
	ConversationID string                `json:"conversation_id,omitempty"`
	Attachments    []outbound.Attachment `json:"attachments,omitempty"`
}

// handleReplyToMessage replies to a previously received email.
// @Summary      Reply to an inbound email
// @Description  Reply to a previously received email using its message ID. The reply is sent as a real email back to the original sender, with proper threading headers (In-Reply-To, References). Pass conversation_id to tag the reply with your thread ID — the recipient will see it on their inbound payload. Rate limited to 60 sends per agent per minute. When the owning agent has HITL enabled, the server returns 202 Accepted and status="pending_approval" instead of sending immediately.
// @Tags         Email
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        email path string true "Agent email address" example(my-bot@example.com)
// @Param        id    path string true "Message ID from the inbound payload" example(msg_abc123)
// @Param        request body ReplyToMessageRequest true "Reply content"
// @Success      200 {object} SendEmailResponse "Reply sent immediately"
// @Success      202 {object} SendEmailResponse "Reply held for human approval"
// @Failure      400 {string} string "Missing body"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      403 {string} string "Agent domain not verified"
// @Failure      404 {string} string "Message not found or does not belong to this agent"
// @Failure      429 {string} string "Rate limit exceeded"
// @Router       /api/v1/agents/{email}/messages/{id}/reply [post]
func (a *API) handleReplyToMessage(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	vars := mux.Vars(r)
	email := vars["email"]
	msgID := vars["id"]

	// Resolve agent from URL path and verify user owns it
	agent, err := a.resolveAgentForUser(r, email, user)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	inbound, err := a.store.GetInboundMessage(r.Context(), msgID)
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	// Verify message belongs to this agent
	if inbound.AgentID != agent.ID {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	if !a.sendLimit.Allow(agent.ID) {
		http.Error(w, "rate limit exceeded — max 60 sends per minute", http.StatusTooManyRequests)
		return
	}

	if !agent.DomainVerified {
		http.Error(w, "agent domain must be verified before sending", http.StatusForbidden)
		return
	}

	var req ReplyRequest
	if err := readJSON(w, r, &req, maxRequestBytesSend); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Body == "" {
		http.Error(w, "body is required", http.StatusBadRequest)
		return
	}
	if err := validateConversationID(req.ConversationID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Build reply subject — avoid Re: Re: stacking
	subject := inbound.Subject
	if subject != "" && !strings.HasPrefix(strings.ToLower(subject), "re: ") {
		subject = "Re: " + subject
	} else if subject == "" {
		subject = "Re: your message"
	}

	// Resolve reply recipients from the raw inbound message
	replyRecipients, err := outbound.ParseReplyRecipients(inbound.RawMessage, req.ReplyAll, req.CC)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// If parsing yielded no To recipients, fall back to inbound.Sender
	replyTo := replyRecipients.To
	if len(replyTo) == 0 {
		replyTo = []string{inbound.Sender}
	}

	// Build the full References chain from the inbound's prior chain plus
	// the inbound itself. Required so multi-party replies thread correctly
	// for participants who didn't see the immediate parent's Message-ID.
	references := outbound.BuildReferencesChain(inbound.RawMessage, inbound.EmailMessageID)

	// Build the SendRequest and route through Sender
	sendReq := outbound.SendRequest{
		To:               replyTo,
		CC:               replyRecipients.CC,
		BCC:              req.BCC,
		Subject:          subject,
		Body:             req.Body,
		HTMLBody:         req.HTMLBody,
		ReplyToMessageID: inbound.EmailMessageID,
		References:       references,
		ConversationID:   req.ConversationID,
		Attachments:      req.Attachments,
	}

	// Self-reply detection. If the resolved reply destination is the
	// agent's own address (e.g. replying to a previous self-note),
	// the SMTP path would error: outbound.Sender.Send strips agent
	// aliases from the recipient list to prevent self-spam, leaving
	// "no valid recipients" on a reply where the original sender WAS
	// the agent itself. Route through the loopback short-circuit
	// instead — symmetric with handleSendEmail's self-send path.
	//
	// Pre-clean: with replyAll=true on a self-thread the inherited
	// CC list already includes the agent's own address (it was a
	// recipient on the original message). isSelfSend requires CC ==
	// [] to fire; without this strip we'd fall through to the SMTP
	// path and outbound.Sender would error with "no valid recipients"
	// after its own alias-strip leaves the lists empty. Stripping
	// here just moves that work upstream so isSelfSend sees a "true"
	// self-loop instead of a "self + self-aliases-in-CC" shape.
	sendReq.CC = stripAgentSelfAliases(sendReq.CC, agent.EmailAddress())
	sendReq.BCC = stripAgentSelfAliases(sendReq.BCC, agent.EmailAddress())
	selfReply := isSelfSend(sendReq, agent.EmailAddress())

	// HITL applies to self-replies for the same reason as self-sends:
	// a reviewer-in-the-loop gate doesn't care whether the recipient
	// is external or the agent itself. The approval finalizer routes
	// the held reply through loopback when it's a self-reply.
	if agent.HITLEnabled {
		a.holdForApproval(w, r, agent, sendReq, "reply", inbound.EmailMessageID)
		return
	}

	// Record usage (side-effect only — never block on quota)
	if _, err := a.usage.RecordAndCheck(r.Context(), user.ID, agent.ID, agent.Domain, "outbound"); err != nil {
		log.Printf("[api] usage recording error: %v", err)
	}

	if selfReply {
		providerID, err := a.performSelfSend(r.Context(), agent, sendReq)
		if err != nil {
			log.Printf("[api] self-reply failed: agent=%s error=%v", agent.EmailAddress(), err)
			http.Error(w, "self-reply failed", http.StatusInternalServerError)
			return
		}
		slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
		log.Printf("[mail] dir=outbound type=reply method=loopback from=%s to=%s slug=%s conv_id=%s subject=%q provider_id=%s in_reply_to=%s",
			agent.EmailAddress(), agent.EmailAddress(), slug, req.ConversationID, subject, providerID, inbound.EmailMessageID)
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]string{
			"status":     "sent",
			"message_id": providerID,
			"method":     "loopback",
		})
		return
	}

	result, err := a.sender.Send(agent, sendReq)
	if err != nil {
		if outbound.IsValidationError(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("[api] reply failed: agent=%s to=%v error=%v", agent.Domain, replyTo, err)
		http.Error(w, fmt.Sprintf("delivery failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Record outbound message with canonicalized recipients from result
	outMsg, err := a.store.CreateOutboundMessage(r.Context(), agent.ID, result.To, result.CC, result.BCC, subject, "reply", result.Method, result.MessageID, req.ConversationID)
	if err != nil {
		log.Printf("[api] failed to record outbound message: %v", err)
	}

	slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
	if outMsg != nil {
		log.Printf("[mail:%s] dir=outbound type=reply from=%s to=%v slug=%s conv_id=%s subject=%q in_reply_to=%s", outMsg.ID, agent.EmailAddress(), result.To, slug, req.ConversationID, subject, msgID)
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{
		"status":     "sent",
		"message_id": result.MessageID,
		"method":     result.Method,
	})
}

// --- Polling API ---

// handleGetMessages lists messages for an agent.
// @Summary      List messages for an agent
// @Description  Fetch messages for an agent. Returns lightweight summaries (no raw message content). Supports opaque token-based pagination via `page_size` and `token`.
// @Tags         Email
// @Produce      json
// @Security     BearerAuth
// @Param        email     path  string true  "Agent email address" example(my-bot@example.com)
// @Param        status    query string false "Filter by message status" Enums(unread, read, all) default(unread)
// @Param        page_size query int    false "Number of messages per page (1-100)" minimum(1) maximum(100) default(50)
// @Param        token     query string false "Opaque pagination token from a previous response's next_token"
// @Success      200 {object} ListMessagesResponse
// @Failure      400 {string} string "Invalid status, pagination token, or filter mismatch"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      404 {string} string "Agent not found or not owned by this user"
// @Router       /api/v1/agents/{email}/messages [get]
func (a *API) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	if !a.pollLimit.Allow(user.ID) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded — max 60 requests per minute", http.StatusTooManyRequests)
		return
	}

	// Resolve agent from URL path
	email := mux.Vars(r)["email"]
	agent, err := a.resolveAgentForUser(r, email, user)
	if err != nil {
		http.Error(w, fmt.Sprintf("agent not found: %v", err), http.StatusNotFound)
		return
	}

	status := r.URL.Query().Get("status")
	if status == "" {
		status = "unread"
	}
	if status != "unread" && status != "read" && status != "all" {
		http.Error(w, "status must be 'unread', 'read', or 'all'", http.StatusBadRequest)
		return
	}

	pageSize := 50
	if ps := r.URL.Query().Get("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n > 0 && n <= 100 {
			pageSize = n
		}
	}

	// Decode opaque pagination token (encodes cursor position + filters)
	var afterTime time.Time
	var afterID string
	if tok := r.URL.Query().Get("token"); tok != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(tok)
		if err != nil {
			http.Error(w, "invalid pagination token", http.StatusBadRequest)
			return
		}
		var cursor struct {
			CreatedAt time.Time `json:"c"`
			ID        string    `json:"i"`
			Status    string    `json:"s"`
			AgentID   string    `json:"a"`
		}
		if err := json.Unmarshal(decoded, &cursor); err != nil {
			http.Error(w, "invalid pagination token", http.StatusBadRequest)
			return
		}
		if cursor.Status != status {
			http.Error(w, "token was created with different filters — start a new query without a token", http.StatusBadRequest)
			return
		}
		if cursor.AgentID != agent.ID {
			http.Error(w, "token was created for a different agent — start a new query without a token", http.StatusBadRequest)
			return
		}
		afterTime = cursor.CreatedAt
		afterID = cursor.ID
	}

	// Fetch pageSize+1 to determine if there are more pages
	messages, err := a.store.GetMessagesByAgent(r.Context(), agent.ID, status, pageSize+1, afterTime, afterID)
	if err != nil {
		http.Error(w, "failed to fetch messages", http.StatusInternalServerError)
		return
	}

	hasMore := len(messages) > pageSize
	if hasMore {
		messages = messages[:pageSize]
	}

	type messageSummary struct {
		ID             string   `json:"message_id"`
		From           string   `json:"from"`
		To             []string `json:"to"`
		CC             []string `json:"cc,omitempty"`
		ReplyTo        []string `json:"reply_to,omitempty"`
		Recipient      string   `json:"recipient"`
		Subject        string   `json:"subject"`
		ConversationID string   `json:"conversation_id,omitempty"`
		Status         string   `json:"status"`
		CreatedAt      string   `json:"created_at"`
	}

	summaries := make([]messageSummary, len(messages))
	for i, m := range messages {
		summaries[i] = messageSummary{
			ID:             m.ID,
			From:           m.Sender,
			To:             orEmptySlice(m.ToRecipients),
			CC:             m.CC,
			ReplyTo:        m.ReplyTo,
			Recipient:      m.Recipient,
			Subject:        m.Subject,
			ConversationID: m.ConversationID,
			Status:         m.DeliveryStatus,
			CreatedAt:      m.CreatedAt.UTC().Format(time.RFC3339),
		}
	}

	// Build next_token from last message (includes filters for validation)
	var nextToken *string
	if hasMore {
		last := messages[len(messages)-1]
		cursorJSON, err := json.Marshal(struct {
			CreatedAt time.Time `json:"c"`
			ID        string    `json:"i"`
			Status    string    `json:"s"`
			AgentID   string    `json:"a"`
		}{CreatedAt: last.CreatedAt, ID: last.ID, Status: status, AgentID: agent.ID})
		if err != nil {
			http.Error(w, "failed to build pagination token", http.StatusInternalServerError)
			return
		}
		tok := base64.RawURLEncoding.EncodeToString(cursorJSON)
		nextToken = &tok
	}

	resp := map[string]interface{}{
		"messages": summaries,
	}
	if nextToken != nil {
		resp["next_token"] = *nextToken
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

// handleGetMessage fetches a single message with full content.
// @Summary      Get a single message
// @Description  Fetch full message content including raw RFC 2822 email and auth headers. If the message is unread, this request marks it as read.
// @Tags         Email
// @Produce      json
// @Security     BearerAuth
// @Param        email path string true "Agent email address" example(my-bot@example.com)
// @Param        id    path string true "Message ID" example(msg_abc123)
// @Success      200 {object} MessageDetail
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      404 {string} string "Message not found"
// @Router       /api/v1/agents/{email}/messages/{id} [get]
func (a *API) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}

	if !a.pollLimit.Allow(user.ID) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded — max 60 requests per minute", http.StatusTooManyRequests)
		return
	}

	vars := mux.Vars(r)
	email := vars["email"]
	msgID := vars["id"]

	// Resolve agent from URL path and verify user owns it
	agent, err := a.resolveAgentForUser(r, email, user)
	if err != nil {
		http.Error(w, fmt.Sprintf("agent not found: %v", err), http.StatusNotFound)
		return
	}

	msg, err := a.store.GetMessageWithContent(r.Context(), msgID, agent.ID)
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]interface{}{
		"message_id":      msg.ID,
		"from":            msg.Sender,
		"to":              orEmptySlice(msg.ToRecipients),
		"cc":              msg.CC,
		"reply_to":        msg.ReplyTo,
		"recipient":       msg.Recipient,
		"subject":         msg.Subject,
		"conversation_id": msg.ConversationID,
		"status":          msg.DeliveryStatus,
		"created_at":      msg.CreatedAt.UTC().Format(time.RFC3339),
		"auth_headers":    msg.AuthHeaders,
		"raw_message":     msg.RawMessage,
	})
}

// orEmptySlice returns s if non-nil, otherwise an empty []string. We marshal
// the To: list as an always-present array (no omitempty) so SDK clients can
// rely on it being present, even for messages stored before the column was
// populated for inbound.
func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleInfo returns deployment-specific configuration so CLIs and SDKs
// can self-configure with only an API URL. Intentionally unauthenticated:
// the returned values are public-facing (they show up in user-visible
// emails and DNS) and exposing them lets a fresh `e2a login` populate the
// rest of the client config without forcing the user to know any
// deployment-specific values up front.
//
// @Summary      Deployment info
// @Description  Returns deployment-specific configuration (shared domain, etc.) so CLI/SDK clients can self-configure. Unauthenticated.
// @Tags         System
// @Produce      json
// @Success      200 {object} DeploymentInfo
// @Router       /api/v1/info [get]
func (a *API) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, DeploymentInfo{
		SharedDomain:             a.sharedDomain,
		SlugRegistrationEnabled:  a.sharedDomain != "",
		PublicURL:                a.publicURL,
	})
}

func (a *API) handleFeedback(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !a.feedbackLimit.Allow(ip) {
		http.Error(w, "rate limit exceeded — max 10 feedback submissions per hour", http.StatusTooManyRequests)
		return
	}

	var req struct {
		Email    string `json:"email"`
		Category string `json:"category"`
		Message  string `json:"message"`
	}
	if err := readJSON(w, r, &req, maxRequestBytesSmall); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if len([]rune(req.Message)) > 5000 {
		http.Error(w, "message too long (max 5000 characters)", http.StatusBadRequest)
		return
	}
	if len(req.Email) > 254 {
		http.Error(w, "email too long", http.StatusBadRequest)
		return
	}
	if req.Category == "" {
		req.Category = "general"
	}
	if req.Category != "bug" && req.Category != "feature" && req.Category != "general" {
		http.Error(w, "category must be bug, feature, or general", http.StatusBadRequest)
		return
	}

	// If user is authenticated, use their email
	if a.userAuth != nil {
		if user := a.userAuth.AuthenticateRequest(r); user != nil {
			if req.Email == "" {
				req.Email = user.Email
			}
		}
	}

	// Create GitHub issue
	labelMap := map[string]string{
		"bug":     "bug",
		"feature": "enhancement",
		"general": "feedback",
	}
	label := labelMap[req.Category]

	// Sanitize user input to prevent GitHub @mention spam and Markdown injection
	sanitize := func(s string) string {
		return strings.ReplaceAll(s, "@", "@\u200B") // zero-width space breaks @mentions
	}

	title := fmt.Sprintf("[%s] %s", req.Category, truncate(sanitize(req.Message), 80))

	body := sanitize(req.Message)
	if req.Email != "" {
		body += fmt.Sprintf("\n\n---\nSubmitted by: `%s`", sanitize(req.Email))
	}

	ghToken := os.Getenv("GITHUB_FEEDBACK_TOKEN")
	if ghToken == "" {
		safeMsg := strings.ReplaceAll(req.Message, "\n", " ")
		if len([]rune(safeMsg)) > 200 {
			safeMsg = string([]rune(safeMsg)[:200])
		}
		log.Printf("feedback: GITHUB_FEEDBACK_TOKEN not set, logging only: [%s] %s", req.Category, safeMsg)
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	ghRepo := os.Getenv("GITHUB_FEEDBACK_REPO")
	if ghRepo == "" {
		ghRepo = "Mnexa-AI/e2a"
	}
	parts := strings.SplitN(ghRepo, "/", 2)
	if len(parts) != 2 {
		log.Printf("feedback: invalid GITHUB_FEEDBACK_REPO: %s", ghRepo)
		http.Error(w, "failed to submit feedback", http.StatusInternalServerError)
		return
	}

	client := github.NewClient(nil).WithAuthToken(ghToken)
	_, _, err := client.Issues.Create(r.Context(), parts[0], parts[1], &github.IssueRequest{
		Title:  github.Ptr(title),
		Body:   github.Ptr(body),
		Labels: &[]string{label},
	})
	if err != nil {
		log.Printf("feedback: GitHub API error: %v", err)
		http.Error(w, "failed to submit feedback", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"status": "ok"})
}

func truncate(s string, maxLen int) string {
	// Truncate to first line, then to maxLen (rune-safe)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	runes := []rune(s)
	if maxLen <= 3 {
		return "..."
	}
	if len(runes) > maxLen {
		return string(runes[:maxLen-3]) + "..."
	}
	return s
}

// validateConversationID rejects values containing CR or LF. The
// composer treats conversation_id as a passthrough for the
// X-E2A-Conversation-ID header; allowing CRLF would let any
// authenticated caller smuggle additional headers (Bcc, fake
// DKIM-Signature, body smuggling) into the outbound MIME message.
// Defense-in-depth: the composer also strips CRLF, but rejecting
// at the boundary gives the caller a clear 400 instead of silently
// neutralising their input.
func validateConversationID(id string) error {
	if strings.ContainsAny(id, "\r\n") {
		return errors.New("conversation_id must not contain CR or LF")
	}
	return nil
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(ip)
		}
		return strings.TrimSpace(xff)
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}
