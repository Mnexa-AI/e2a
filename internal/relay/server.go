package relay

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/emailauth"
	"github.com/Mnexa-AI/e2a/internal/headers"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/inboundpolicy"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/piguard"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/Mnexa-AI/e2a/internal/ws"
	"github.com/emersion/go-smtp"
	"github.com/jackc/pgx/v5"
)

type Server struct {
	smtpServer *smtp.Server
	store      *identity.Store
	signer     *headers.Signer
	// outbox is the transactional event log. The inbound trigger writes the
	// messages row and the webhook_events row in a single transaction (per
	// design §4.2); the drain fans out to subscribers and enqueues River jobs.
	outbox   webhookpub.Outbox
	hub      *ws.Hub
	usage    usage.UsageTracker
	enforcer limits.Enforcer // optional; when nil, inbound caps are not enforced
	// screen is the content-screening engine (Slice 4). Runs the per-agent inbound
	// scan when inbound_scan='on'. Always non-nil (built with the dependency-free
	// heuristics detector); external providers plug in behind the same interface.
	screen     *piguard.Engine
	smtpDomain string
	// outboundFromDomain is the domain used in envelope MAIL FROM for mail we
	// originate (e.g. "send.e2a.dev"). Inbound messages whose envelope MAIL FROM
	// matches this domain are trusted same-platform traffic and may surface
	// X-E2A-Conversation-ID directly; external senders fall back to the
	// In-Reply-To lookup so they cannot forge conversation IDs.
	outboundFromDomain string
	// inboundAsync routes inbound through the queue-first River pipeline
	// (E2A_INBOUND_MODE=async): the session durably accepts to inbound_intake +
	// enqueues a processing job before 250 instead of processing inline. A nil
	// inboundEnq forces the synchronous path regardless (fail-safe).
	inboundAsync bool
	inboundEnq   InboundEnqueuer
}

// InboundEnqueuer inserts the inbound_process job in the SMTP accept-tx (the same
// transaction as the inbound_intake insert). *inboundprocess.Jobs satisfies it.
// Injected via SetInboundEnqueuer; nil keeps inbound on the synchronous path.
type InboundEnqueuer interface {
	EnqueueInboundProcessTx(ctx context.Context, tx pgx.Tx, intakeID string) (int64, error)
}

// SetInboundEnqueuer wires the shared River client's inbound enqueuer, enabling the
// queue-first accept path when E2A_INBOUND_MODE=async.
func (s *Server) SetInboundEnqueuer(e InboundEnqueuer) { s.inboundEnq = e }

// SetOutbox wires the transactional outbox. The inbound trigger commits the
// messages row and the webhook_events outbox row in a single transaction (per
// design §4.2); the drain fans out and enqueues River delivery jobs.
func (s *Server) SetOutbox(o webhookpub.Outbox) { s.outbox = o }

// SetEnforcer wires in the resource-limits enforcer used to reject
// inbound recipients whose owner has hit the message-flow or storage
// cap. When nil (the default) every RCPT TO is accepted as far as the
// limits subsystem is concerned — handy for tests and for self-host
// operators who run without limits enabled. The cmd/e2a runtime always
// sets it.
func (s *Server) SetEnforcer(e limits.Enforcer) { s.enforcer = e }

func NewServer(cfg *config.Config, store *identity.Store, signer *headers.Signer, usage usage.UsageTracker, hub *ws.Hub) *Server {
	s := &Server{
		store:              store,
		signer:             signer,
		hub:                hub,
		usage:              usage,
		screen:             buildScreenEngine(),
		smtpDomain:         cfg.SMTP.Domain,
		outboundFromDomain: cfg.OutboundSMTP.FromDomain,
		inboundAsync:       cfg.Inbound.Mode == "async",
	}

	be := &backend{relay: s}
	smtpSrv := smtp.NewServer(be)
	smtpSrv.Addr = cfg.SMTP.ListenAddr
	smtpSrv.Domain = cfg.SMTP.Domain
	smtpSrv.ReadTimeout = 30 * time.Second
	smtpSrv.WriteTimeout = 30 * time.Second
	smtpSrv.MaxMessageBytes = 10 * 1024 * 1024 // 10MB
	smtpSrv.MaxRecipients = 50
	smtpSrv.AllowInsecureAuth = !cfg.IsProduction()

	if cfg.SMTP.TLSCert != "" && cfg.SMTP.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.SMTP.TLSCert, cfg.SMTP.TLSKey)
		if err != nil {
			log.Fatalf("failed to load TLS cert: %v", err)
		}
		smtpSrv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}

	s.smtpServer = smtpSrv
	return s
}

// geminiDetectorTimeout is the per-detector timeout used when the Gemini detector
// is wired in, wider than the Engine's default (5s) so the retry/backoff schedule
// in piguard/gemini.go (up to geminiDefaultMaxRetries retries) has room to run
// instead of being cut off by the engine before it can fire.
const geminiDetectorTimeout = 10 * time.Second

