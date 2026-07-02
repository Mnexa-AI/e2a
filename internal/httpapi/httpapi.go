package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

// Authenticator resolves the calling user from the raw request. It is
// injected so this package reuses the *single* auth path that already lives
// in the agent layer (API key, OAuth bearer, session cookie) instead of
// forking a second one — there is exactly one place credentials are checked.
type Authenticator func(r *http.Request) (*identity.User, error)

// PrincipalAuthenticator is the scope-aware seam (Slice 5a): the same single
// auth path, but returning the full principal (user + credential scope + bound
// agent) so the v1 handlers can enforce the hard scope ceiling (design §5).
// When set it supersedes Authenticator; when nil the server wraps Authenticator
// and treats every caller as account-scoped (pre-Slice-5a behavior).
type PrincipalAuthenticator func(r *http.Request) (*identity.Principal, error)

// AgentLister returns the agents owned by a user. Injected as a narrow
// function so the foundation slice doesn't depend on the whole store; the
// remaining ports will widen this into a resource-scoped store interface.
type AgentLister func(ctx context.Context, userID string) ([]identity.AgentIdentity, error)

// AgentGetter loads a single agent by its full email address (the
// identifier). Ownership is checked by the caller against the resolved
// agent's UserID.
type AgentGetter func(ctx context.Context, address string) (*identity.AgentIdentity, error)

// MessageGetter loads a single message (with content) scoped to an agent.
// Mirrors store.GetMessageWithContent(messageID, agentID).
type MessageGetter func(ctx context.Context, messageID, agentID string) (*identity.Message, error)

// MessageLister returns a filtered page of message summaries for an agent.
// Mirrors store.GetMessagesByAgent(filter).
type MessageLister func(ctx context.Context, filter identity.MessageListFilter) ([]identity.Message, error)

// ConversationLister mirrors store.ListConversationsByAgent(filter).
type ConversationLister func(ctx context.Context, filter identity.ConversationListFilter) ([]identity.ConversationSummary, error)

// ConversationGetter mirrors store.GetConversationByID(agentID, conversationID).
type ConversationGetter func(ctx context.Context, agentID, conversationID string) (*identity.ConversationDetail, error)

// --- write collaborators ---

// AgentCreator mirrors store.CreateAgent. The webhookURL/agentMode params are
// retained for signature compatibility with the store but are ignored — the
// legacy columns were dropped (migration 029). Handlers pass "".
type AgentCreator func(ctx context.Context, email, domain, name, webhookURL, agentMode, userID string) (*identity.AgentIdentity, error)

// DomainLookup mirrors store.LookupDomain(domain, userID) — the create-time
// ownership guard.
type DomainLookup func(ctx context.Context, domain, userID string) (*identity.Domain, error)

// AgentCreateEnforcer mirrors enforcer.CheckAgentCreate; returns a
// limits.LimitExceededError when the per-user cap is hit.
type AgentCreateEnforcer func(ctx context.Context, userID string) error

// Agent mutation funcs mirror the like-named store methods.
type (
	AgentDeleter func(ctx context.Context, agentID, userID string) error
)

