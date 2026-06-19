package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/actiongate"
	"github.com/Mnexa-AI/e2a/internal/agentauth"
	"github.com/Mnexa-AI/e2a/internal/approvaltoken"
	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/dkim"
	"github.com/Mnexa-AI/e2a/internal/emailauth"
	"github.com/Mnexa-AI/e2a/internal/hitlnotify"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/oauth"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/ratelimit"
	"github.com/Mnexa-AI/e2a/internal/telemetry"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/google/go-github/v72/github"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/fosite"
	"golang.org/x/net/idna"
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

// normalizeEmail is the agent-package-local alias for identity.NormalizeEmail.
// Defined here as a one-line forwarder so the existing call sites in this
// file stay readable; the canonical implementation lives in identity so
// ws/, auth/, and oauth_handlers all reach the same canonicalization.
func normalizeEmail(email string) string {
	return identity.NormalizeEmail(email)
}

// writeTooManyRequests sends a 429 response with a Retry-After header
// (delay-seconds form, RFC 7231 §7.1.3). Callers should pass the
// duration returned from Limiter.AllowWithRetryAfter so the value
// reflects when the next slot actually opens up. Callers must return
// after invoking this — it writes the full response.
func writeTooManyRequests(w http.ResponseWriter, retryAfter time.Duration, msg string) {
	secs := int(retryAfter.Round(time.Second).Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	http.Error(w, msg, http.StatusTooManyRequests)
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
		if webhook.IsDisallowedWebhookIP(ip) {
			return fmt.Errorf("webhook URL must not resolve to a private/loopback/link-local address")
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
	store      *identity.Store
	sender     *outbound.Sender
	smtpRelay  *outbound.SMTPRelay
	userAuth   *auth.UserAuth
	usage      usage.UsageTracker
	smtpDomain string
	fromDomain string
	// sharedDomain enables slug-based agent registration when non-empty.
	// See config.Config.SharedDomain for the rationale.
	sharedDomain string
	// publicURL is the externally visible base URL of the API. Surfaced
	// via GET /api/v1/info so CLI/SDK clients can populate absolute
	// links without each user configuring it. Empty when the operator
	// hasn't set http.public_url.
	publicURL         string
	production        bool
	sendLimit         *ratelimit.Limiter
	regLimit          *ratelimit.Limiter
	pollLimit         *ratelimit.Limiter
	feedbackLimit     *ratelimit.Limiter
	dcrLimit          *ratelimit.Limiter    // OAuth Dynamic Client Registration — anonymous endpoint, per-IP
	approvalSigner    *approvaltoken.Signer // optional; if nil, magic-link endpoints return 404
	notifier          *hitlnotify.Notifier  // optional; if nil, holdForApproval doesn't send notification email
	oauthProvider     fosite.OAuth2Provider // optional; if nil, /oauth2/* endpoints return 404
	oauthStorage      *oauth.Storage        // optional; consent handler needs Pool() for cross-package tx
	signer            *agentauth.Signer     // optional; nil ⇒ JWKS serves an empty set (agent-auth disabled)
	idempotency       *idempotency.Store    // optional; when nil, Idempotency-Key header is ignored
	enforcer          limits.Enforcer       // optional; when nil, all limit checks are skipped (effectively unlimited)
	usageStore        *usage.Store          // optional; needed by handleGetMyLimits to surface current counts
	internalAPISecret string                // optional; when empty, /api/internal/* endpoints return 503
	billingHookURL    string                // optional; when set, handleDeleteUserData POSTs an HMAC-signed user-deleted notice here (sidecar's /api/internal/billing/cancel)
	// subscriberStore powers the slice-2 webhooks-as-a-resource
	// /webhooks/{id}/test and /webhooks/{id}/deliveries endpoints.
	// Optional — when nil, those endpoints return 404 (the rest of
	// the /webhooks CRUD still works, just without test + history).
	subscriberStore *webhook.SubscriberStore

	// domainTeardownHook (decision 4 / Slice 4) is run, within the account-
	// delete transaction, for every domain the user owns — it enqueues the
	// SES sending-identity deprovision job. Optional; nil when SES is not
	// configured (the orphan reaper is the backstop either way).
	domainTeardownHook func(ctx context.Context, tx pgx.Tx, domain string) error

	// publisher routes email.sent / email.pending_approval /
	// email.approved / email.rejected events to the webhooks
	// resource — the sole push path since the legacy per-agent
	// webhook_url was removed in slice 3. Optional — when nil, the
	// trigger sites silently skip the publish step.
	publisher webhookpub.Publisher
	// outbox is the slice-4 transactional publisher for outbound
	// events. When wired AND its FeatureFlag is enabled, post-side-
	// effect events (email.sent, email.approved) fire via
	// PublishBestEffortTx so the outbox write never rolls back the
	// already-committed SES.Send. Pre-side-effect HITL events stay on
	// the legacy `go publisher.Publish` path per the §5.12 design
	// limitation ("if we have it, keep it").
	outbox webhookpub.Outbox
	// eventsPool is the raw pgxpool used by the slice-6 events API.
	// Optional — when nil, GET/POST /api/v1/events return 404.
	eventsPool *pgxpool.Pool
	// metrics is the slice 10 observability surface. Defaulted to
	// NoOp; production wires telemetry.Log or a real backend.
	metrics telemetry.Metrics
}

// SetSubscriberStore wires the subscriber-store dependency after
// NewAPI. Same optional-setter convention as SetEnforcer / etc.
func (a *API) SetSubscriberStore(s *webhook.SubscriberStore) {
	a.subscriberStore = s
}

// SetPublisher wires the LEGACY in-process fan-out publisher. Kept
// during the slice-4→slice-11 rollout window. Safe to leave nil in
// tests; trigger sites no-op when it is.
func (a *API) SetPublisher(p webhookpub.Publisher) { a.publisher = p }

// SetOutbox wires the slice-4 transactional outbox. Used by the
// outbound /send handler for the email.sent event (post-side-effect:
// PublishBestEffortTx). Other trigger sites (HITL) continue to use
// the legacy publisher; they will migrate in a future slice if/when
// their handlers gain transactional plumbing.
func (a *API) SetOutbox(o webhookpub.Outbox) { a.outbox = o }

// SetMetrics wires the slice 10 observability backend. Default is
// telemetry.NoOp; production passes telemetry.NewLog() or a real
// counter backend.
func (a *API) SetMetrics(m telemetry.Metrics) { a.metrics = m }

// emit returns the wired metrics backend, defaulting to NoOp so
// handler-level instrumentation doesn't need nil guards everywhere.
func (a *API) emit() telemetry.Metrics {
	if a.metrics == nil {
		return telemetry.NoOp{}
	}
	return a.metrics
}

// publishAsync fires an event in a fresh goroutine so the handler's
// response is not blocked by webhook routing. Returns immediately.
// Uses context.Background() to detach from the request — the publisher
// is a best-effort post-commit fan-out, not part of the handler's
// success criteria.
func (a *API) publishAsync(e webhookpub.Event) {
	if a.publisher == nil {
		return
	}
	go a.publisher.Publish(context.Background(), e)
}

// publishSent fires email.sent via BOTH the legacy publisher (in a
// goroutine) AND the slice-4 transactional outbox path. The outbox
// uses PublishBestEffortTx because SES has already accepted the send —
// the messages row write + outbox write happen in one tx so the
// outbox commit is durable, but failure to write to the outbox
// MUST NOT roll back the already-committed messages row (and the
// already-sent email).
//
// During the rollout window (slice 4 → slice 11) both paths fire in
// parallel; the partial unique index on
// webhook_subscriber_deliveries(event_id, webhook_id) is what
// prevents double delivery once slice 2's worker picks up the new
// path.
//
// outMsg may be nil if CreateOutboundMessage failed earlier — when
// nil we skip the outbox path because there is no message row to
// transactionally co-commit with. The legacy goroutine still fires.
// shouldFireLegacy reports whether the legacy publisher.Publish
// goroutine MUST fire to deliver this event. Returns true when:
//
//   - the outbox is not wired (nil), OR
//   - the outbox flag is off (legacy is the sole delivery path), OR
//   - the outbox was the durable path BUT this attempt did not write
//     (caller passes outboxWrote=false), so legacy is the safety net.
//
// Returns false ONLY when the outbox successfully wrote the row —
// then legacy would produce a duplicate webhook POST that the partial
// unique index cannot dedupe (legacy writes event_id=NULL).
//
// Closes the second half of the C3 audit finding. The first half
// (suppress on duplicate) was the original concern; the second half
// surfaced in review: if outbox is enabled and the write FAILED,
// suppressing legacy too would silently drop the event. The PR
// description's "treat as ops issue" framing was a hand-wave — for
// HITL pending/reject events especially, a dropped event means the
// reviewer's webhook never fires.
func (a *API) shouldFireLegacy(outboxWrote bool) bool {
	if a.outbox == nil || !a.outbox.Enabled() {
		return true // legacy is the sole delivery path
	}
	return !outboxWrote // legacy is the fallback when outbox didn't write
}

func (a *API) publishSent(ctx context.Context, e webhookpub.Event, outMsg *identity.Message) {
	var outboxWrote bool
	if a.outbox != nil && outMsg != nil {
		// Use deterministic ID so MTA retries (no analog here for
		// /send, but rebill of an idempotency key counts as one)
		// dedupe through ON CONFLICT (id) DO NOTHING.
		e.ID = webhookpub.DeterministicEventID(outMsg.ID, e.Type)
		err := a.store.WithTx(ctx, func(tx pgx.Tx) error {
			// The messages row is already committed (CreateOutbound-
			// Message ran outside this tx because SES already
			// accepted the send and the row should be durable even
			// if the outbox write fails). So this tx only writes the
			// outbox row — best-effort by contract.
			outboxWrote = a.outbox.PublishBestEffortTx(ctx, tx, e)
			return nil
		})
		if err != nil {
			log.Printf("[api] outbox tx for email.sent err: %v", err)
			outboxWrote = false // tx-level failure also forces fallback
		}
	}
	a.emit().OutboxEventsPublished(e.Type)
	if a.shouldFireLegacy(outboxWrote) {
		a.publishAsync(e)
	}
}

// publishPendingApproval fires email.pending_approval via the outbox
// (PublishTx — pre-side-effect: the pending row hasn't been sent to
// SES yet) AND the legacy goroutine. pendingMsgID seeds the
// deterministic event id so /send retries with the same idempotency
// key don't fire duplicate events.
func (a *API) publishPendingApproval(ctx context.Context, e webhookpub.Event, pendingMsgID string) {
	var outboxWrote bool
	if a.outbox != nil && pendingMsgID != "" {
		e.ID = webhookpub.DeterministicEventID(pendingMsgID, e.Type)
		err := a.store.WithTx(ctx, func(tx pgx.Tx) error {
			return a.outbox.PublishTx(ctx, tx, e)
		})
		if err != nil {
			log.Printf("[api] outbox tx for email.pending_approval err: %v", err)
		} else {
			// err==nil means the tx committed AND PublishTx returned
			// nil — both are required for the row to be durably
			// recorded. (If the flag was off, PublishTx no-op'd and
			// returned nil; outbox.Enabled() guards that below in
			// shouldFireLegacy so we don't suppress legacy in that
			// case either.)
			outboxWrote = true
		}
	}
	a.emit().OutboxEventsPublished(e.Type)
	if a.shouldFireLegacy(outboxWrote) {
		a.publishAsync(e)
	}
}

// publishApproved fires email.approved via the outbox
// (PublishBestEffortTx — POST-side-effect: SES has already accepted
// the approved send) AND the legacy goroutine.
func (a *API) publishApproved(ctx context.Context, e webhookpub.Event, sentMsg *identity.Message) {
	var outboxWrote bool
	if a.outbox != nil && sentMsg != nil {
		e.ID = webhookpub.DeterministicEventID(sentMsg.ID, e.Type)
		err := a.store.WithTx(ctx, func(tx pgx.Tx) error {
			outboxWrote = a.outbox.PublishBestEffortTx(ctx, tx, e)
			return nil
		})
		if err != nil {
			log.Printf("[api] outbox tx for email.approved err: %v", err)
			outboxWrote = false
		}
	}
	a.emit().OutboxEventsPublished(e.Type)
	if a.shouldFireLegacy(outboxWrote) {
		a.publishAsync(e)
	}
}

// publishRejected fires email.rejected via the outbox (PublishTx —
// pre-side-effect: rejection is a row update, no SES involvement) AND
// the legacy goroutine.
func (a *API) publishRejected(ctx context.Context, e webhookpub.Event, rejectedMsgID string) {
	var outboxWrote bool
	if a.outbox != nil && rejectedMsgID != "" {
		e.ID = webhookpub.DeterministicEventID(rejectedMsgID, e.Type)
		err := a.store.WithTx(ctx, func(tx pgx.Tx) error {
			return a.outbox.PublishTx(ctx, tx, e)
		})
		if err != nil {
			log.Printf("[api] outbox tx for email.rejected err: %v", err)
		} else {
			outboxWrote = true
		}
	}
	a.emit().OutboxEventsPublished(e.Type)
	if a.shouldFireLegacy(outboxWrote) {
		a.publishAsync(e)
	}
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
// nil, /oauth2/* endpoints return 404 (matches the
// SetApprovalSigner / SetNotifier pattern of "optional capability,
// silently absent when not wired"). Operators who don't want OAuth
// enabled simply don't call this.
func (a *API) SetOAuthProvider(p fosite.OAuth2Provider) { a.oauthProvider = p }

// SetSigner wires the RS256 agent-token signer (Slice 5b). Optional — when nil
// or disabled, /.well-known/jwks.json serves an empty key set and the
// agent-identity token paths (later sub-slices) report "not enabled".
func (a *API) SetSigner(s *agentauth.Signer) { a.signer = s }

// SetOAuthStorage wires in the OAuth storage handle separately from
// the provider. The consent handler needs Pool() to begin a pgx tx
// that spans the agent-create (identity pkg) and the auth-code insert
// (fosite → oauth pkg). Provider-only callers (e.g. /token) don't need
// it, but it's required for /consent to work; setting one without the
// other is a misconfiguration the consent handler surfaces as a 503.
func (a *API) SetOAuthStorage(s *oauth.Storage) { a.oauthStorage = s }

// SetIdempotencyStore enables Idempotency-Key processing on the
// outbound /send and /reply endpoints. When nil (the default) the
// header is silently ignored — keeps the agent package usable in
// environments that don't have postgres wired or want to disable
// the feature. The cmd/e2a runtime always sets it.
func (a *API) SetIdempotencyStore(s *idempotency.Store) { a.idempotency = s }

// SetEnforcer wires in the resource-limits enforcer. When nil (the
// default) every check passes — handlers behave as if every user has
// unlimited capacity. The cmd/e2a runtime always sets it; tests that
// don't care about limits omit it and continue to work as before.
func (a *API) SetEnforcer(e limits.Enforcer) { a.enforcer = e }

// SendLimitAllow exposes the per-agent outbound rate limiter so the v1 httpapi
// layer shares the *same* token bucket as the legacy handlers (a caller hitting
// the limit via either path is counted once). key = agent id.
func (a *API) SendLimitAllow(key string) (bool, time.Duration) {
	return a.sendLimit.AllowWithRetryAfter(key)
}

// PollLimitAllow exposes the per-user read limiter (key = user id) and
// RegLimitAllow the per-IP registration limiter (key = client ip), each
// returning the IETF RateLimit snapshot so the v1 httpapi middleware can
// stamp RateLimit-Limit/Remaining/Reset. Both share the SAME buckets as the
// legacy handlers, so a caller is counted once across either surface.
func (a *API) PollLimitAllow(key string) (bool, time.Duration, int, int, int) {
	return a.pollLimit.AllowSnapshot(key)
}

func (a *API) RegLimitAllow(key string) (bool, time.Duration, int, int, int) {
	return a.regLimit.AllowSnapshot(key)
}

// SetUsageStore wires in the usage store used by handleGetMyLimits to
// surface the user's current counts (agents, domains, messages this
// month, storage bytes) alongside the resolved caps. Separate from the
// usage.UsageTracker (which is for recording events) so the dashboard
// read path can stay alive even when usage-tracking is otherwise off.
func (a *API) SetUsageStore(s *usage.Store) { a.usageStore = s }

// SetInternalAPISecret wires in the shared HMAC secret used to
// authenticate the /api/internal/limits/invalidate endpoint. When
// empty (default), that endpoint returns 503 — self-host operators
// who don't run a billing provisioner never need to configure it.
func (a *API) SetInternalAPISecret(s string) { a.internalAPISecret = s }

// SetBillingHookURL wires in the URL of an external billing service's
// user-event endpoint. When the user deletes their account, the API
// HMAC-signs a JSON payload and POSTs it there so the billing service
// can cancel the corresponding Stripe subscription. When empty (the
// self-host default), no hook fires — appropriate for deployments
// without a billing service. The same internal_api_secret is reused
// for the signature.
func (a *API) SetBillingHookURL(s string) { a.billingHookURL = s }

// SetDomainTeardownHook wires the per-domain SES deprovision enqueue run in the
// account-delete transaction (decision 4 / Slice 4). Optional.
func (a *API) SetDomainTeardownHook(h func(ctx context.Context, tx pgx.Tx, domain string) error) {
	a.domainTeardownHook = h
}

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
		regLimit:      ratelimit.New(1*time.Hour, 200),  // 200 registrations per IP per hour
		pollLimit:     ratelimit.New(1*time.Minute, 60), // 60 poll requests per user per minute
		feedbackLimit: ratelimit.New(1*time.Hour, 10),   // 10 feedback submissions per IP per hour
		dcrLimit:      ratelimit.New(1*time.Hour, 10),   // 10 OAuth client registrations per IP per hour
	}
}

func (a *API) RegisterRoutes(r *mux.Router) {
	// Catch-all 404/405 handlers so every error response leaves the
	// server as `text/plain; charset=utf-8` with a single-line body.
	// gorilla/mux's defaults are bare status codes with no body and no
	// Content-Type, which breaks client error handling and surfaced
	// during the e2e contract sweep — see tests/e2e-prod 07-error-contract.
	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	r.MethodNotAllowedHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})

	// Internal machine-to-machine endpoint: the external limits
	// provisioner (hosted billing sidecar) calls this to bust the
	// in-process limits cache for a given user immediately after it
	// writes account_limits. Authenticated by shared HMAC over the
	// request body; deliberately not advertised in OpenAPI.
	r.HandleFunc("/api/internal/limits/invalidate", a.handleInvalidateLimits).Methods("POST")

	// HITL magic-link pages. Human-facing token-gated HTML (not the JSON
	// API), opened from the approval email. GET renders a confirmation
	// page with a POST form; POST executes the action. Splitting this way
	// keeps email-client URL scanners (Gmail, Outlook Safe Links,
	// corporate mail gateways) from triggering side effects just by
	// previewing the link. Served under /v1 via the chi root's fallback to
	// this mux (these are raw HTML routes, not Huma operations).
	r.HandleFunc("/v1/approve", a.handleApproveMagicLinkGet).Methods("GET")
	r.HandleFunc("/v1/approve", a.handleApproveMagicLinkPost).Methods("POST")
	r.HandleFunc("/v1/reject", a.handleRejectMagicLinkGet).Methods("GET")
	r.HandleFunc("/v1/reject", a.handleRejectMagicLinkPost).Methods("POST")

	// --- Non-versioned operational endpoints ---
	r.HandleFunc("/api/health", a.handleHealth).Methods("GET", "HEAD")
	r.HandleFunc("/api/feedback", a.handleFeedback).Methods("POST")

	// OAuth 2.1 / RFC 6749 endpoints, root + unversioned (Slice 5b: renamed
	// from /oauth2/* to /oauth2/* to conform to the auth.md spec — no
	// back-compat alias). Handlers 404 when SetOAuthProvider wasn't called, so
	// registering unconditionally is safe.
	r.HandleFunc("/oauth2/authorize", a.handleOAuthAuthorize).Methods("GET")
	r.HandleFunc("/oauth2/consent", a.handleOAuthConsent).Methods("POST")
	r.HandleFunc("/oauth2/token", a.handleOAuthToken).Methods("POST")
	r.HandleFunc("/oauth2/revoke", a.handleOAuthRevoke).Methods("POST")
	r.HandleFunc("/oauth2/register", a.handleOAuthRegister).Methods("POST")
	r.HandleFunc("/oauth2/clients/{client_id}", a.handleOAuthGetClient).Methods("GET")
	r.HandleFunc("/.well-known/oauth-authorization-server", a.handleOAuthDiscovery).Methods("GET")
	// Public JWKS for verifying e2a-minted agent JWTs (Slice 5b). Always
	// registered; serves {"keys":[]} when no signing key is configured.
	r.HandleFunc("/.well-known/jwks.json", a.handleJWKS).Methods("GET")
	// auth.md agent-identity bootstrap (Slice 5b-2): present an agent-scoped
	// credential, receive an identity_assertion to exchange at /oauth2/token
	// (grant_type=jwt-bearer). 501 when no signing key is configured.
	r.HandleFunc("/agent/identity", a.handleAgentIdentity).Methods("POST")

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
// AuthenticateUser is the exported seam over authenticateUser so the v1
// httpapi layer (internal/httpapi) reuses the exact same credential-
// resolution path (API key, OAuth bearer, session cookie) instead of
// forking a second one. There is one place credentials are checked.
func (a *API) AuthenticateUser(r *http.Request) (*identity.User, error) {
	p, err := a.authenticatePrincipal(r)
	if err != nil {
		return nil, err
	}
	return p.User, nil
}

