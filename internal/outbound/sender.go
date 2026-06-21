package outbound

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/dkim"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/mailfrom"
)

// DKIMKeyLookup returns the DKIM selector and PKCS#1 DER private key
// bytes for a domain. Empty selector OR empty key means "no key
// available — skip signing". Implementations should NOT return an
// error for the not-found case; that's a normal flow during the
// migration window when older domains haven't been keyed yet.
//
// Method name carries the "Internal" suffix to flag the boundary:
// this is NOT user-input-safe. The caller must have already
// authenticated and authorized the from-domain (e.g. via the agent
// layer's ownership check on the sender). A handler that ever calls
// this with a user-supplied domain string becomes a "sign as
// anyone" primitive.
type DKIMKeyLookup interface {
	GetDKIMKeyInternal(ctx context.Context, domain string) (selector string, privateKey []byte, err error)
}

// Attachment is a base64-encoded file attachment.
type Attachment struct {
	Filename    string `json:"filename" example:"report.pdf"`
	ContentType string `json:"content_type" example:"application/pdf"`
	Data        string `json:"data" example:"base64-encoded-content"` // base64-encoded
} // @name Attachment

// SendRequest is the outbound email contract.
//
// References is the full ancestor Message-ID chain for a reply, oldest →
// newest. When non-empty, it is written verbatim into the References:
// header so receiving mail clients can anchor the reply to an existing
// thread by matching ANY id in the chain — required for multi-party
// threads where the immediate-parent Message-ID may not be in every
// participant's mailbox. When empty but ReplyToMessageID is set, the
// References header falls back to a single id (legacy behavior).
type SendRequest struct {
	From             string       `json:"from,omitempty"`
	To               []string     `json:"to"`
	CC               []string     `json:"cc,omitempty"`
	BCC              []string     `json:"bcc,omitempty"`
	Subject          string       `json:"subject"`
	Body             string       `json:"body"`
	HTMLBody         string       `json:"html_body,omitempty"`
	ReplyToMessageID string       `json:"reply_to_message_id"`
	References       []string     `json:"references,omitempty"`
	ConversationID   string       `json:"conversation_id,omitempty"`
	Attachments      []Attachment `json:"attachments,omitempty"`
}

// SendResult contains the result of a successful send, including the
// canonicalized recipient lists for persistence.
type SendResult struct {
	MessageID string   `json:"message_id"`
	Method    string   `json:"method"`  // "smtp"
	SentAs    string   `json:"sent_as"` // "own_address" | "relay" (decision 4 fallback)
	To        []string `json:"-"`       // canonicalized To recipients
	CC        []string `json:"-"`       // canonicalized CC recipients
	BCC       []string `json:"-"`       // canonicalized BCC recipients
	// Raw is the exact composed MIME placed on the wire (post-DKIM, post-SES
	// header). Persisted as messages.raw_message so the agent gets a readable
	// "Sent folder" — a mailbox keeps both sides of a conversation.
	Raw []byte `json:"-"`
}

// ValidationError indicates a caller error (invalid addresses, no visible recipients).
// Handlers should map this to HTTP 400.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// IsValidationError returns true if err is a ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

type Sender struct {
	smtpRelay  *SMTPRelay
	fromDomain string
	// dkimLookup is optional. When non-nil, Send asks it for a private
	// key for the From-header domain and prepends a DKIM-Signature
	// header before handing the message to the relay. A nil lookup
	// (older callers, unit tests, dev mode without a store) bypasses
	// signing entirely — the relay falls back to whatever
	// deployment-level signing it has always done.
	dkimLookup DKIMKeyLookup
	// sendingStatus is optional (decision 4 / Slice 4). When set AND the
	// agent's verified custom domain has sending_status == "verified", the
	// From header uses the agent's OWN address instead of the relay
	// "… via e2a" rewrite. nil, an unverified domain, a lookup error, or any
	// non-verified status all fall back to the relay From (fail-closed): we
	// never send unaligned mail under a customer domain.
	sendingStatus SendingStatusLookup
	// sesConfigSet, when set, is added as the X-SES-CONFIGURATION-SET header so
	// SES publishes delivery/bounce/complaint events (decision 9 / Slice 4b).
	// Empty (the default) = no header, no events — dev/self-host without SES.
	sesConfigSet string
}

// SetSESConfigurationSet enables SES event publishing for outbound mail by
// tagging each message with the given configuration set. Optional-setter
// pattern; empty leaves event publishing off.
func (s *Sender) SetSESConfigurationSet(name string) { s.sesConfigSet = name }

// SendingStatusLookup returns a domain's sending_status string
// ("none"|"pending"|"verified"|"failed"). *identity.Store satisfies it.
// Kept as a string interface so outbound does not import senderidentity
// (and its River + AWS SDK deps).
type SendingStatusLookup interface {
	GetSendingStatus(ctx context.Context, domain string) (string, error)
}