// Deps are the collaborators the v1 layer needs. Everything is injected so
// the package has no hidden globals and is straightforward to test.
type Deps struct {
	Authenticator          Authenticator
	PrincipalAuthenticator PrincipalAuthenticator
	// AuthChallenge builds the RFC 6750 §3 WWW-Authenticate header value for a
	// request that failed authentication. Injected so the v1 surface advertises
	// the Bearer scheme (and OAuth error params) on every 401 exactly like the
	// legacy mux did, from the same definition (agent.API.WWWAuthenticateChallenge).
	// Optional — nil disables the challenge header.
	AuthChallenge func(r *http.Request) string
	ListAgents    AgentLister
	GetAgent      AgentGetter
	GetMessage    MessageGetter
	ListMessages  MessageLister
	// ModifyMessageLabels applies a labels delta to a message scoped to an
	// agent, returning the post-update set. Mirrors store.ModifyMessageLabels.
	ModifyMessageLabels func(ctx context.Context, messageID, agentID string, add, remove []string) ([]string, error)

	ListConversations ConversationLister
	GetConversation   ConversationGetter

	CreateAgent        AgentCreator
	LookupDomain       DomainLookup
	EnforceAgentCreate AgentCreateEnforcer
	// UpdateAgentName updates an agent's display name (the only mutable field on
	// the agent PATCH after the screening config moved to /protection).
	UpdateAgentName func(ctx context.Context, agentID, userID, name string) error
	// UpdateAgentProtection writes the full per-agent protection posture (gate +
	// scan sensitivity + holds) for the /v1/agents/{email}/protection resource.
	// Returns a validation error for an invalid posture, which the handler maps
	// to 400 invalid_request.
	UpdateAgentProtection func(ctx context.Context, agentID, userID string, cfg identity.ProtectionConfig) error
	DeleteAgent           AgentDeleter

	// domains
	ListDomains         func(ctx context.Context, userID string) ([]identity.Domain, error)
	ClaimDomain         func(ctx context.Context, domain, userID string) (*identity.Domain, error)
	EnforceDomainCreate func(ctx context.Context, userID string) error
	DeleteDomain        func(ctx context.Context, domain, userID string) error
	HasAgentsOnDomain   func(ctx context.Context, domain, userID string) (bool, error)

	// SMTPDomain is the relay's MX host, surfaced in the DNS records a
	// domain must publish (config smtp.domain).
	SMTPDomain string

	// SESRegion is the AWS region of the SES sending identity
	// (config sender_identity.ses_region). Non-empty ⇒ the sending feature is
	// enabled: domainView emits the deterministic mail_from_* records. Empty ⇒
	// sending is off and those records are omitted.
	SESRegion string

	// CursorSecret is the deployment HMAC secret (config.Signing.HMACSecret)
	// used to sign/verify pagination cursors so they are tamper-evident
	// (issue #144 M2). The same key approvaltoken and the X-E2A-Auth-* email
	// headers use — no new key. Handlers pass it to EncodeCursor and wrap it
	// in a 1-element slice for DecodeCursor (whose verify loop supports N for
	// a future secret rotation). Empty in minimal test setups, which is fine:
	// encode and verify stay consistent under the same (empty) key.
	CursorSecret string

	// Idempotency is the retry-safety store for unsafe writes (send/reply/
	// forward/redeliver). Optional — nil disables the Idempotency-Key path.
	Idempotency IdemStore

	// outbound (the shared live delivery path extracted from agent.API)
	DeliverOutbound func(ctx context.Context, user *identity.User, ag *identity.AgentIdentity, req outbound.SendRequest, msgType, replyToEmailMessageID string, referenced *identity.Message) (*agent.OutboundResult, *agent.OutboundError)
	SendTest        func(ctx context.Context, ag *identity.AgentIdentity) (*agent.OutboundResult, *agent.OutboundError)
	// HITL approve/reject (the held-draft decision)
	ApprovePending     func(ctx context.Context, userID, messageID, expectedAgentEmail string, ovr agent.ApproveOverrides) (*identity.Message, *agent.OutboundError)
	RejectPending      func(ctx context.Context, userID, messageID, expectedAgentEmail, reason string) (*identity.Message, *agent.OutboundError)
	EnforceMessageSend func(ctx context.Context, userID string) error
	// Inbound review release — the held-screening decision (design 2026-06-22 §5).
	// GetReviewMessage resolves a held message's direction so /approve+/reject can
	// branch (it intentionally sees held inbound statuses, scoped to the resolved
	// owned agent — account-scope only). ApproveInboundReview releases the message
	// to the agent's inbox; RejectInboundReview drops it. Both fire the unified
	// review_approved/review_rejected events. Optional — nil leaves the endpoints
	// outbound-only (pre-slice-3 behavior).
	GetReviewMessage     func(ctx context.Context, messageID, agentID string) (*identity.ReviewMessageMeta, error)
	ApproveInboundReview func(ctx context.Context, userID string, msg *identity.ReviewMessageMeta) *agent.OutboundError
	RejectInboundReview  func(ctx context.Context, userID, reason string, msg *identity.ReviewMessageMeta) *agent.OutboundError

	// Review queue (account-scoped /v1/reviews). ListReviews returns all holds
	// (both directions) across the user's agents; GetReviewWithContent loads one
	// held message (ownership-scoped) for the detail view + approve/reject
	// resolution. Both intentionally include held inbound — operator surface only.
	ListReviews          func(ctx context.Context, userID string) ([]identity.ReviewListItem, error)
	GetReviewWithContent func(ctx context.Context, userID, messageID string) (*identity.Message, error)
	// SendLimit is the per-agent outbound rate limiter (mirrors
	// sendLimit.AllowWithRetryAfter; key = agent id). Optional.
	SendLimit func(key string) (ok bool, retryAfter time.Duration)
	// PollLimit is the per-user read limiter (key = user id) and RegLimit is
	// the per-IP agent-registration limiter (key = client ip). Both return
	// the IETF RateLimit snapshot so the middleware can set the headers.
	// Optional — nil disables that limiter on the /v1 surface.
	PollLimit RateSnapshot
	RegLimit  RateSnapshot
	// DownloadLimit is the per-IP attachment-download limiter (key = client ip).
	// The download route is a raw chi handler outside the Huma rate-limit
	// middleware, so it consults this directly. Optional — nil disables it.
	DownloadLimit RateSnapshot
	// GetRepliableMessage loads a message that can be replied to or forwarded —
	// either an inbound the agent received or an outbound the agent sent — as
	// long as it is live (not expired) and not held/rejected in review. The
	// reply/forward handlers use this so an agent can continue a thread off its
	// own sent message (Gmail-style), which GetInboundMessage's direction filter
	// forbids.
	GetRepliableMessage func(ctx context.Context, messageID string) (*identity.Message, error)

	// AttachmentStore mints/verifies short-lived attachment downloads (§6a #5).
	// Native by default; when nil, the attachment endpoints are unavailable.
	AttachmentStore AttachmentStore

	// account
	GetLimits      func(ctx context.Context, userID string) (limits.Limits, error)
	GetUsage       func(ctx context.Context, userID string) LimitsUsageView
	ExportUserData func(ctx context.Context, userID string) (*identity.UserExport, error)

	// Suppression list (decision 9 / Slice 4b). Optional — nil deployments
	// return 501 from the /v1/account/suppressions endpoints.
	ListSuppressions  func(ctx context.Context, userID string, limit int, afterCreatedAt time.Time, afterAddress string) ([]identity.Suppression, error)
	RemoveSuppression func(ctx context.Context, userID, address string) (bool, error)
	DeleteUserData    func(ctx context.Context, user *identity.User) (*identity.DeleteUserDataResult, error)

	// events (delivery log). EventQuery carries the filters + cursor
	// position; the closures bind the events pool in main.
	ListEvents func(ctx context.Context, q EventQuery) ([]agent.EventJSON, error)
	GetEvent2  func(ctx context.Context, userID, eventID string) (*agent.EventJSON, error)
	// redeliver
	LoadReplayEvent      func(ctx context.Context, userID, eventID string) (*agent.ReplayEvent, error)
	InsertReplayDelivery func(ctx context.Context, eventID, webhookID, eventType string, messageID *string, envelope []byte) (string, error)

	// EventsEnabled reflects whether the durable event log (the
	// webhook_events outbox) is populated on this deployment — i.e. the
	// WEBHOOKS_OUTBOX_ENABLED flag. When false the legacy publisher delivers
	// webhooks straight to webhook_subscriber_deliveries and webhook_events is
	// never written, so list/get/redeliver would silently return empty. The
	// events handlers gate on this and return 501 events_log_disabled instead
	// of masquerading as "no events". Webhook delivery is unaffected either way.
	EventsEnabled bool

	// webhooks
	CreateWebhook func(ctx context.Context, userID, url, description string, events []string, filters identity.WebhookFilters) (*identity.Webhook, error)
	ListWebhooks  func(ctx context.Context, userID string) ([]identity.Webhook, error)
	GetWebhook    func(ctx context.Context, webhookID, userID string) (*identity.Webhook, error)
	UpdateWebhook func(ctx context.Context, webhookID, userID string, u identity.WebhookUpdate) (*identity.Webhook, error)
	DeleteWebhook func(ctx context.Context, webhookID, userID string) error
	RotateSecret  func(ctx context.Context, webhookID, userID string) (string, time.Time, error)
	// TestWebhookInsert schedules a synthetic delivery (subscriberStore.
	// InsertPendingForTest). ListDeliveries reads the per-webhook delivery
	// log (subscriberStore.ListDeliveriesByWebhook).
	TestWebhookInsert func(ctx context.Context, webhookID, eventType string, envelope []byte) (string, error)
	ListDeliveries    func(ctx context.Context, webhookID, status string, limit int) ([]webhook.SubscriberDelivery, error)

	// API keys (account-scope management). CreateScopedAPIKey returns the
	// minted key including its one-time plaintext; agentID is "" for account
	// scope and a resolved agent id for agent scope.
	CreateScopedAPIKey func(ctx context.Context, userID, name, scope, agentID string, expiresAt *time.Time) (*identity.APIKey, error)
	ListAPIKeys        func(ctx context.Context, userID string) ([]identity.APIKey, error)
	DeleteAPIKey       func(ctx context.Context, keyID, userID string) error

	// domain verification
	TouchDomainChecked func(ctx context.Context, domain, userID string) error
	VerifyDomain       func(ctx context.Context, domain, userID string) error
	// EnqueueSenderProvision (decision 4 / Slice 4) schedules SES sending-
	// identity provisioning for a verified domain. Called on every successful
	// verify check (newly OR already verified), so POST /domains/{domain}/verify
	// doubles as the forced sending re-check. Optional — nil when SES is not
	// configured (dev/self-host), leaving sending_status at none (relay From).
	EnqueueSenderProvision func(ctx context.Context, domain string)
	// VerifyProbe runs the live DNS check for a domain's published records.
	// Injected so it is fakeable in tests (the real one wraps
	// agent.CheckDomainRecords).
	VerifyProbe func(domain, verificationToken, dkimSelector, dkimPublicKey string) DomainCheckResult

	// Deployment info surfaced by GET /v1/info.
	SharedDomain string
	PublicURL    string

	// WSHandle serves the WebSocket upgrade for an agent address (the real-
	// time inbound transport). Injected so httpapi need not depend on the ws
	// package; the real one is ws.Handler.ServeWithEmail.
	WSHandle func(w http.ResponseWriter, r *http.Request, address string)

	// Legacy is the existing gorilla/mux handler. The chi root falls back
	// to it for every route not yet ported onto Huma (the strangler), so
	// the service stays fully functional through the multi-sub-slice port.
	Legacy http.Handler
}

