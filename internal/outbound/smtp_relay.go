package outbound

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/config"
)

var smtpRetryBackoffs = []time.Duration{1 * time.Second, 5 * time.Second, 15 * time.Second}

type SMTPRelay struct {
	cfg *config.OutboundSMTPConfig
}

func NewSMTPRelay(cfg *config.OutboundSMTPConfig) *SMTPRelay {
	return &SMTPRelay{cfg: cfg}
}

func (r *SMTPRelay) Configured() bool {
	return r.cfg.Host != ""
}

// Send sends an email to one or more recipients and returns the Message-ID assigned by the remote server (e.g. SES).
func (r *SMTPRelay) Send(from string, recipients []string, message []byte) (string, error) {
	return r.SendWithEnvelope(from, recipients, message)
}

// SendWithEnvelope sends an email using envelopeFrom for SMTP MAIL FROM.
// Issues RCPT TO for each recipient. If any RCPT TO is rejected, the transaction is aborted.
// Returns the Message-ID assigned by the remote SMTP server from the DATA response.
// Retries transient SMTP errors (4xx) up to 3 times with backoff.
func (r *SMTPRelay) SendWithEnvelope(envelopeFrom string, recipients []string, message []byte) (string, error) {
	if !r.Configured() {
		return "", fmt.Errorf("outbound SMTP relay not configured")
	}

	var lastErr error
	for attempt := 0; attempt <= len(smtpRetryBackoffs); attempt++ {
		msgID, err := r.sendOnce(envelopeFrom, recipients, message)
		if err == nil {
			return msgID, nil
		}
		lastErr = err
		if !isTransientSMTPError(lastErr) {
			return "", lastErr
		}
		if attempt < len(smtpRetryBackoffs) {
			log.Printf("[smtp-relay] transient error sending to %v (attempt %d/%d), retrying in %s: %v",
				recipients, attempt+1, len(smtpRetryBackoffs)+1, smtpRetryBackoffs[attempt], lastErr)
			time.Sleep(smtpRetryBackoffs[attempt])
		}
	}
	return "", lastErr
}

// sendOnce performs a single SMTP send using smtp.Client for the handshake,
// then drives the DATA command manually via c.Text to capture the response.
// Issues RCPT TO for each recipient; aborts if any is rejected.
// SES returns the assigned Message-ID in the 250 response after DATA, e.g.:
//
//	250 Ok <010f019d2bd82cd5-49c4925c-...@us-east-2.amazonses.com>
func (r *SMTPRelay) sendOnce(envelopeFrom string, recipients []string, message []byte) (string, error) {
	addr := net.JoinHostPort(r.cfg.Host, fmt.Sprintf("%d", r.cfg.Port))

	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("dial: %w", err)
	}
	// Set an overall deadline so a hanging server can't block forever
	conn.SetDeadline(time.Now().Add(2 * time.Minute))

	c, err := smtp.NewClient(conn, r.cfg.Host)
	if err != nil {
		conn.Close()
		return "", fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	// STARTTLS. Negotiate whenever the server advertises it; track
	// whether the connection actually became encrypted so we can fail
	// closed below. A network attacker can strip the STARTTLS capability
	// from the EHLO response to force a cleartext relay — RequireTLS
	// turns that into a hard error instead of a silent downgrade.
	tlsActive := false
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: r.cfg.Host}); err != nil {
			return "", fmt.Errorf("starttls: %w", err)
		}
		tlsActive = true
	}
	if r.cfg.RequireTLS != nil && *r.cfg.RequireTLS && !tlsActive {
		return "", fmt.Errorf("outbound smtp: server did not offer STARTTLS and require_tls is set; refusing to relay in cleartext")
	}

	// Auth. Never send PLAIN credentials over an unencrypted connection,
	// regardless of RequireTLS — that would leak the relay username and
	// password to anyone on the path. This is a hard floor below the
	// RequireTLS policy.
	if r.cfg.Username != "" {
		if !tlsActive {
			return "", fmt.Errorf("outbound smtp: refusing to send PLAIN auth over an unencrypted connection")
		}
		auth := smtp.PlainAuth("", r.cfg.Username, r.cfg.Password, r.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return "", fmt.Errorf("auth: %w", err)
		}
	}

	// MAIL FROM
	if err := c.Mail(envelopeFrom); err != nil {
		return "", fmt.Errorf("mail from: %w", err)
	}

	// RCPT TO — one per recipient; abort if any is rejected
	for _, rcpt := range recipients {
		if err := c.Rcpt(rcpt); err != nil {
			return "", fmt.Errorf("rcpt to %s: %w", rcpt, err)
		}
	}

	// DATA — drive manually via c.Text to capture the 250 response text.
	// smtp.Client.Data() would consume and discard the response message.
	text := c.Text

	// Send DATA command
	id, err := text.Cmd("DATA")
	if err != nil {
		return "", fmt.Errorf("data cmd: %w", err)
	}
	text.StartResponse(id)
	_, _, err = text.ReadResponse(354)
	text.EndResponse(id)
	if err != nil {
		return "", fmt.Errorf("data response: %w", err)
	}

	// Write message body using dot-encoding writer
	w := text.DotWriter()
	if _, err := w.Write(message); err != nil {
		w.Close()
		return "", fmt.Errorf("data write: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("data close: %w", err)
	}

	// After DotWriter.Close() sends the terminating ".\r\n", the server's
	// 250 response is waiting in the buffer. Read it directly.
	_, msg, err := text.ReadResponse(250)
	if err != nil {
		return "", fmt.Errorf("data final: %w", err)
	}

	// Parse Message-ID from response like "Ok <xxx@us-east-2.amazonses.com>"
	sesMessageID := parseMessageIDFromResponse(msg)

	c.Quit()
	return sesMessageID, nil
}

// parseMessageIDFromResponse extracts a Message-ID from an SMTP response string.
// SES format: "Ok <010f019d...@us-east-2.amazonses.com>"
// Some SES endpoints return bare IDs without angle brackets: "Ok 010f019d..."
// Returns the full angle-bracket ID including <>, or the bare ID wrapped in <>.
func parseMessageIDFromResponse(resp string) string {
	if i := strings.Index(resp, "<"); i >= 0 {
		if j := strings.Index(resp[i:], ">"); j >= 0 {
			return resp[i : i+j+1]
		}
	}
	// Fallback: strip "Ok " prefix and wrap in angle brackets
	trimmed := strings.TrimSpace(resp)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimPrefix(trimmed, "Ok ")
	return "<" + trimmed + ">"
}

// isTransientSMTPError returns true for SMTP 4xx errors that are worth retrying.
func isTransientSMTPError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// SMTP 4xx errors start with "4" in the response code
	// net/smtp returns errors like "450 ..." or "421 ..."
	if len(msg) >= 3 && msg[0] == '4' {
		return true
	}
	// Some relay providers return rate limit messages
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "throttl") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "try again")
}
