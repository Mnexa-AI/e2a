package relay

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/tls"
	"encoding/hex"
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
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/ws"
	"github.com/emersion/go-smtp"
)

type Server struct {
	smtpServer *smtp.Server
	store      *identity.Store
	signer     *headers.Signer
	deliverer  *webhook.PersistentDeliverer
	hub        *ws.Hub
	usage      usage.UsageTracker
	smtpDomain string
	// outboundFromDomain is the domain used in envelope MAIL FROM for mail we
	// originate (e.g. "send.e2a.dev"). Inbound messages whose envelope MAIL FROM
	// matches this domain are trusted same-platform traffic and may surface
	// X-E2A-Conversation-ID directly; external senders fall back to the
	// In-Reply-To lookup so they cannot forge conversation IDs.
	outboundFromDomain string
}

func NewServer(cfg *config.Config, store *identity.Store, signer *headers.Signer, deliverer *webhook.PersistentDeliverer, usage usage.UsageTracker, hub *ws.Hub) *Server {
	s := &Server{
		store:              store,
		signer:             signer,
		deliverer:          deliverer,
		hub:                hub,
		usage:              usage,
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

// deliverMessages runs SPF/DKIM checks and delivers to agents via webhook.
func (s *session) deliverMessages(ctx context.Context, senderEmail string, body []byte) error {
	// Run SPF/DKIM checks on the inbound message
	domainAuth := emailauth.Check(s.remoteIP, senderEmail, body)
	log.Printf("[%s] [%s] domain auth from %s: %s", s.id, senderEmail, s.remoteIP, domainAuth.Summary())

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

	// Record inbound usage (fail-open — never block inbound email)
	if agent.UserID != "" {
		s.relay.usage.RecordAndCheck(ctx, agent.UserID, agent.ID, agent.Domain, "inbound")
	}

	lookup := func(ctx context.Context, ids []string) (string, error) {
		return s.relay.store.LookupConversationID(ctx, agent.ID, ids)
	}
	conversationID := resolveConversationID(ctx, s.inboundThreadInfo, s.envelopeFromTrusted(), lookup)

	// Determine delivery status based on agent mode
	deliveryStatus := "pending"
	if agent.AgentMode == "local" {
		deliveryStatus = "unread"
	}

	// Record inbound message with full content. Pass messageID so the
	// stored row uses the same ID we just bound into the auth headers.
	// toRecipients/cc come from the parsed To:/Cc: headers and are the
	// same across every fan-out delivery for this inbound message.
	inboundMsg, err := s.relay.store.CreateInboundMessage(ctx, messageID, agent.ID, displaySender, rcpt, s.inboundMsgID, s.inboundSubject, conversationID, deliveryStatus, body, authHeaders, s.inboundThreadInfo.To, s.inboundThreadInfo.CC, s.inboundThreadInfo.ReplyTo)
	if err != nil {
		log.Printf("[%s] [%s] failed to record inbound message: %v", s.id, senderEmail, err)
		return
	}
	_ = inboundMsg
	slug, _, _ := strings.Cut(rcpt, "@")

	log.Printf("[mail:%s] dir=inbound from=%s to=%s slug=%s conv_id=%s subject=%q mode=%s verified=%t",
		messageID, displaySender, rcpt, slug, conversationID, s.inboundSubject, agent.AgentMode, domainAuth.DomainAuthenticated())

	// Local mode: message is stored, try WS notification
	if agent.AgentMode == "local" {
		if s.relay.hub != nil && s.relay.hub.IsConnected(agent.ID) {
			notification := buildWSNotification(inboundMsg)
			if s.relay.hub.Send(agent.ID, notification) {
				log.Printf("[mail:%s] ws_notify=sent slug=%s", messageID, slug)
			}
		}
		return
	}

	// Cloud mode: deliver via webhook
	log.Printf("[mail:%s] webhook=delivering slug=%s url=%s", messageID, slug, agent.WebhookURL)
	err = s.relay.deliverer.Deliver(ctx, agent, webhook.Payload{
		MessageID:      messageID,
		ConversationID: conversationID,
		From:           displaySender,
		To:             s.inboundThreadInfo.To,
		CC:             s.inboundThreadInfo.CC,
		ReplyTo:        s.inboundThreadInfo.ReplyTo,
		Recipient:      rcpt,
		RawMessage:     body,
		AuthHeaders:    authHeaders,
		ReceivedAt:     time.Now(),
	})
	if err != nil {
		log.Printf("[mail:%s] webhook=FAILED slug=%s error=%v", messageID, slug, err)
	} else {
		log.Printf("[mail:%s] webhook=OK slug=%s", messageID, slug)
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
		MessageID:      msg.Header.Get("Message-Id"),
		Subject:        decodeMIMEHeader(msg.Header.Get("Subject")),
		InReplyTo:      msg.Header.Get("In-Reply-To"),
		References:     refs,
		From:           fromHeader,
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
