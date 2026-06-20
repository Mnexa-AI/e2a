package relay

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"mime"
	"net"
	"net/mail"
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
	// publisher is the in-process fan-out path for the /v1/webhooks
	// subscriber resource. Fires email.received events to subscribed
	// endpoints. Optional — when nil (e.g. tests that don't exercise
	// the subscriber path) the relay only stores the message and
	// best-effort WS-notifies any connected agent.
	//
	// When `outbox` is wired AND its FeatureFlag is enabled, the
	// inbound trigger uses the transactional outbox path instead of
	// firing this goroutine.
	publisher webhookpub.Publisher
	// outbox is the slice-1 transactional publisher. When non-nil,
	// the inbound trigger writes the messages row and the
	// webhook_events row in a single transaction (per design §4.2).
	// When nil (the default), the legacy goroutine path runs
	// instead — preserves the v1 default-off rollout posture even if
	// a deployment forgets to wire it. The Outbox's own FeatureFlag
	// is the secondary gate.
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
}

// SetPublisher wires the webhooks-as-a-resource publisher — the sole
// push path since the per-agent webhook was removed in slice 3. Same
// optional-setter pattern as SetEnforcer — keeps NewServer's signature
// unchanged for the existing call sites and tests that don't care
// about the push path.
func (s *Server) SetPublisher(p webhookpub.Publisher) { s.publisher = p }

// SetOutbox wires the slice-1 transactional outbox. When set AND its
// FeatureFlag is enabled, the inbound trigger commits the messages row
// and the webhook_events outbox row in a single transaction (per design
// §4.2). The non-transactional SetPublisher path remains the default
// until the outbox FeatureFlag flips on during the rollout window.
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
		screen:             piguard.NewEngine(piguard.EngineConfig{}, piguard.NewHeuristicsDetector()),
		smtpDomain:         cfg.SMTP.Domain,
		outboundFromDomain: cfg.OutboundSMTP.FromDomain,
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
	// Extracted from inbound email for threading
	inboundMsgID      string
	inboundSubject    string
	inboundThreadInfo threadInfo
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

	// Extract threading info from inbound email
	threadInfo := extractThreadInfo(body)
	s.inboundMsgID = threadInfo.MessageID
	s.inboundSubject = threadInfo.Subject
	s.inboundThreadInfo = threadInfo

	// Prefer From header (human-readable) over SMTP envelope (may be SES bounce address)
	senderEmail := extractEmail(s.from)
	if threadInfo.From != "" {
		senderEmail = threadInfo.From
	}
	log.Printf("[%s] [%s] DATA recipients=%v size=%d bytes", s.id, senderEmail, s.recipients, len(body))

	// Deliver directly — no human identity lookup needed
	return s.deliverMessages(ctx, senderEmail, body)
}

// deliverMessages runs SPF/DKIM/DMARC checks and delivers to agents via webhook.
func (s *session) deliverMessages(ctx context.Context, senderEmail string, body []byte) error {
	// Authenticate against the TRUE SMTP envelope MAIL FROM (s.from), not the
	// display senderEmail (which prefers the From header). SPF is an
	// envelope-identity check (RFC 7208), and DMARC alignment must compare the
	// real envelope domain against the From-header domain — passing the
	// From-derived senderEmail here would make SPF-alignment a tautology
	// (adversarial review F5). DKIM + From extraction come from the body.
	domainAuth := emailauth.Check(s.remoteIP, extractEmail(s.from), body)
	log.Printf("[%s] [%s] domain auth from %s (envelope %s): %s", s.id, senderEmail, s.remoteIP, s.from, domainAuth.Summary())

	delivered := 0
	for _, rcpt := range s.recipients {
		agent, err := s.relay.resolveAgent(ctx, rcpt)
		if err != nil {
			// Should not happen — Rcpt() already validated, but guard defensively
			log.Printf("[%s] [%s] skipping %s: agent resolution failed: %v", s.id, senderEmail, rcpt, err)
			continue
		}

		s.deliverToAgent(ctx, agent, senderEmail, rcpt, body, domainAuth)
		delivered++
	}

	log.Printf("[%s] [%s] session complete: delivered=%d/%d", s.id, senderEmail, delivered, len(s.recipients))
	return nil
}