// SetSendingStatusLookup enables own-address From for sending-verified
// domains. Optional-setter pattern (cf. relay.SetPublisher) so existing
// NewSender/NewSenderWithDKIM call sites and tests are unaffected.
func (s *Sender) SetSendingStatusLookup(l SendingStatusLookup) { s.sendingStatus = l }

// useOwnAddressFrom reports whether outbound for this agent may use its own
// address as the From header. Fail-closed: every uncertain path returns false.
func (s *Sender) useOwnAddressFrom(agent *identity.AgentIdentity) bool {
	if s.sendingStatus == nil || agent == nil || !agent.DomainVerified || agent.Domain == "" {
		return false
	}
	// "verified" mirrors senderidentity.StatusVerified (not imported here).
	status, err := s.sendingStatus.GetSendingStatus(context.Background(), agent.Domain)
	if err != nil {
		return false
	}
	return status == "verified"
}

// envelopeSender returns the SMTP MAIL FROM (Return-Path) for an outbound
// message: the aligned custom MAIL FROM (bounces@bounce.<domain>) when the
// domain is sending-verified, else the e2a-owned relay address (fail-closed).
// Pure (no store hit) so it's unit-testable; `own` is resolved once by Send.
func envelopeSender(own bool, agentDomain, fromDomain string) string {
	if own {
		return mailfrom.EnvelopeSender(agentDomain)
	}
	return fmt.Sprintf("agent@%s", fromDomain)
}

func NewSender(smtpRelay *SMTPRelay, fromDomain string) *Sender {
	return &Sender{
		smtpRelay:  smtpRelay,
		fromDomain: fromDomain,
	}
}

// NewSenderWithDKIM is NewSender with per-domain DKIM signing enabled.
// The lookup is queried once per send; key misses silently skip signing
// rather than fail the send.
func NewSenderWithDKIM(smtpRelay *SMTPRelay, fromDomain string, dkimLookup DKIMKeyLookup) *Sender {
	return &Sender{
		smtpRelay:  smtpRelay,
		fromDomain: fromDomain,
		dkimLookup: dkimLookup,
	}
}