// geminiDetectorEnabled reports whether buildScreenEngine should even attempt to
// construct the Gemini detector. Defaults to true — the existing behavior, where
// Gemini is enabled purely by GEMINI_API_KEY/GOOGLE_API_KEY being present — unless
// E2A_GEMINI_DETECTOR_ENABLED is explicitly set to "false". This is an operator
// kill-switch/A-B toggle independent of the credential: it lets you disable Gemini
// (isolating whether it or heuristics is driving a given block/review outcome, or
// rolling back without touching secrets) without having to remove the API key.
func geminiDetectorEnabled() bool {
	return os.Getenv("E2A_GEMINI_DETECTOR_ENABLED") != "false"
}

// buildScreenEngine constructs the piguard screening engine for inbound mail. The
// heuristics detector is always included. The Gemini detector is added when
// geminiDetectorEnabled() and GEMINI_API_KEY or GOOGLE_API_KEY is set in the
// environment; its prompt only classifies inbound content, so this engine
// (inbound-only, unlike buildAgentScreenEngine in internal/agent/api.go) is where
// it belongs.
func buildScreenEngine() *piguard.Engine {
	detectors := []piguard.Detector{piguard.NewHeuristicsDetector()}
	cfg := piguard.EngineConfig{}
	if geminiDetectorEnabled() {
		if d, err := piguard.NewGeminiDetector(piguard.GeminiConfig{}); err == nil {
			detectors = append(detectors, d)
			cfg.Timeout = geminiDetectorTimeout
			log.Printf("[piguard] Gemini detector enabled (model: %s)", d.Model())
		}
	} else {
		log.Printf("[piguard] Gemini detector disabled via E2A_GEMINI_DETECTOR_ENABLED=false")
	}
	return piguard.NewEngine(cfg, detectors...)
}

func (s *Server) ListenAndServe() error {
	log.Printf("SMTP relay listening on %s", s.smtpServer.Addr)
	return s.smtpServer.ListenAndServe()
}

func (s *Server) Close() error {
	return s.smtpServer.Close()
}

// SMTP backend implementation

type backend struct {
	relay *Server
}

func (b *backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	var remoteIP net.IP
	if addr, ok := c.Conn().RemoteAddr().(*net.TCPAddr); ok {
		remoteIP = addr.IP
	}
	sid := newSessionID()
	log.Printf("[%s] new session from %s", sid, remoteIP)
	return &session{relay: b.relay, id: sid, remoteIP: remoteIP}, nil
}

func newSessionID() string {
	b := make([]byte, 4)
	crand.Read(b)
	return hex.EncodeToString(b)
}

type session struct {
	relay      *Server
	id         string
	from       string
	recipients []string
	remoteIP   net.IP
}

func (s *session) AuthPlain(username, password string) error {
	return nil // Auth handled at identity layer, not SMTP layer
}

func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	log.Printf("[%s] [%s] MAIL FROM", s.id, from)
	return nil
}

func (s *session) Rcpt(to string, opts *smtp.RcptOptions) error {
	log.Printf("[%s] [%s] RCPT TO: %s", s.id, s.from, to)

	// Reject unknown or unverified recipients at SMTP level.
	// The sender's mail server will generate a bounce notification.
	ctx := context.Background()
	agent, err := s.relay.resolveAgent(ctx, to)
	if err != nil {
		log.Printf("[%s] [%s] rejecting %s: no agent found", s.id, s.from, to)
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "recipient not found"}
	}
	if !agent.DomainVerified {
		log.Printf("[%s] [%s] rejecting %s: domain not verified", s.id, s.from, to)
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "recipient not found"}
	}

	// Reject at SMTP envelope time if the recipient's owner has hit the
	// message-flow or storage cap. 552 (mailbox quota exceeded, RFC 5321
	// §4.2.2) tells the upstream MTA to bounce the message back to the
	// original sender with a deliverable error rather than retry. We
	// fail open on enforcer errors (DB hiccup) so a transient outage
	// doesn't lose mail — the storage trigger is the safety net.
	if s.relay.enforcer != nil && agent.UserID != "" {
		if err := s.relay.enforcer.CheckMessageSend(ctx, agent.UserID); err != nil {
			if le, ok := limits.IsLimitExceeded(err); ok {
				log.Printf("[%s] [%s] rejecting %s: limit exceeded (%s)", s.id, s.from, to, le.Resource)
				return &smtp.SMTPError{Code: 552, EnhancedCode: smtp.EnhancedCode{5, 2, 2}, Message: "mailbox quota exceeded"}
			}
			log.Printf("[%s] [%s] limits check error (failing open): %v", s.id, s.from, err)
		}
	}

	s.recipients = append(s.recipients, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	ctx := context.Background()
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	// Extract threading info once, for the DATA log + the async accept path (the
	// per-recipient processing recomputes its own threadInfo from the raw body).
	threadInfo := extractThreadInfo(body)
	senderEmail := extractEmail(s.from) // prefer From header for the log
	if threadInfo.From != "" {
		senderEmail = threadInfo.From
	}
	log.Printf("[%s] [%s] DATA recipients=%v size=%d bytes", s.id, senderEmail, s.recipients, len(body))

	// Queue-first async path (E2A_INBOUND_MODE=async): durably accept the raw MIME to
	// inbound_intake + enqueue a River processing job, all before 250 — processing
	// happens off the SMTP critical path. Falls back to the synchronous inline path
	// when the enqueuer isn't wired (fail-safe).
	if s.relay.inboundAsync && s.relay.inboundEnq != nil {
		return s.acceptInbound(ctx, body, threadInfo)
	}
	return s.deliverMessages(ctx, body)
}