// Server is the v1 HTTP surface: a chi root router with the Huma API mounted
// on it and the legacy handler wired as the fallback.
type Server struct {
	Router chi.Router
	API    huma.API
	deps   Deps
}

// New builds the v1 server. It installs the e2a error envelope globally,
// stands up the Huma API on a chi router under the `/v1` documentation
// paths, registers the ported operations, and points chi's not-found/
// method-not-allowed handlers at the legacy surface.
func New(deps Deps) *Server {
	installErrorEnvelope()

	root := chi.NewRouter()
	root.Use(requestID)
	root.Use(securityHeaders)
	root.Use(authChallenge(deps.AuthChallenge))
	root.Use(withRawRequest)

	config := huma.DefaultConfig("e2a API", APIVersion)
	// Serve the spec and human docs under the versioned prefix so they sit
	// beside the operations (api-v1-redesign §1: everything lives under the
	// api host; here, under /v1).
	config.OpenAPIPath = "/v1/openapi"
	config.DocsPath = "/v1/docs"
	config.SchemasPath = "/v1/schemas"
	// Drop Huma's default schema-link transformer: it injects a `$schema`
	// field and Link header into response bodies, which would change the
	// clean contract shape this redesign is standardizing. Keep only our
	// request-id stamper.
	config.CreateHooks = nil
	config.Transformers = []huma.Transformer{stampRequestID}
	config.Info.Description = "e2a — authenticated email gateway for AI agents. v1 contract."
	// Canonical production host (api-v1-redesign §1: "Canonical base URL
	// https://api.e2a.dev/v1"). Operations already carry the /v1 prefix, so the
	// server URL stops at the host — otherwise clients would double it. Without a
	// servers block, generated SDKs default to http://localhost (a
	// Bearer-over-cleartext footgun).
	config.Servers = []*huma.Server{
		{URL: "https://api.e2a.dev", Description: "Production"},
	}
	// One auth scheme across the surface: a Bearer credential that is
	// either an API key or an OAuth 2.1 access token (api-v1-redesign §5).
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer": {
			Type:        "http",
			Scheme:      "bearer",
			Description: "API key (e2a_…) or OAuth 2.1 access token, sent as `Authorization: Bearer <token>`.",
		},
	}

	api := humachi.New(root, config)

	s := &Server{Router: root, API: api, deps: deps}
	// Rate limiting runs as Huma middleware so it can stamp the IETF
	// RateLimit-* headers on the response and short-circuit a 429 before the
	// handler. Registered once; applies to every operation.
	api.UseMiddleware(s.rateLimit)
	s.registerOperations()

	// WebSocket transport — registered directly on chi (not Huma; it's a raw
	// upgrade, not a JSON operation). First-class /v1 inbound transport.
	if deps.WSHandle != nil {
		root.Get("/v1/agents/{email}/ws", func(w http.ResponseWriter, r *http.Request) {
			// chi routes on RawPath when the request URI is percent-encoded and
			// returns URL params STILL ENCODED — and every SDK client encodes the
			// address (encodeURIComponent), so without this decode the handler
			// looked up an agent literally named "x%40y" and 404'd every real
			// WebSocket client. Huma decodes its own params; this bypass route
			// must do it explicitly.
			address := chi.URLParam(r, "email")
			if decoded, err := url.PathUnescape(address); err == nil {
				address = decoded
			}
			deps.WSHandle(w, r, address)
		})
	}

	// Attachment download — raw chi route (not Huma): a binary stream authorized
	// by the capability token in the URL, not the bearer (§6a #5). The metadata
	// endpoint that mints these URLs IS a Huma operation (registerAttachments).
	if deps.AttachmentStore != nil {
		root.Get("/v1/agents/{email}/messages/{id}/attachments/{index}/download", s.handleAttachmentDownload)
	}

	if deps.Legacy != nil {
		root.NotFound(deps.Legacy.ServeHTTP)
		root.MethodNotAllowed(deps.Legacy.ServeHTTP)
	}
	return s
}