// deliverToAgent signs auth headers and delivers a single message to an agent.
// Push agents get webhook delivery; poll agents get the message stored for retrieval.
func (s *session) deliverToAgent(ctx context.Context, agent *identity.AgentIdentity, senderEmail, rcpt string, body []byte, domainAuth *emailauth.Result) {
	// Generate the message ID up-front so it can be bound into the HMAC
	// canonical. Recipients verify by reconstructing the canonical with
	// the message_id from the payload — substituting the ID without
	// recomputing the MAC fails verification.
	messageID := identity.NewMessageID()

	// Build auth headers — signed Sender is the From: address (what SPF/DKIM authenticated).
	// The body hash is bound into the canonical so a captured (headers, MAC) pair
	// cannot be replayed under a modified body within the replay window.
	//
	// Signing secret is per-agent-owner: we use the user's most recently
	// created webhook signing secret. Older secrets remain valid for
	// recipient-side verification (multi-secret rotation) but the relay
	// always picks the freshest one for new signatures. Falls back to
	// the deployment-wide signer only if the user lookup fails — that
	// way an unowned/legacy agent still gets signed delivery.
	signingSecret := ""
	signingSecretID := ""
	if agent.UserID != "" {
		secrets, err := s.relay.store.GetUserSigningSecrets(ctx, agent.UserID)
		if err != nil {
			log.Printf("[%s] failed to load signing secrets for user %s: %v — falling back to deployment secret", s.id, agent.UserID, err)
		} else if len(secrets) > 0 {
			signingSecret = secrets[0].Secret
			signingSecretID = secrets[0].ID
		}
	}
	var authHeaders headers.AuthHeaders
	authPayload := headers.AuthPayload{
		Verified:    domainAuth.DomainAuthenticated(),
		Sender:      senderEmail,
		EntityType:  "human",
		DomainCheck: domainAuth.Summary(),
		MessageID:   messageID,
		BodyHash:    headers.HashBody(body),
	}
	if signingSecret != "" {
		authHeaders = headers.Sign(signingSecret, authPayload)
		// Record last_signed_at off the hot path. Best-effort: a failed
		// touch never blocks delivery — it only loses the dashboard
		// "last used" hint for this signature.
		go func(id string) {
			if err := s.relay.store.TouchSigningSecretLastSigned(context.Background(), id); err != nil {
				log.Printf("[mail:%s] touch last_signed_at failed: id=%s err=%v", messageID, id, err)
			}
		}(signingSecretID)
	} else {
		// Last-resort fallback for unowned agents or transient DB errors.
		// Production deployments should never hit this path after the
		// per-user-secrets backfill runs; if they do, the deployment
		// secret remains a working signer.
		authHeaders = s.relay.signer.Sign(authPayload)
	}

	// Display sender for webhook / stored message prefers the first Reply-To
	// when set, so recipients reply to the intended mailbox (matches how
	// mail clients treat Reply-To). Auth headers above retain the From:
	// address. The full Reply-To list is shipped separately on the
	// webhook payload so downstream consumers can see all addresses.
	displaySender := senderEmail
	if len(s.inboundThreadInfo.ReplyTo) > 0 {
		displaySender = s.inboundThreadInfo.ReplyTo[0]
	}

	// Inbound trust policy ingestion gate (decision 10 / Slice 7a). Evaluate the
	// agent's policy against the AUTHENTICATED From identity (senderEmail — what
	// SPF/DKIM/DMARC pertain to), NOT displaySender (Reply-To is attacker-
	// controllable). A non-match is FLAGGED — still delivered (never dropped),
	// marked on the row, and emits email.flagged so operators get a signal.
	dmarcPass := domainAuth.DMARC.Status == emailauth.StatusPass
	policyDecision := inboundpolicy.EvaluateIngestion(agent.InboundPolicy, agent.InboundAllowlist, senderEmail, dmarcPass)

	// Content screening (Slice 4): run the per-agent inbound scan and record the
	// audit trail (screening_events) + the denormalized verdict. Detection +
	// annotation only here — review/block holds are a later slice, so the message
	// still delivers.
	screenRes := s.relay.screenInbound(ctx, agent, messageID, senderEmail, body, domainAuth, policyDecision)

	// Record inbound usage (fail-open — never block inbound email)
	if agent.UserID != "" {
		s.relay.usage.RecordAndCheck(ctx, agent.UserID, agent.ID, agent.Domain, "inbound")
	}

	lookup := func(ctx context.Context, ids []string) (string, error) {
		return s.relay.store.LookupConversationID(ctx, agent.ID, ids)
	}
	conversationID := resolveConversationID(ctx, s.inboundThreadInfo, s.envelopeFromTrusted(), lookup)

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
		messageID, conversationID, displaySender, senderEmail, rcpt, s.inboundSubject, s.inboundThreadInfo, body, authHeaders, agent,
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
	// (review/block), delivery is suppressed and email.injection_detected carries
	// the signal instead, so don't also fire email.flagged.
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
			"reply_to":       s.inboundThreadInfo.ReplyTo,
			"recipient":      rcpt,
			"subject":        s.inboundSubject,
			"policy":         agent.InboundPolicy,
			"reason":         policyDecision.Reason,
		})
		fe.AgentID = agent.ID
		fe.ConversationID = conversationID
		fe.MessageID = messageID
		fe.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailFlagged)
		flaggedEvent = &fe
	}

	// email.injection_detected (Slice 4): fired when the content scan flags the
	// message. Carries the score, applied action, and categories. Deterministic id
	// keeps MTA retries idempotent, mirroring email.flagged.
	var injectionEvent *webhookpub.Event
	if screenRes.Emit() {
		ie := webhookpub.NewEvent(webhookpub.EventEmailInjectionDetected, agent.UserID, map[string]interface{}{
			"message_id":      messageID,
			"conversation_id": conversationID,
			"agent":           map[string]interface{}{"id": agent.ID, "email": agent.EmailAddress(), "domain": agent.Domain},
			"from":            senderEmail,
			"recipient":       rcpt,
			"subject":         s.inboundSubject,
			"score":           screenRes.Score,
			"action":          screenRes.Action,
			"categories":      screenRes.Categories,
			"reason":          screenRes.Reason,
		})
		ie.AgentID = agent.ID
		ie.ConversationID = conversationID
		ie.MessageID = messageID
		ie.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailInjectionDetected)
		injectionEvent = &ie
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
	var err error
	if s.relay.outbox != nil {
		err = s.relay.store.WithTx(ctx, func(tx pgx.Tx) error {
			var txErr error
			inboundMsg, txErr = s.relay.store.CreateInboundMessageInTx(
				ctx, tx, messageID, agent.ID, displaySender, rcpt,
				s.inboundMsgID, s.inboundSubject, conversationID,
				deliveryStatus, body, authHeaders, authVerdictJSON,
				policyDecision.Flagged, policyDecision.Reason,
				s.inboundThreadInfo.To, s.inboundThreadInfo.CC, s.inboundThreadInfo.ReplyTo,
				screenRes.Denorm,
			)
			if txErr != nil {
				return txErr
			}
			// Held messages (review/block) are persisted but NOT delivered — suppress
			// the email.received push (the agent only ever sees released messages).
			if !screenRes.Hold {
				if txErr = s.relay.outbox.PublishTx(ctx, tx, event); txErr != nil {
					return txErr
				}
			}
			if flaggedEvent != nil {
				if txErr = s.relay.outbox.PublishTx(ctx, tx, *flaggedEvent); txErr != nil {
					return txErr
				}
			}
			if injectionEvent != nil {
				if txErr = s.relay.outbox.PublishTx(ctx, tx, *injectionEvent); txErr != nil {
					return txErr
				}
			}
			return nil
		})
	} else {
		inboundMsg, err = s.relay.store.CreateInboundMessage(
			ctx, messageID, agent.ID, displaySender, rcpt,
			s.inboundMsgID, s.inboundSubject, conversationID,
			deliveryStatus, body, authHeaders, authVerdictJSON,
			policyDecision.Flagged, policyDecision.Reason,
			s.inboundThreadInfo.To, s.inboundThreadInfo.CC, s.inboundThreadInfo.ReplyTo,
			screenRes.Denorm,
		)
	}
	if err != nil {
		log.Printf("[%s] [%s] failed to record inbound message: %v", s.id, senderEmail, err)
		return
	}
	_ = inboundMsg

	// Append the screening audit rows (gate + scan violations) best-effort. Soft-ref
	// + deterministic ids make this idempotent under MTA retry, so it's safe outside
	// the message transaction.
	s.relay.writeScreeningEvents(ctx, messageID, screenRes.Events)

	slug, _, _ := strings.Cut(rcpt, "@")

	log.Printf("[mail:%s] dir=inbound from=%s to=%s slug=%s conv_id=%s subject=%q verified=%t",
		messageID, displaySender, rcpt, slug, conversationID, s.inboundSubject, domainAuth.DomainAuthenticated())

	// Legacy in-process publisher fires ONLY when the outbox is not
	// the durable fan-out path for this deployment. When outbox is
	// enabled, the worker draining webhook_events handles fan-out;
	// firing the legacy goroutine here too would write a SECOND
	// delivery row (with event_id=NULL, so the partial unique index
	// idx_wsd_event_webhook_uniq cannot dedupe it against the
	// outbox-written row that has event_id set). Result pre-C3-fix:
	// every customer webhook fires twice the moment
	// WEBHOOKS_OUTBOX_ENABLED flips to true in prod.
	//
	// Note on the failure path: when the outbox tx fails, the early
	// return above means we never reach this block — neither the
	// outbox worker nor the legacy goroutine fires for that event.
	// Unlike the agent-side publishX helpers (which fall back to
	// legacy on outbox failure via shouldFireLegacy), the relay does
	// NOT compensate. This is a pre-existing silent-drop shape: the
	// relay's Data() returns nil regardless of per-recipient
	// delivery errors, so the upstream MTA gets SMTP 250 OK and
	// will NOT retry. The legacy path has the same shape (a
	// CreateInboundMessage failure is logged and dropped). Closing
	// this gap requires propagating the error up to Data() so the
	// MTA sees 4xx — out of scope for the C3 dedup fix; tracked
	// separately.
	if s.relay.publisher != nil && (s.relay.outbox == nil || !s.relay.outbox.Enabled()) {
		// Held messages are not delivered — suppress the email.received push.
		if !screenRes.Hold {
			go s.relay.publisher.Publish(context.Background(), event)
		}
		if flaggedEvent != nil {
			go s.relay.publisher.Publish(context.Background(), *flaggedEvent)
		}
		if injectionEvent != nil {
			go s.relay.publisher.Publish(context.Background(), *injectionEvent)
		}
	}

	// Best-effort WebSocket notification for any connected agent. The
	// /v1/webhooks subscriber resource (driven by the publisher above)
	// is the durable push path; WS is an opportunistic live-tail on top
	// of it, available to every agent regardless of how it's configured.
	if s.relay.hub != nil && !screenRes.Hold && s.relay.hub.IsConnected(agent.ID) {
		notification := buildWSNotification(inboundMsg)
		if s.relay.hub.Send(agent.ID, notification) {
			log.Printf("[mail:%s] ws_notify=sent slug=%s", messageID, slug)
		}
	}
}