// deliverMessages resolves each recipient and processes the message synchronously
// (the E2A_INBOUND_MODE=sync path): it calls processInbound inline with no
// post-persist hook. A persist failure surfaces as a 451 so the sending MTA retries
// (never a silent 250). The async path enqueues to River instead (see the accept-tx).
func (s *session) deliverMessages(ctx context.Context, body []byte) error {
	for _, rcpt := range s.recipients {
		in := inboundInput{
			Body:         body,
			EnvelopeFrom: extractEmail(s.from),
			RemoteIP:     s.remoteIP,
			Recipient:    rcpt,
			TraceID:      s.id,
		}
		if derr := s.relay.processInbound(ctx, in, nil); derr != nil {
			if errors.Is(derr, identity.ErrRecipientGone) {
				continue // recipient's agent is gone — skip it (historical skip+continue)
			}
			// Persist failed — return a transient SMTP error so the sending MTA
			// retries the whole message (RFC 5321 §4.5.4.1) instead of us silently
			// losing it under a 250. Multi-recipient caveat: a retry re-delivers to
			// already-succeeded recipients (the sync path has no dedup) — duplicate
			// beats loss, and the queue-first path (E2A_INBOUND_MODE=async) dedups.
			log.Printf("[%s] persist failed for %s → 451 (sender will retry): %v", s.id, rcpt, derr)
			return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "temporary failure storing message; please retry"}
		}
	}
	return nil
}

// inboundInput is the connection-derived data processInbound needs — the same facts
// the live SMTP session holds and the intake row persists for the async worker (which
// cannot recompute EnvelopeFrom/RemoteIP after the session closes).
type inboundInput struct {
	Body         []byte
	EnvelopeFrom string // SMTP MAIL FROM
	RemoteIP     net.IP // connecting IP (SPF); the worker parses the stored text form
	Recipient    string // RCPT TO
	TraceID      string // log correlation (session id / intake id)
}

// postPersistHook runs INSIDE processInbound's persist transaction, after the
// messages insert + event publish, given the new message id. The async worker passes
// MarkInboundIntakeProcessedTx so the intake flips to 'processed' atomically with the
// message (its idempotency gate); the sync path passes nil.
type postPersistHook func(ctx context.Context, tx pgx.Tx, messageID string) error