// AuthenticatePrincipal is the scope-aware seam (Slice 5a): same credential
// resolution as AuthenticateUser, but returns the full principal (user + scope
// + bound agent) so the v1 layer can enforce the hard scope ceiling. There is
// still exactly one place credentials are checked.
func (a *API) AuthenticatePrincipal(r *http.Request) (*identity.Principal, error) {
	return a.authenticatePrincipal(r)
}

// authenticateUser is the user-only convenience over authenticatePrincipal,
// retained for the legacy /api/v1 handlers that don't enforce scope.
func (a *API) authenticateUser(r *http.Request) (*identity.User, error) {
	p, err := a.authenticatePrincipal(r)
	if err != nil {
		return nil, err
	}
	return p.User, nil
}

func (a *API) authenticatePrincipal(r *http.Request) (*identity.Principal, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		bearer := stripBearerScheme(authHeader)
		// auth.md agent access token (Slice 5b-2): an RS256 JWT we minted,
		// resolving to an agent-scoped principal. Checked before the API-key
		// path since a JWT is never a valid API key. A bearer that looks like
		// our JWT but fails verification is rejected here (not fall-through).
		if p, ours, err := a.resolveAgentAccessToken(r, bearer); ours {
			if err != nil {
				return nil, err
			}
			return p, nil
		}
		if strings.HasPrefix(bearer, oauth.AccessTokenPrefix) {
			u, err := a.lookupUserByOAuthToken(r, bearer)
			if err != nil {
				return nil, err
			}
			// OAuth (fosite) access tokens are account-scoped until they carry
			// scope claims. Until then they behave as today.
			return &identity.Principal{User: u, Scope: identity.ScopeAccount}, nil
		}
		return a.store.GetPrincipalByAPIKey(r.Context(), bearer)
	}
	// Fall back to session cookie auth — the web dashboard owner, account scope.
	if a.userAuth != nil {
		if user := a.userAuth.AuthenticateRequest(r); user != nil {
			return &identity.Principal{User: user, Scope: identity.ScopeAccount}, nil
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
	w.Header().Set("WWW-Authenticate", a.authChallenge(r, err))
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

// authChallenge builds the RFC 6750 §3 WWW-Authenticate value for a 401 on a
// Bearer-accepting endpoint, given the request and the auth error that caused
// the rejection. Every 401 advertises the bare `Bearer realm="e2a"`; OAuth-
// bearer failures additionally carry the §3.1 error params (see writeAuthError
// docstring for the existence-oracle rationale). Shared by the legacy mux
// (writeAuthError) and the v1 surface (WWWAuthenticateChallenge) so both
// surfaces emit byte-identical challenges from one definition.
func (a *API) authChallenge(r *http.Request, err error) string {
	bearer := stripBearerScheme(r.Header.Get("Authorization"))
	isOAuthFailure := errors.Is(err, errOAuthBearerInvalid) || strings.HasPrefix(bearer, oauth.AccessTokenPrefix)
	if !isOAuthFailure {
		return `Bearer realm="e2a"`
	}
	desc := "the access token is invalid"
	if errors.Is(err, fosite.ErrTokenExpired) {
		desc = "the access token has expired"
	}
	return `Bearer realm="e2a", error="invalid_token", error_description="` + desc + `"`
}

// WWWAuthenticateChallenge is the v1-surface seam (internal/httpapi) for the
// RFC 6750 challenge. The legacy mux had the auth error in hand at the 401 site
// (writeAuthError); the v1 layer rejects via the canonical envelope and reaches
// this from a response wrapper that only knows the status was 401, so we re-run
// the same credential resolution to recover the typed error and classify the
// challenge identically (expired vs invalid). 401s are rare, so the extra
// resolution is cheap, and routing it through the one auth path keeps the
// surfaces from diverging.
func (a *API) WWWAuthenticateChallenge(r *http.Request) string {
	_, err := a.authenticatePrincipal(r)
	return a.authChallenge(r, err)
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

// --- Domain Management ---

// dnsRecordCheck holds the per-record probe results for the verify
// endpoint. Values are "found" / "missing" — DKIM additionally supports
// "deferred" when per-domain DKIM hasn't been provisioned for the
// domain yet (legacy rows pre-migration 014).
type dnsRecordCheck struct {
	TXTFound bool
	MX       string
	SPF      string
	DKIM     string
}

// checkDomainRecords runs the three per-record probes plus the TXT
// ownership check. In dev mode, all checks short-circuit to "found" /
// true so domain verification flows can be exercised without real DNS.
//
// Probe semantics:
//   - TXT: any TXT record contains the verification token (ownership proof)
//   - MX: any MX record points at smtpDomain (mail routing)
//   - SPF: any TXT record begins with v=spf1 and contains the relay's
//     send domain. We accept either smtpDomain or just the bare domain
//     as a substring — operators commonly use either form.
//   - DKIM: when dkimSelector + dkimPublicKey are present, looks up
//     "{selector}._domainkey.{domain}" and matches the stored public
//     key. Domains without a stored keypair report "deferred" — these
//     are pre-migration rows that the next claim would key.
//
// DNSRecordCheck is the exported diagnostic from CheckDomainRecords.
type DNSRecordCheck struct {
	TXTFound bool
	MX       string
	SPF      string
	DKIM     string
}

// CheckDomainRecords is the exported seam over checkDomainRecords so the v1
// httpapi layer reuses the exact DNS-probe logic for domain verification.
func CheckDomainRecords(domain, smtpDomain, verificationToken, dkimSelector, dkimPublicKey string, production bool) DNSRecordCheck {
	c := checkDomainRecords(domain, smtpDomain, verificationToken, dkimSelector, dkimPublicKey, production)
	return DNSRecordCheck{TXTFound: c.TXTFound, MX: c.MX, SPF: c.SPF, DKIM: c.DKIM}
}

func checkDomainRecords(domain, smtpDomain, verificationToken, dkimSelector, dkimPublicKey string, production bool) dnsRecordCheck {
	if !production {
		dkimState := "deferred"
		if dkimSelector != "" && dkimPublicKey != "" {
			// Dev short-circuit treats a stored keypair as "found" so
			// the Get-started flow can show the DKIM row populated.
			dkimState = "found"
		}
		return dnsRecordCheck{
			TXTFound: true,
			MX:       "found",
			SPF:      "found",
			DKIM:     dkimState,
		}
	}
	check := dnsRecordCheck{DKIM: "deferred", MX: "missing", SPF: "missing"}

	// TXT ownership + SPF live in the same record set
	if txts, err := net.LookupTXT(domain); err == nil {
		for _, txt := range txts {
			if strings.Contains(txt, verificationToken) {
				check.TXTFound = true
			}
			if strings.HasPrefix(strings.ToLower(txt), "v=spf1") &&
				strings.Contains(strings.ToLower(txt), strings.ToLower(smtpDomain)) {
				check.SPF = "found"
			}
		}
	}

	if mxs, err := net.LookupMX(domain); err == nil {
		for _, mx := range mxs {
			if strings.EqualFold(strings.TrimSuffix(mx.Host, "."), smtpDomain) {
				check.MX = "found"
				break
			}
		}
	}

	// DKIM: only probe if we have a stored keypair for the domain. The
	// expected DNS name is "{selector}._domainkey.{domain}" with a
	// "v=DKIM1; k=rsa; p=<base64>" value. We treat the record as
	// "found" if any TXT at that name contains a "p=" payload matching
	// the stored public key — operators sometimes paste extra tags
	// (s=, t=, etc.) which we tolerate.
	if dkimSelector != "" && dkimPublicKey != "" {
		check.DKIM = "missing"
		dkimName := fmt.Sprintf("%s._domainkey.%s", dkimSelector, domain)
		if txts, err := net.LookupTXT(dkimName); err == nil {
			for _, txt := range txts {
				if got := dkim.ExtractPublicKeyFromTXT(txt); got != "" && got == dkimPublicKey {
					check.DKIM = "found"
					break
				}
			}
		}
	}

	return check
}

// holdForApproval persists a fully composed outbound SendRequest as a
// pending_approval message and writes a 202 response. It is the shared
// branch taken by handleSendEmail, handleReplyToMessage, and
// handleSendTestEmail when agent.HITLEnabled is true.
//
// replyToEmailMessageID is the inbound Message-ID being replied to, or "".
// msgType is one of "send", "reply", "test", or "forward".
// errHoldAttachments is returned by HoldForApprovalCore when the attachments
// fail to serialize, so callers can map it to the same 500 the legacy handler
// produced ("failed to serialize attachments") vs the create-failure 500.
var errHoldAttachments = errors.New("failed to serialize attachments")

// HoldForApprovalCore is the HTTP-free core of the HITL hold: it persists the
// pending outbound message, fires the async reviewer notification, and
// publishes the pending-approval event, returning the held message. Both the
// legacy handler and the v1 httpapi layer call it so there is exactly one
// hold-and-notify path (api-v1-redesign — outbound extraction).
func (a *API) HoldForApprovalCore(ctx context.Context, agent *identity.AgentIdentity, req outbound.SendRequest, msgType, replyToEmailMessageID string) (*identity.Message, error) {
	var attachmentsJSON []byte
	if len(req.Attachments) > 0 {
		b, err := json.Marshal(req.Attachments)
		if err != nil {
			log.Printf("[api] hitl: marshal attachments: %v", err)
			return nil, errHoldAttachments
		}
		attachmentsJSON = b
	}

	msg, err := a.store.CreatePendingOutboundMessage(
		ctx, agent.ID,
		req.To, req.CC, req.BCC,
		req.Subject, req.Body, req.HTMLBody,
		attachmentsJSON,
		msgType, req.ConversationID, replyToEmailMessageID,
		agent.HITLTTLSeconds,
	)
	if err != nil {
		log.Printf("[api] hitl: create pending message: agent=%s err=%v", agent.ID, err)
		return nil, err
	}

	slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
	log.Printf("[mail:%s] dir=outbound type=%s status=pending_approval from=%s to=%v slug=%s conv_id=%s subject=%q approval_expires_at=%s",
		msg.ID, msgType, agent.EmailAddress(), req.To, slug, req.ConversationID, req.Subject, msg.ApprovalExpiresAt.Format(time.RFC3339))

	// Fire the reviewer notification asynchronously. Failures are logged
	// inside the notifier and must never block the response — the pending
	// row is already persisted and the expiration worker will finalize it
	// even if every notification email bounces.
	if a.notifier != nil {
		a.notifier.NotifyPendingApprovalAsync(msg, agent)
	}

	a.publishPendingApproval(ctx, a.buildPendingApprovalEvent(agent, msg, req, msgType), msg.ID)
	return msg, nil
}

// OutboundResult is the HTTP-free outcome of DeliverOutbound.
type OutboundResult struct {
	Held              bool
	PendingMessageID  string
	ApprovalExpiresAt *time.Time
	MessageID         string // provider/loopback id when sent
	Method            string // "smtp" | "loopback"
}

// OutboundError carries an HTTP status + message so both the legacy handler
// (http.Error) and the v1 httpapi layer (error envelope) render it
// consistently. Code is the machine-branchable code the v1 layer uses.
type OutboundError struct {
	Status int
	Code   string
	Msg    string
}

func (e *OutboundError) Error() string { return e.Msg }

// checkSuppression rejects a send when any recipient (To/CC/BCC) is on the
// tenant's suppression list (decision 9). Returns a structured
// recipient_suppressed 422. Fails OPEN on a store error — a suppression-DB
// hiccup must not block legitimate mail; the list is protective, not a hard
// gate, and the storage trigger / SES account-level list backstop it.
func (a *API) checkSuppression(ctx context.Context, userID string, req outbound.SendRequest) *OutboundError {
	addrs := make([]string, 0, len(req.To)+len(req.CC)+len(req.BCC))
	addrs = append(addrs, req.To...)
	addrs = append(addrs, req.CC...)
	addrs = append(addrs, req.BCC...)
	if len(addrs) == 0 {
		return nil
	}
	suppressed, err := a.store.SuppressedAddresses(ctx, userID, addrs)
	if err != nil {
		log.Printf("[api] suppression check failed (allowing send): %v", err)
		return nil
	}
	if len(suppressed) > 0 {
		return &OutboundError{http.StatusUnprocessableEntity, "recipient_suppressed",
			"recipient(s) on the suppression list: " + strings.Join(suppressed, ", ") +
				" — remove via DELETE /v1/account/suppressions/{address}"}
	}
	return nil
}

// actionGateHold computes the Slice 7b trust-gated hold decision. The caller
// has already confirmed HITL is enabled; this only chooses based on the
// agent's hitl_mode and the trust signals.
//
//   - referenced: the inbound message this action reacts to (reply/forward);
//     nil for a cold send/test, which has no untrusted input to react to.
//   - untrusted input (today): the referenced inbound's DMARC verdict is not a
//     pass — i.e. we can't confirm it really came from the claimed sender. A
//     missing verdict is treated as untrusted (fail-closed). This is the
//     pluggable seam: a content-level prompt-injection verdict can later OR
//     into this signal without touching actiongate.
//   - high impact: a recipient reaches a domain that wasn't a participant of
//     the referenced inbound (reply to a new party / forward to a third party).
func actionGateHold(agent *identity.AgentIdentity, req outbound.SendRequest, referenced *identity.Message) actiongate.Decision {
	mode := agent.HITLMode
	if mode == "" {
		mode = actiongate.ModeAll
	}
	untrusted := referenced == nil || referenced.Auth == nil ||
		referenced.Auth.DMARC.Status != emailauth.StatusPass

	// Trust anchor for the high-impact check is the agent's OWN verified domain
	// — NOT the referenced inbound's From/To/Cc. high_impact only ever holds on
	// an untrusted (DMARC-fail) inbound, whose headers are attacker-controlled:
	// a spoofer who adds `Cc: exfil@evil.com` would otherwise pre-authorize
	// their own exfil domain as a "participant" and slip a forward past the gate
	// (adversarial review). So an untrusted inbound expands the trusted set by
	// nothing; any recipient off the agent's domain is high-impact. (Reusing a
	// *trusted* prior thread's participants is a possible future refinement.)
	participants := []string{agent.EmailAddress()}

	recipients := make([]string, 0, len(req.To)+len(req.CC)+len(req.BCC))
	recipients = append(recipients, req.To...)
	recipients = append(recipients, req.CC...)
	recipients = append(recipients, req.BCC...)

	return actiongate.Evaluate(mode, referenced != nil, untrusted, actiongate.HighImpact(participants, recipients))
}

// DeliverOutbound is the shared send/reply/forward delivery tail, HTTP-free:
// HITL hold (HoldForApprovalCore), else self-send loopback, else SES send +
// record outbound + publish sent event. The caller has already authed,
// resolved + ownership-checked the agent, rate-limited, domain-verified, run
// the message-send cap, and built the SendRequest with its final Subject /
// ConversationID. Both the legacy handlers and the v1 layer call this so the
// live delivery path exists exactly once (api-v1-redesign — outbound
// extraction). On a nil-error return the side effect has committed, so the
// idempotency key must be Completed (cached), never Released.
func (a *API) DeliverOutbound(ctx context.Context, user *identity.User, agent *identity.AgentIdentity, req outbound.SendRequest, msgType, replyToEmailMessageID string, referenced *identity.Message) (*OutboundResult, *OutboundError) {
	// Suppression enforcement (decision 9 / Slice 4b): fail fast if any
	// recipient is on this tenant's suppression list. Enforced fresh on every
	// attempt and NOT cached under the idempotency key (it's a clearable state,
	// released like every other error).
	if supErr := a.checkSuppression(ctx, user.ID, req); supErr != nil {
		return nil, supErr
	}

	// Trust-gated action authorization (decision 10 / Slice 7b): when HITL is
	// on, the sub-mode decides WHAT is held — all outbound (hitl_mode=all), or
	// only a high-impact action taken on untrusted inbound (high_impact).
	// `referenced` is the inbound message this action reacts to (reply/forward);
	// nil for a cold send.
	if agent.HITLEnabled && actionGateHold(agent, req, referenced).Hold {
		msg, err := a.HoldForApprovalCore(ctx, agent, req, msgType, replyToEmailMessageID)
		if err != nil {
			if errors.Is(err, errHoldAttachments) {
				return nil, &OutboundError{http.StatusInternalServerError, "internal_error", "failed to serialize attachments"}
			}
			return nil, &OutboundError{http.StatusInternalServerError, "internal_error", "failed to hold message for approval"}
		}
		return &OutboundResult{Held: true, PendingMessageID: msg.ID, ApprovalExpiresAt: msg.ApprovalExpiresAt}, nil
	}

	// Record usage (side-effect only — never block on quota; the cap
	// pre-check is the gate).
	if _, err := a.usage.RecordAndCheck(ctx, user.ID, agent.ID, agent.Domain, "outbound"); err != nil {
		log.Printf("[api] usage recording error: %v", err)
	}

	if isSelfSend(req, agent.EmailAddress()) {
		providerID, err := a.performSelfSend(ctx, agent, req, msgType)
		if err != nil {
			log.Printf("[api] self-send failed: agent=%s error=%v", agent.EmailAddress(), err)
			return nil, &OutboundError{http.StatusInternalServerError, "internal_error", "self-send failed"}
		}
		slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
		log.Printf("[mail] dir=outbound type=%s method=loopback from=%s to=%s slug=%s conv_id=%s subject=%q provider_id=%s", msgType, agent.EmailAddress(), agent.EmailAddress(), slug, req.ConversationID, req.Subject, providerID)
		return &OutboundResult{MessageID: providerID, Method: "loopback"}, nil
	}

	result, err := a.sender.Send(agent, req)
	if err != nil {
		if outbound.IsValidationError(err) {
			return nil, &OutboundError{http.StatusBadRequest, "invalid_request", err.Error()}
		}
		log.Printf("[api] send failed: agent=%s to=%v error=%v", agent.Domain, req.To, err)
		return nil, &OutboundError{http.StatusInternalServerError, "internal_error", fmt.Sprintf("send failed: %v", err)}
	}
	outMsg, err := a.store.CreateOutboundMessage(ctx, agent.ID, result.To, result.CC, result.BCC, req.Subject, msgType, result.Method, result.MessageID, req.ConversationID)
	if err != nil {
		log.Printf("[api] failed to record outbound message: %v", err)
	}
	slug, _, _ := strings.Cut(agent.EmailAddress(), "@")
	if outMsg != nil {
		// Record the delivery lifecycle (decision 9): delivery_status='sent',
		// which From identity was used, and the per-recipient breakdown the SES
		// notifications consumer transitions as feedback arrives.
		if err := a.store.MarkMessageSent(ctx, outMsg.ID, result.SentAs, result.To, result.CC, result.BCC); err != nil {
			log.Printf("[api] mark sent (delivery_status): %v", err)
		}
		log.Printf("[mail:%s] dir=outbound type=%s from=%s to=%v slug=%s conv_id=%s subject=%q", outMsg.ID, msgType, agent.EmailAddress(), result.To, slug, req.ConversationID, req.Subject)
	}
	a.publishSent(ctx, a.buildSentEvent(agent, outMsg, result, req, msgType), outMsg)
	return &OutboundResult{MessageID: result.MessageID, Method: result.Method}, nil
}

// SendTestCore composes and sends (or HITL-holds) a platform test email to
// the agent's own address. HTTP-free; shared by the legacy handler and the v1
// layer. The caller has already authed, resolved + owned the agent,
// domain-verified, and run the message-send cap.
func (a *API) SendTestCore(ctx context.Context, agent *identity.AgentIdentity) (*OutboundResult, *OutboundError) {
	envelopeFrom := fmt.Sprintf("noreply@%s", a.fromDomain)
	headerFrom := fmt.Sprintf("%q <%s>", "e2a", envelopeFrom)
	to := []string{agent.EmailAddress()}
	subject := "Test email from e2a"
	body := fmt.Sprintf("This is a test email for %s.\n\nYour agent is set up and ready to receive emails.", agent.EmailAddress())

	// A platform test is a cold self-send (no referenced inbound), so in
	// hitl_mode=high_impact it isn't held (nothing untrusted to react to); in
	// hitl_mode=all it's held like any outbound.
	testReq := outbound.SendRequest{To: to, Subject: subject, Body: body}
	if agent.HITLEnabled && actionGateHold(agent, testReq, nil).Hold {
		msg, err := a.HoldForApprovalCore(ctx, agent, testReq, "test", "")
		if err != nil {
			return nil, &OutboundError{http.StatusInternalServerError, "internal_error", "failed to hold message for approval"}
		}
		return &OutboundResult{Held: true, PendingMessageID: msg.ID, ApprovalExpiresAt: msg.ApprovalExpiresAt}, nil
	}

	message, err := outbound.ComposeMessage(headerFrom, to, nil, subject, body, "text/plain", "", nil, a.fromDomain, "", "")
	if err != nil {
		log.Printf("[api] compose test email failed: %v", err)
		return nil, &OutboundError{http.StatusInternalServerError, "internal_error", "failed to compose test email"}
	}
	messageID, err := a.smtpRelay.Send(envelopeFrom, to, message)
	if err != nil {
		log.Printf("[api] send test email failed: %v", err)
		return nil, &OutboundError{http.StatusInternalServerError, "internal_error", fmt.Sprintf("failed to send test email: %v", err)}
	}
	log.Printf("[api] test email sent to %s (message_id=%s)", agent.EmailAddress(), messageID)
	return &OutboundResult{MessageID: messageID, Method: "smtp"}, nil
}

// --- Send Email ---

// ForwardRequest is the JSON body for /api/v1/agents/{email}/messages/{id}/forward.
type ForwardRequest struct {
	To             []string              `json:"to"`
	CC             []string              `json:"cc,omitempty"`
	BCC            []string              `json:"bcc,omitempty"`
	Body           string                `json:"body,omitempty"`
	HTMLBody       string                `json:"html_body,omitempty"`
	ConversationID string                `json:"conversation_id,omitempty"`
	Attachments    []outbound.Attachment `json:"attachments,omitempty"`
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"status": "ok"})
}

func (a *API) handleFeedback(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if ok, retryAfter := a.feedbackLimit.AllowWithRetryAfter(ip); !ok {
		writeTooManyRequests(w, retryAfter, "rate limit exceeded — max 10 feedback submissions per hour per IP")
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

// Label validation constants. Public so tests can reference them.
const (
	// MaxLabelLength bounds a single label's length to keep the GIN
	// index entries small and prevent multi-KB tags from being smuggled
	// into the array.
	MaxLabelLength = 64

	// MaxLabelsPerOp caps the per-request add/remove list size.
	// Modeled on Gmail's 100/100 cap. Bigger than AgentMail's
	// (unspecified) but defensive enough that one PATCH can't try
	// to set thousands of labels.
	MaxLabelsPerOp = 50

	// LabelSystemPrefix marks server-applied system labels. User
	// writes that try to set a label starting with this prefix are
	// rejected with 400 — the namespace is reserved so future system
	// labels (auto-tagged, hitl-approved, …) don't collide with user
	// tags.
	LabelSystemPrefix = "e2a:"
)

// normalizeAndValidateLabel canonicalizes a single label and rejects it
// if it would violate the labels invariants:
//   - lowercase
//   - charset `[a-z0-9:_-]+` (colon allowed for namespacing, but only
//     the server may set `e2a:*`)
//   - 1..MaxLabelLength chars after trimming
//
// Returns the normalized form on success. Lowercasing is the only
// transformation — colons / dashes / underscores stay as-is so a label
// from a query param is byte-identical to the same label set via PATCH.
func normalizeAndValidateLabel(raw string, allowSystemPrefix bool) (string, error) {
	l := strings.ToLower(strings.TrimSpace(raw))
	if l == "" {
		return "", errors.New("label must not be empty")
	}
	if len(l) > MaxLabelLength {
		return "", fmt.Errorf("label too long (max %d chars)", MaxLabelLength)
	}
	for _, r := range l {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == ':':
		default:
			return "", fmt.Errorf("label %q has invalid character; allowed: a-z 0-9 : - _", l)
		}
	}
	if !allowSystemPrefix && strings.HasPrefix(l, LabelSystemPrefix) {
		return "", fmt.Errorf("labels starting with %q are reserved for system use", LabelSystemPrefix)
	}
	return l, nil
}

// NormalizeAndValidateLabelList runs each entry through
// normalizeAndValidateLabel, dedups within the slice, and rejects if
// the slice is empty after trimming or exceeds the per-op cap. The
// `op` argument is used only to shape the error message.
func NormalizeAndValidateLabelList(raw []string, op string) ([]string, error) {
	if len(raw) > MaxLabelsPerOp {
		return nil, fmt.Errorf("%s_labels exceeds per-request cap of %d", op, MaxLabelsPerOp)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		l, err := normalizeAndValidateLabel(r, false)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	return out, nil
}

// validateRecipients ensures every entry in the joined To/CC/BCC slices
// is a syntactically valid RFC 5322 address. We use net/mail.ParseAddress
// rather than a custom regex because it handles bare local@domain,
// display-name forms ("Bob Smith <bob@x.com>"), quoted local parts, and
// IDN domains uniformly. Semantic validity (the mailbox actually exists,
// the user can receive) is checked downstream by the SMTP relay on a
// per-recipient basis — that's the right layer for best-effort delivery.
// The API layer's job is only to reject syntactic garbage that could
// never route through SMTP at all (no @, whitespace, etc.).
//
// Returns the first invalid address found, with a wrapped parser error
// suitable for surfacing to the caller. Empty slices are not an error
// here — handlers already enforce "at least one recipient" separately
// with a more specific message.
// ValidateRecipients is the exported seam over validateRecipients so the v1
// httpapi layer reuses the same RFC 5322 recipient check.
func ValidateRecipients(groups ...[]string) error { return validateRecipients(groups...) }

func validateRecipients(groups ...[]string) error {
	for _, group := range groups {
		for _, addr := range group {
			if addr == "" {
				return errors.New("recipient address must not be empty")
			}
			if _, err := mail.ParseAddress(addr); err != nil {
				return fmt.Errorf("invalid recipient %q: %w", addr, err)
			}
		}
	}
	return nil
}

// validateDomain runs the IDNA "Lookup" profile against a user-supplied
// domain string. Lookup is the strictest of the four IDNA profiles and
// is what DNS resolvers themselves apply when converting a name into
// a wire-format query — it rejects whitespace, control characters,
// invalid label combinations, and over-length names; converts Unicode
// IDN to Punycode along the way. We additionally require at least one
// period because IDNA accepts bare labels like "localhost" which
// aren't legal as a user-claimable domain.
//
// Returns the ASCII-normalized form on success so callers can persist
// the canonical wire-format (e.g. "xn--e1afmkfd.xn--p1ai" for
// "пример.рф") instead of the raw input.
// ValidateDomain is the exported seam over validateDomain so the v1 httpapi
// layer reuses the exact IDN/punycode normalization + label-length checks
// instead of replicating the security-relevant parsing. Returns the
// normalized (Punycode) form.
func ValidateDomain(domain string) (string, error) {
	return validateDomain(domain)
}

func validateDomain(domain string) (string, error) {
	if domain == "" {
		return "", errors.New("domain is required")
	}
	if !strings.Contains(domain, ".") {
		return "", errors.New("domain must contain at least one period")
	}
	ascii, err := idna.Lookup.ToASCII(domain)
	if err != nil {
		return "", fmt.Errorf("invalid domain: %w", err)
	}
	// IDNA's VerifyDNSLength option only enforces the 253-char total
	// length; the 63-char per-label DNS limit (RFC 1035) is not
	// checked. Walk labels explicitly so we don't accept domains that
	// would fail downstream at the resolver.
	for _, label := range strings.Split(ascii, ".") {
		if label == "" {
			return "", errors.New("invalid domain: empty label")
		}
		if len(label) > 63 {
			return "", errors.New("invalid domain: label exceeds 63 characters")
		}
	}
	return ascii, nil
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
