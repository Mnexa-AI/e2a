package outbound

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

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
	Method    string   `json:"method"` // "smtp"
	To        []string `json:"-"`      // canonicalized To recipients
	CC        []string `json:"-"`      // canonicalized CC recipients
	BCC       []string `json:"-"`      // canonicalized BCC recipients
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
}

func NewSender(smtpRelay *SMTPRelay, fromDomain string) *Sender {
	return &Sender{
		smtpRelay:  smtpRelay,
		fromDomain: fromDomain,
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
	envelopeFrom := fmt.Sprintf("agent@%s", s.fromDomain)
	headerFrom := fmt.Sprintf("%q <%s>", displayName+" via e2a", envelopeFrom)
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

	sesMessageID, err := s.smtpRelay.Send(envelopeFrom, envelope, message)
	if err != nil {
		return nil, fmt.Errorf("smtp relay: %w", err)
	}

	return &SendResult{
		MessageID: sesMessageID,
		Method:    "smtp",
		To:        to,
		CC:        cc,
		BCC:       bcc,
	}, nil
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