// buildEmailReceivedPayload assembles the data portion of the
// email.received envelope sent to webhook subscribers. Mirrors the
// shape of the legacy webhook.Payload so receivers writing against
// either model see the same fields — sender, to/cc/reply_to lists,
// the raw RFC 5322 body (base64-encoded for JSON-safety), and the
// signed auth headers.
//
// The envelope wrapper ({event, id, created_at, data}) is added by
// the publisher when it marshals the Event; this helper only
// produces the data subfield.
func buildEmailReceivedPayload(
	messageID, conversationID, displaySender, authenticatedFrom, recipient, subject string,
	threadInfo threadInfo,
	rawMessage []byte,
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
		// routing): a consumer of a verified_only/allowlist agent MUST treat
		// authenticated_from — not from — as the gated/verified identity.
		"authenticated_from": authenticatedFrom,
		"to":                 threadInfo.To,
		"cc":                 threadInfo.CC,
		"reply_to":           threadInfo.ReplyTo,
		"recipient":          recipient,
		"subject":            subject,
		"raw_message":        rawMessage,
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
	s.inboundMsgID = ""
	s.inboundSubject = ""
	s.inboundThreadInfo = threadInfo{}
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
func (s *session) envelopeFromTrusted() bool {
	if s.relay.outboundFromDomain == "" {
		return false
	}
	envDomain := strings.ToLower(extractDomain(s.from))
	trusted := strings.ToLower(s.relay.outboundFromDomain)
	return envDomain == trusted || strings.HasSuffix(envDomain, "."+trusted)
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