// ServeHTTP makes Server a drop-in http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Router.ServeHTTP(w, r)
}

// OpenAPIYAML renders the generated spec as YAML. Used by the codegen step
// and the drift test — the spec is emitted from the live handlers, never
// hand-authored.
func (s *Server) OpenAPIYAML() ([]byte, error) {
	return s.API.OpenAPI().YAML()
}

// registerOperations wires every ported Huma operation. As resources move
// off the legacy mux they are added here and removed from the legacy
// RegisterRoutes in the same commit.
func (s *Server) registerOperations() {
	s.registerInfo()
	s.registerAgents()
	s.registerMessages()
	s.registerAttachments()
	s.registerConversations()
	s.registerAgentWrites()
	s.registerAgentProtection()
	s.registerDomains()
	s.registerWebhooks()
	s.registerEvents()
	s.registerAccount()
	s.registerAPIKeys()
	s.registerOutbound()
	s.registerHITL()
	s.registerReviews()
}

// reqCtxKey carries the raw *http.Request through to Huma handlers so they
// can reuse the injected Authenticator (which reads headers + cookies).
type reqCtxKey struct{}

// withRawRequest stashes the request so Huma handlers can recover it for
// the auth path. Storing the request in its own derived context is the
// standard bridge; only headers/cookies are read downstream, so the
// pre-derivation request is equivalent for authentication.
func withRawRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), reqCtxKey{}, r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestFromContext recovers the raw request stashed by withRawRequest.
func RequestFromContext(ctx context.Context) *http.Request {
	if r, ok := ctx.Value(reqCtxKey{}).(*http.Request); ok {
		return r
	}
	return nil
}