// processInbound runs the full inbound chain — parse, SPF/DKIM, HMAC sign, ingestion
// gate, content screening, persist, and event publish — for one recipient. It is the
// SINGLE implementation shared by the synchronous SMTP session and the async River
// worker (internal/inboundprocess); the post-persist hook is the only difference.
//
// Returns a non-nil error ONLY when the message could not be durably persisted (the
// caller maps that to a 451 / a River retry). A recipient that no longer resolves to
// an agent is a no-op (nil) — the mailbox is gone. Screening/metering fail open.
func (srv *Server) processInbound(ctx context.Context, in inboundInput, hook postPersistHook) error {
	// Recompute the connection-derived context from the raw bytes — identical whether
	// we are in the live session or replaying a persisted intake row.
	threadInfo := extractThreadInfo(in.Body)
	senderEmail := extractEmail(in.EnvelopeFrom)
	if threadInfo.From != "" {
		senderEmail = threadInfo.From
	}
	// SPF/DKIM/DMARC against the TRUE envelope MAIL FROM (RFC 7208), not the From
	// header — else SPF-alignment is a tautology (adversarial review F5).
	domainAuth := emailauth.Check(in.RemoteIP, extractEmail(in.EnvelopeFrom), in.Body)
	log.Printf("[%s] [%s] domain auth from %s (envelope %s): %s", in.TraceID, senderEmail, in.RemoteIP, in.EnvelopeFrom, domainAuth.Summary())

	agent, err := srv.resolveAgent(ctx, in.Recipient)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err // genuine transient resolve failure — retryable (451 / River)
	}
	if errors.Is(err, pgx.ErrNoRows) || agent == nil {
		// Recipient's agent was deleted between accept and processing — NOT a transient
		// error. Return the ErrRecipientGone sentinel: the async worker marks the
		// intake terminally (so it doesn't linger 'accepted' with orphaned raw MIME)
		// and the sync path skips the recipient. Returning a plain error here would
		// burn the whole retry envelope (~5.5h) and re-meter the undeliverable message
		// on every attempt. (GetAgentByID returns ErrNoRows, not (nil,nil), for a gone
		// agent — the nil check is defensive.)
		log.Printf("[%s] recipient %s no longer resolves to an agent — dropping", in.TraceID, in.Recipient)
		return identity.ErrRecipientGone
	}
	rcpt := in.Recipient
	body := in.Body

	// Generate the message ID up-front so it can be bound into the HMAC
	// canonical. Recipients verify by reconstructing the canonical with
	// the message_id from the payload — substituting the ID without
	// recomputing the MAC fails verification.
	messageID := identity.NewMessageID()

	// Build auth headers — signed Sender is the From: address (what SPF/DKIM authenticated).
	// The body hash is bound into the canonical so a captured (headers, MAC) pair
	// cannot be replayed under a modified body within the replay window.
	//
	// Signed with the deployment HMAC secret (cfg.Signing.HMACSecret) — the
	// sole signer for X-E2A-Auth-* headers.
	authPayload := headers.AuthPayload{
		Verified:    domainAuth.DomainAuthenticated(),
		Sender:      senderEmail,
		EntityType:  "human",
		DomainCheck: domainAuth.Summary(),
		MessageID:   messageID,
		BodyHash:    headers.HashBody(body),
	}
	authHeaders := srv.signer.Sign(authPayload)

	// Display sender for webhook / stored message prefers the first Reply-To
	// when set, so recipients reply to the intended mailbox (matches how
	// mail clients treat Reply-To). Auth headers above retain the From:
	// address. The full Reply-To list is shipped separately on the
	// webhook payload so downstream consumers can see all addresses.
	displaySender := senderEmail
	if len(threadInfo.ReplyTo) > 0 {
		displaySender = threadInfo.ReplyTo[0]
	}

	// Inbound trust policy ingestion gate (decision 10 / Slice 7a). Evaluate the
	// agent's policy against the AUTHENTICATED From identity (senderEmail — what
	// SPF/DKIM/DMARC pertain to), NOT displaySender (Reply-To is attacker-
	// controllable). A non-match is FLAGGED — still delivered (never dropped),
	// marked on the row, and emits email.flagged so operators get a signal.
	//
	// senderResolvable fails the gate closed for shared-relay "via e2a" mail,
	// which authenticates but carries no per-agent identity (#299).
	policyDecision := inboundpolicy.EvaluateIngestion(agent.InboundPolicy, agent.InboundAllowlist, senderEmail, srv.senderResolvable(senderEmail))

	// Content screening (Slice 4): run the per-agent inbound scan and record the
	// audit trail (protection_events) + the denormalized verdict. Detection +
	// annotation only here — review/block holds are a later slice, so the message
	// still delivers.
	screenRes := srv.screenInbound(ctx, agent, messageID, senderEmail, body, domainAuth, policyDecision)

	lookup := func(ctx context.Context, ids []string) (string, error) {
		return srv.store.LookupConversationID(ctx, agent.ID, ids)
	}
	conversationID := resolveConversationID(ctx, threadInfo, srv.envelopeFromTrusted(in.EnvelopeFrom), lookup)

	// All inbound is persisted to the pollable inbox. There is no per-agent
	// webhook delivery anymore (push is via /v1/webhooks subscriptions), so
	// nothing is "pending delivery" — every received message starts unread.
	deliveryStatus := "unread"

	// Build the email.received event up front so its deterministic ID
	// is computed BEFORE the trigger tx opens. An MTA-retried delivery
	// produces the same (messageID, eventType) inputs → the same
	// "evt_<hash>" id → outbox INSERT no-ops on the second attempt via
	// ON CONFLICT (id) DO NOTHING. Idempotency by construction; see
	// design §5.1.
	event := webhookpub.NewEvent(webhookpub.EventEmailReceived, agent.UserID, buildEmailReceivedPayload(
		messageID, conversationID, displaySender, senderEmail, rcpt, threadInfo.Subject, threadInfo, authHeaders, agent,
	))
	event.AgentID = agent.ID
	event.ConversationID = conversationID
	event.MessageID = messageID
	// labels are unset on inbound (the labels feature applies
	// post-receive); leave Event.Labels empty so label-filtered
	// subscribers correctly skip (H5 null/empty semantics).
	event.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailReceived)

	// email.flagged (decision 10): fired in addition to email.received when the
	// ingestion policy didn't match, so operators get a signal that this message
	// is untrusted. Deterministic id keeps MTA retries idempotent.
	// email.flagged fires for a delivered gate-flag. When the message is HELD
	// (review/block), delivery is suppressed and email.flagged is not fired: a
	// block emits email.blocked, a review emits email.pending_review (below).
	var flaggedEvent *webhookpub.Event
	if policyDecision.Flagged && !screenRes.Hold {
		fe := webhookpub.NewEvent(webhookpub.EventEmailFlagged, agent.UserID, map[string]interface{}{
			"message_id":      messageID,
			"conversation_id": conversationID,
			"agent":           map[string]interface{}{"id": agent.ID, "email": agent.EmailAddress(), "domain": agent.Domain},
			// from is the AUTHENTICATED From identity the policy evaluated and
			// flagged — NOT displaySender (Reply-To), which is attacker-
			// controllable and would name a trusted-looking address on the very
			// message the gate rejected. display_sender/reply_to carry the
			// reply-routing addresses separately so the signal is complete.
			"from":           senderEmail,
			"display_sender": displaySender,
			"reply_to":       threadInfo.ReplyTo,
			"recipient":      rcpt,
			"subject":        threadInfo.Subject,
			"policy":         agent.InboundPolicy,
			"reason":         policyDecision.Reason,
		})
		fe.AgentID = agent.ID
		fe.ConversationID = conversationID
		fe.MessageID = messageID
		fe.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailFlagged)
		flaggedEvent = &fe
	}

	// email.blocked: fired when the applied action is block — the message is
	// accept-then-quarantined (review_rejected, dropped, no human). It is the only
	// signal a subscriber gets for a dropped inbound message, so emit it regardless of
	// producer. reason_source mirrors the protection_events audit vocabulary
	// (sender_gate / inbound_scan). Deterministic id keeps MTA retries idempotent.
	var blockedEvent *webhookpub.Event
	if screenRes.Blocked() {
		be := webhookpub.NewEvent(webhookpub.EventEmailBlocked, agent.UserID, map[string]interface{}{
			"message_id":      messageID,
			"conversation_id": conversationID,
			"agent":           map[string]interface{}{"id": agent.ID, "email": agent.EmailAddress(), "domain": agent.Domain},
			"direction":       "inbound",
			"from":            senderEmail,
			"recipient":       rcpt,
			"subject":         threadInfo.Subject,
			"reason":          screenRes.Reason,
			"reason_source":   screenRes.Denorm.ReviewReason,
		})
		be.AgentID = agent.ID
		be.ConversationID = conversationID
		be.MessageID = messageID
		be.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailBlocked)
		blockedEvent = &be
	}

	// email.pending_review: fired when the applied action is review — the message is
	// held as pending_review awaiting a human / TTL. The same event fires for
	// outbound HITL holds (direction-aware); carries the review TTL (approval_expires_at) and
	// reason_source so a subscriber can drive a review queue from push. Deterministic
	// id keeps MTA retries idempotent.
	var pendingReviewEvent *webhookpub.Event
	if screenRes.Review() {
		pe := webhookpub.NewEvent(webhookpub.EventEmailPendingReview, agent.UserID, map[string]interface{}{
			"message_id":          messageID,
			"conversation_id":     conversationID,
			"agent":               map[string]interface{}{"id": agent.ID, "email": agent.EmailAddress(), "domain": agent.Domain},
			"direction":           "inbound",
			"from":                senderEmail,
			"recipient":           rcpt,
			"subject":             threadInfo.Subject,
			"reason":              screenRes.Reason,
			"reason_source":       screenRes.Denorm.ReviewReason,
			"approval_expires_at": screenRes.Denorm.ApprovalExpiresAt,
		})
		pe.AgentID = agent.ID
		pe.ConversationID = conversationID
		pe.MessageID = messageID
		pe.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailPendingReview)
		pendingReviewEvent = &pe
	}

	// Record inbound message with full content. Pass messageID so the
	// stored row uses the same ID we just bound into the auth headers.
	// toRecipients/cc come from the parsed To:/Cc: headers and are the
	// same across every fan-out delivery for this inbound message.
	//
	// Two paths:
	//   (1) Transactional outbox path (slice 3, default-off via the
	//       Outbox's FeatureFlag in v1): wraps the messages INSERT and
	//       webhook_events INSERT in one tx so the at-least-once
	//       publish-loss window is closed. Per §5.1, a COMMIT failure
	//       means the message wasn't recorded and we return — the
	//       SMTP MTA retries with the same Message-ID and the
	//       deterministic event ID makes the retry idempotent.
	//   (2) Legacy path: messages INSERT outside any tx; the
	//       in-process publisher.Publish goroutine fires post-commit.
	//       Preserves pre-design behavior so deployments that haven't
	//       wired the outbox don't regress.
	// Structured inbound auth verdict {spf,dkim,dmarc} persisted alongside the
	// signed X-E2A-Auth-* blob (decision 9 / Slice 4b-2) — the trust primitive
	// surfaced on the message and enforced on by the Slice 7 inbound policy.
	// SPF can't be recomputed at read (the connecting IP isn't stored), so store
	// it now. Best-effort marshal: a failure just omits the verdict.
	authVerdictJSON, _ := json.Marshal(domainAuth)

	var inboundMsg *identity.Message
	if srv.outbox != nil {
		err = srv.store.WithTx(ctx, func(tx pgx.Tx) error {
			var txErr error
			inboundMsg, txErr = srv.store.CreateInboundMessageInTx(
				ctx, tx, messageID, agent.ID, displaySender, rcpt,
				threadInfo.MessageID, threadInfo.Subject, conversationID,
				deliveryStatus, body, authHeaders, authVerdictJSON,
				policyDecision.Flagged, policyDecision.Reason,
				threadInfo.To, threadInfo.CC, threadInfo.ReplyTo,
				screenRes.Denorm,
			)
			if txErr != nil {
				return txErr
			}
			// Held messages (review/block) are persisted but NOT delivered — suppress
			// the email.received push (the agent only ever sees released messages).
			if !screenRes.Hold {
				if txErr = srv.outbox.PublishTx(ctx, tx, event); txErr != nil {
					return txErr
				}
			}
			if flaggedEvent != nil {
				if txErr = srv.outbox.PublishTx(ctx, tx, *flaggedEvent); txErr != nil {
					return txErr
				}
			}
			if blockedEvent != nil {
				if txErr = srv.outbox.PublishTx(ctx, tx, *blockedEvent); txErr != nil {
					return txErr
				}
			}
			if pendingReviewEvent != nil {
				if txErr = srv.outbox.PublishTx(ctx, tx, *pendingReviewEvent); txErr != nil {
					return txErr
				}
			}
			// Async worker: flip the intake to 'processed' ATOMICALLY with the message
			// + events, so a crash re-drive finds 'processed' and no-ops (the worker's
			// idempotency gate). nil on the synchronous path.
			if hook != nil {
				if txErr = hook(ctx, tx, messageID); txErr != nil {
					return txErr
				}
			}
			return nil
		})
	} else {
		// No-outbox legacy path (sync only — the async worker always runs with the
		// outbox wired). hook is not supported here; async requires srv.outbox.
		inboundMsg, err = srv.store.CreateInboundMessage(
			ctx, messageID, agent.ID, displaySender, rcpt,
			threadInfo.MessageID, threadInfo.Subject, conversationID,
			deliveryStatus, body, authHeaders, authVerdictJSON,
			policyDecision.Flagged, policyDecision.Reason,
			threadInfo.To, threadInfo.CC, threadInfo.ReplyTo,
			screenRes.Denorm,
		)
	}
	if err != nil {
		if errors.Is(err, identity.ErrIntakeAlreadyProcessed) {
			// Benign at-least-once re-drive: a prior attempt already processed this
			// intake, so the persist tx rolled back (no duplicate). Not a failure —
			// the async worker treats this sentinel as done. Don't log it as an error.
			return err
		}
		// Do NOT swallow — surface to deliverMessages so the SMTP session returns a
		// 451 and the sender retries, instead of a 250 that silently drops the mail.
		log.Printf("[%s] [%s] failed to record inbound message: %v", in.TraceID, senderEmail, err)
		return err
	}
	_ = inboundMsg

	// Record inbound usage AFTER the message is durably persisted (fail-open — never
	// block inbound email). The async worker retries a failed attempt, so metering
	// before the persist tx would over-count — billing an undeliverable message once
	// per attempt. Post-persist + the worker's already-processed no-op gate ⇒ once per
	// delivered message. Mirrors the outbound send path.
	if agent.UserID != "" {
		srv.usage.RecordAndCheck(ctx, agent.UserID, agent.ID, agent.Domain, "inbound")
	}

	// Append the screening audit rows (gate + scan violations) best-effort. Soft-ref
	// + deterministic ids make this idempotent under MTA retry, so it's safe outside
	// the message transaction.
	srv.writeProtectionEvents(ctx, messageID, screenRes.Events)

	slug, _, _ := strings.Cut(rcpt, "@")

	log.Printf("[mail:%s] dir=inbound from=%s to=%s slug=%s conv_id=%s subject=%q verified=%t",
		messageID, displaySender, rcpt, slug, conversationID, threadInfo.Subject, domainAuth.DomainAuthenticated())

	// Inbound events (email.received + flagged/blocked/pending_review variants)
	// are written to the outbox (webhook_events) above, in the message tx; the
	// drain fans them out and enqueues River delivery jobs. No in-process
	// fan-out here.

	// Best-effort WebSocket notification for any connected agent. The
	// /v1/webhooks subscriber resource (driven by the outbox drain) is the
	// durable push path; WS is an opportunistic live-tail on top of it,
	// available to every agent regardless of how it's configured.
	if srv.hub != nil && !screenRes.Hold && srv.hub.IsConnected(agent.ID) {
		notification := buildWSNotification(inboundMsg)
		if srv.hub.Send(agent.ID, notification) {
			log.Printf("[mail:%s] ws_notify=sent slug=%s", messageID, slug)
		}
	}
	return nil
}