// Send normalizes recipients, composes, and sends an email via SMTP relay.
// Returns a ValidationError for caller errors (bad addresses, no visible recipients)
// and a plain error for transport failures.
func (s *Sender) Send(agent *identity.AgentIdentity, req SendRequest) (*SendResult, error) {
	agentAddr := strings.ToLower(agent.EmailAddress())
	agentAliases := []string{
		agentAddr,
		strings.ToLower(fmt.Sprintf("agent@%s", s.fromDomain)),
	}

	// Normalize and validate all addresses
	to, err := normalizeAddrs(req.To)
	if err != nil {
		return nil, &ValidationError{Message: fmt.Sprintf("invalid To address: %v", err)}
	}
	cc, err := normalizeAddrs(req.CC)
	if err != nil {
		return nil, &ValidationError{Message: fmt.Sprintf("invalid CC address: %v", err)}
	}
	bcc, err := normalizeAddrs(req.BCC)
	if err != nil {
		return nil, &ValidationError{Message: fmt.Sprintf("invalid BCC address: %v", err)}
	}

	// Remove agent's own addresses
	to = removeAddrs(to, agentAliases)
	cc = removeAddrs(cc, agentAliases)
	bcc = removeAddrs(bcc, agentAliases)

	// Dedupe within each field
	to = dedupe(to)
	cc = dedupe(cc)
	bcc = dedupe(bcc)

	// Cross-field dedupe: To > CC > BCC
	cc = removeAddrs(cc, to)
	bcc = removeAddrs(bcc, to)
	bcc = removeAddrs(bcc, cc)

	// Visible-recipient check: at least one address in To or CC
	if len(to) == 0 && len(cc) == 0 {
		return nil, &ValidationError{Message: "no valid recipients"}
	}

	// Build envelope recipients (To + CC + BCC)
	envelope := make([]string, 0, len(to)+len(cc)+len(bcc))
	envelope = append(envelope, to...)
	envelope = append(envelope, cc...)
	envelope = append(envelope, bcc...)

	// Compose headers
	displayName := agent.Name
	if displayName == "" {
		displayName = agent.EmailAddress()
	}
	// Resolve the sending-verified gate once (it hits the sending_status store),
	// then derive both the header From and the envelope Return-Path from it.
	own := s.useOwnAddressFrom(agent)
	// Envelope MAIL FROM (Return-Path): the aligned custom MAIL FROM
	// (bounce.<domain>) for a verified domain — SPF authenticates the From
	// org-domain → no Gmail "via e2a" — else the e2a-owned relay address
	// (fail-closed: SPF passes for the relay, e2a captures bounces). Verified now
	// requires the custom MAIL FROM to be live, so the subdomain's MX exists and
	// bounces still reach SES's feedback handler.
	envelopeFrom := envelopeSender(own, agent.Domain, s.fromDomain)
	// Header From: the agent's OWN address once sending-verified (DKIM-aligned →
	// DMARC passes, replies reach the agent directly); else the "… via e2a"
	// rewrite (fail-closed default).
	var headerFrom string
	sentAs := "relay"
	if own {
		headerFrom = fmt.Sprintf("%q <%s>", displayName, agent.EmailAddress())
		sentAs = "own_address"
	} else {
		headerFrom = fmt.Sprintf("%q <%s>", displayName+" via e2a", envelopeFrom)
	}
	replyTo := agent.EmailAddress()

	var message []byte
	if len(req.Attachments) > 0 {
		message, err = ComposeMessageWithAttachments(headerFrom, to, cc, req.Subject, req.Body, req.HTMLBody, req.ReplyToMessageID, req.References, s.fromDomain, replyTo, req.ConversationID, req.Attachments)
	} else if req.HTMLBody != "" {
		message, err = ComposeMultipartMessage(headerFrom, to, cc, req.Subject, req.Body, req.HTMLBody, req.ReplyToMessageID, req.References, s.fromDomain, replyTo, req.ConversationID)
	} else {
		message, err = ComposeMessage(headerFrom, to, cc, req.Subject, req.Body, "text/plain", req.ReplyToMessageID, req.References, s.fromDomain, replyTo, req.ConversationID)
	}
	if err != nil {
		return nil, fmt.Errorf("compose message: %w", err)
	}

	// Per-domain DKIM signing. Choose the signing
	// domain from the agent's verified custom domain when available —
	// for shared agents that falls back to s.fromDomain. Failures here
	// are logged and skipped: an unsigned message gets through the
	// deployment-level DKIM that the SMTP relay (SES) attaches at the
	// edge, which is what we did before this change anyway.
	if s.dkimLookup != nil {
		signingDomain := s.fromDomain
		if agent != nil && agent.DomainVerified && agent.Domain != "" {
			signingDomain = agent.Domain
		}
		if signed, ok := s.signMessage(message, signingDomain); ok {
			message = signed
		}
	}

	// Snapshot the recipient-facing bytes for the retained "Sent folder" copy:
	// DKIM-signed, but BEFORE the e2a-internal SES configuration-set header (SES
	// strips that before delivery, so the recipient never sees it).
	sentBody := message

	// Attach the SES configuration-set header (decision 9 / Slice 4b) so SES
	// publishes delivery/bounce/complaint events for this message. Prepended
	// AFTER DKIM signing so it is never in the signed header set (SES strips it
	// before delivery; signing it would break the signature). Empty when SES
	// event publishing is not configured (dev/self-host) — no header, no events.
	if s.sesConfigSet != "" {
		message = append([]byte("X-SES-CONFIGURATION-SET: "+s.sesConfigSet+"\r\n"), message...)
	}

	sesMessageID, err := s.smtpRelay.Send(envelopeFrom, envelope, message)
	if err != nil {
		return nil, fmt.Errorf("smtp relay: %w", err)
	}

	return &SendResult{
		MessageID: sesMessageID,
		Method:    "smtp",
		SentAs:    sentAs,
		To:        to,
		CC:        cc,
		BCC:       bcc,
		Raw:       sentBody,
	}, nil
}

// signMessage looks up a DKIM keypair for the given domain and returns
// a signed copy of the message. Returns (nil, false) when no key is
// stored for the domain or when signing fails — callers proceed with
// the unsigned message rather than failing the send.
func (s *Sender) signMessage(message []byte, domain string) ([]byte, bool) {
	if s.dkimLookup == nil || domain == "" {
		return nil, false
	}
	selector, privKey, err := s.dkimLookup.GetDKIMKeyInternal(context.Background(), domain)
	if err != nil {
		log.Printf("[sender] dkim key lookup for %s: %v", domain, err)
		return nil, false
	}
	if selector == "" || len(privKey) == 0 {
		return nil, false
	}
	signed, err := dkim.Sign(message, domain, selector, privKey)
	if err != nil {
		log.Printf("[sender] dkim sign for %s failed (sending unsigned): %v", domain, err)
		return nil, false
	}
	return signed, true
}

// normalizeAddrs parses and lowercases a list of email addresses.
// Returns an error if any address is unparseable.
func normalizeAddrs(addrs []string) ([]string, error) {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		parsed, err := mail.ParseAddress(a)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", a, err)
		}
		out = append(out, strings.ToLower(parsed.Address))
	}
	return out, nil
}

// removeAddrs removes any address in exclude from addrs (case-insensitive).
func removeAddrs(addrs []string, exclude []string) []string {
	if len(exclude) == 0 {
		return addrs
	}
	set := make(map[string]struct{}, len(exclude))
	for _, e := range exclude {
		set[strings.ToLower(e)] = struct{}{}
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if _, ok := set[strings.ToLower(a)]; !ok {
			out = append(out, a)
		}
	}
	return out
}

// dedupe removes duplicate addresses preserving order (case-insensitive).
func dedupe(addrs []string) []string {
	seen := make(map[string]struct{}, len(addrs))
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		key := strings.ToLower(a)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			out = append(out, a)
		}
	}
	return out
}