// requireUser authenticates the caller or returns a 401 envelope carrying
// the machine-branchable "unauthorized" code.
func (s *Server) requireUser(ctx context.Context) (*identity.User, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return p.User, nil
}

// requirePrincipal authenticates the caller and returns the full principal
// (user + scope + bound agent), or a 401 envelope. The scope-aware basis for
// the hard scope ceiling (requireAccountScope / requireAgentAccess).
func (s *Server) requirePrincipal(ctx context.Context) (*identity.Principal, error) {
	// The rate-limit middleware may have already authenticated this request
	// on the read path; reuse that principal instead of hitting auth twice.
	if p := principalFromContext(ctx); p != nil {
		return p, nil
	}
	r := RequestFromContext(ctx)
	if r == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "authentication unavailable")
	}
	p, err := s.resolvePrincipal(r)
	if err != nil {
		return nil, NewError(http.StatusUnauthorized, "unauthorized", "authentication required")
	}
	return p, nil
}

// resolvePrincipal runs the injected auth path. It prefers the scope-aware
// PrincipalAuthenticator; if only the legacy Authenticator is wired it treats
// the caller as account-scoped (pre-Slice-5a behavior — no scope ceiling).
func (s *Server) resolvePrincipal(r *http.Request) (*identity.Principal, error) {
	if s.deps.PrincipalAuthenticator != nil {
		return s.deps.PrincipalAuthenticator(r)
	}
	if s.deps.Authenticator == nil {
		return nil, fmt.Errorf("authentication unavailable")
	}
	u, err := s.deps.Authenticator(r)
	if err != nil {
		return nil, err
	}
	return &identity.Principal{User: u, Scope: identity.ScopeAccount}, nil
}