// The envelope wrapper ({event, id, created_at, data}) is added by the publisher
// when it marshals the Event; this helper only produces the data subfield.
//
// buildEmailReceivedPayload builds the email.received event data. The event is a
// metadata-only NOTIFICATION, not a content carrier: it omits the message body
// (raw_message) that an earlier revision embedded. A subscriber fetches the full
// message — body + attachments — from GET /v1/agents/{recipient}/messages/{message_id}
// using the message_id + recipient carried here (the same notify→fetch model the
// WebSocket listener already uses). This keeps the fan-out bus payload bounded,
// avoids shipping full message PII to every subscriber endpoint, and makes the
// REST resource the single source of truth for content.
//
// auth_headers stays on the event: it is small SIGNED metadata — the X-E2A-Auth-*
// attestation (HMAC-keyed by the owner's signing secret, with a replay timestamp)
// that lets a subscriber INDEPENDENTLY verify the inbound SPF/DKIM/DMARC verdict.
// It is metadata, not content, so it is not subject to the body-fetch rule.
func buildEmailReceivedPayload(
	messageID, conversationID, displaySender, authenticatedFrom, recipient, subject string,
	threadInfo threadInfo,
	authHeaders map[string]string,
	agent *identity.AgentIdentity,
) map[string]interface{} {
	return map[string]interface{}{
		"message_id":      messageID,
		"conversation_id": conversationID,
		"agent": map[string]interface{}{
			"id":     agent.ID,
			"email":  agent.EmailAddress(),
			"domain": agent.Domain,
		},
		"from": displaySender,
		// authenticated_from is the From-header identity that SPF/DKIM/DMARC
		// and the inbound trust policy (decision 10) actually pertain to.
		// It can differ from "from" (which prefers Reply-To for reply
		// routing): a consumer of an allowlist/domain-gated agent MUST treat
		// authenticated_from — not from — as the gated/verified identity.
		"authenticated_from": authenticatedFrom,
		"to":                 threadInfo.To,
		"cc":                 threadInfo.CC,
		"reply_to":           threadInfo.ReplyTo,
		"recipient":          recipient,
		"subject":            subject,
		"auth_headers":       authHeaders,
		"received_at":        time.Now().UTC().Format(time.RFC3339),
	}
}

// buildWSNotification creates a lightweight JSON notification for WebSocket delivery.
func buildWSNotification(msg *identity.Message) []byte {
	return ws.BuildNotification(msg)
}

func splitEmail(addr string) (local, domain string) {
	parts := strings.SplitN(addr, "@", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return addr, ""
}

func (s *session) Reset() {
	s.from = ""
	s.recipients = nil
}

func (s *session) Logout() error {
	return nil
}

// resolveConversationID applies the precedence used to recover the
// application-level thread ID for an inbound message:
//  1. Trusted X-E2A-Conversation-ID header (envelope MAIL FROM matches our
//     own relay). External senders can't forge this because the gate keeps
//     the header from being honored outside same-platform traffic.
//  2. In-Reply-To / References lookup, scoped to the recipient agent's own
//     messages. Covers external replies that thread off our outbound.
//  3. "" (no thread context available).
//
// lookup may be nil in tests; it is invoked only when lookup IDs are present.
func resolveConversationID(ctx context.Context, info threadInfo, trusted bool, lookup func(context.Context, []string) (string, error)) string {
	if info.ConversationID != "" && trusted {
		return info.ConversationID
	}
	var lookupIDs []string
	if info.InReplyTo != "" {
		lookupIDs = append(lookupIDs, info.InReplyTo)
	}
	lookupIDs = append(lookupIDs, info.References...)
	if len(lookupIDs) == 0 || lookup == nil {
		return ""
	}
	cid, err := lookup(ctx, lookupIDs)
	if err != nil {
		return ""
	}
	return cid
}

// envelopeFromTrusted reports whether the SMTP MAIL FROM for this session
// originated from our own outbound relay. Used to gate trust of custom
// X-E2A-* headers that carry app-level context across the SMTP boundary.
//
// Matches the exact configured domain OR any subdomain of it. The subdomain
// case is needed because SES rewrites the envelope MAIL FROM to a bounce
// address on a "MAIL FROM Domain" subdomain (e.g. configured outbound is
// send.e2a.dev but the envelope comes back as
// <bounce-id>@mail.send.e2a.dev). We own all subdomains of our outbound
// domain, so trusting them is the same trust boundary.
func (srv *Server) envelopeFromTrusted(envelopeFrom string) bool {
	if srv.outboundFromDomain == "" {
		return false
	}
	envDomain := strings.ToLower(extractDomain(envelopeFrom))
	trusted := strings.ToLower(srv.outboundFromDomain)
	return envDomain == trusted || strings.HasSuffix(envDomain, "."+trusted)
}

// senderResolvable reports whether the inbound From identity maps to a SPECIFIC
// authenticated sender, so a per-agent inbound allowlist/domain gate can match it.
//
// Mail relayed for a non-sending-verified agent goes out under the shared
// "agent@<outboundFromDomain>" address (internal/outbound/sender.go): it
// authenticates against the relay domain (DMARC passes) but is the SAME address
// for every such agent, so it carries no per-agent identity. Treating it as
// resolvable would let any allowlist that names the relay domain (or that shared
// address) admit every unverified agent indiscriminately — and would never admit
// the specific agent the operator meant. So we report it unresolvable and the
// gate fails closed under allowlist/domain (open is unaffected). #299.
//
// Matches the exact relay domain OR any subdomain, mirroring envelopeFromTrusted:
// a verified agent always sends from its own custom domain, never the relay's.
func (srv *Server) senderResolvable(senderEmail string) bool {
	if srv.outboundFromDomain == "" {
		return true
	}
	dom := strings.ToLower(extractDomain(senderEmail))
	relay := strings.ToLower(srv.outboundFromDomain)
	return dom != relay && !strings.HasSuffix(dom, "."+relay)
}

func extractEmail(addr string) string {
	if parsed, err := mail.ParseAddress(addr); err == nil {
		return parsed.Address
	}
	return addr
}

type threadInfo struct {
	MessageID      string
	Subject        string
	InReplyTo      string
	References     []string
	From           string   // From header (human-readable sender, not SMTP envelope)
	ReplyTo        []string // Reply-To header addresses — empty when absent (RFC 5322 allows multiple)
	To             []string // To: header addresses (one row per fan-out target sees the same list)
	CC             []string // Cc: header addresses
	ConversationID string   // X-E2A-Conversation-ID header, if present
}

// extractThreadInfo parses threading headers from a raw RFC 2822 message.
func extractThreadInfo(body []byte) threadInfo {
	msg, err := mail.ReadMessage(bytes.NewReader(body))
	if err != nil {
		return threadInfo{}
	}
	var refs []string
	if r := msg.Header.Get("References"); r != "" {
		for _, ref := range strings.Fields(r) {
			refs = append(refs, ref)
		}
	}
	// Extract From header email address
	fromHeader := ""
	if addr, err := mail.ParseAddress(msg.Header.Get("From")); err == nil {
		fromHeader = addr.Address
	}
	return threadInfo{
		MessageID:  msg.Header.Get("Message-Id"),
		Subject:    decodeMIMEHeader(msg.Header.Get("Subject")),
		InReplyTo:  msg.Header.Get("In-Reply-To"),
		References: refs,
		From:       fromHeader,
		// RFC 5322 § 3.6.2 allows multiple addresses in Reply-To. Mirror
		// the same parser used for To/Cc so display names get stripped
		// the same way and the field shape is uniform across consumers.
		ReplyTo:        extractAddressList(msg.Header.Get("Reply-To")),
		To:             extractAddressList(msg.Header.Get("To")),
		CC:             extractAddressList(msg.Header.Get("Cc")),
		ConversationID: msg.Header.Get("X-E2A-Conversation-Id"),
	}
}

// decodeMIMEHeader decodes any RFC 2047 encoded-word runs in a header value
// (e.g. `=?utf-8?Q?caf=C3=A9?=` → `café`). Used for display fields like
// Subject so downstream consumers (DB, list summaries, dashboard) see the
// UTF-8 form instead of the wire-encoded form. If decoding fails the original
// value is returned — preserving the field is better than dropping it.
func decodeMIMEHeader(v string) string {
	if v == "" {
		return v
	}
	decoded, err := (&mime.WordDecoder{}).DecodeHeader(v)
	if err != nil {
		return v
	}
	return decoded
}

// extractAddressList parses an RFC 5322 address-list header (To/Cc) into bare
// email addresses, dropping display names. Returns nil if the header is empty
// or unparseable; group addresses and malformed entries are silently skipped.
func extractAddressList(header string) []string {
	if header == "" {
		return nil
	}
	addrs, err := mail.ParseAddressList(header)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.Address != "" {
			out = append(out, a.Address)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func extractDomain(addr string) string {
	email := extractEmail(addr)
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// resolveAgent looks up an agent by exact recipient email match only.
func (s *Server) resolveAgent(ctx context.Context, rcpt string) (*identity.AgentIdentity, error) {
	email := extractEmail(rcpt)
	return s.store.GetAgentByID(ctx, email)
}
