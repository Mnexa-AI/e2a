package outbound

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime"
	"net/textproto"
	"strings"
	"time"
)

// NOTE: Message-ID is intentionally omitted from composed messages.
// SES assigns its own Message-ID on send, and we capture it from the
// SMTP response. This avoids a mismatch where recipients see the SES ID
// but we tracked a different one.

// ComposeMessage builds an RFC 2822 email message (single content type).
// Message-ID is omitted — SES assigns one on send.
// If to is empty, the To: header is omitted entirely (CC-only send).
// BCC is never written to headers — it is handled at the SMTP envelope level.
// When conversationID is non-empty, an X-E2A-Conversation-ID header is written
// so recipient agents on this platform can continue the same application thread
// without depending on In-Reply-To chains.
func ComposeMessage(from string, to []string, cc []string, subject, body, contentType, replyToMsgID, fromDomain, replyTo, conversationID string) ([]byte, error) {
	if contentType == "" {
		contentType = "text/plain"
	}

	var buf strings.Builder
	writeHeader := headerWriter(&buf)

	writeHeader("From", from)
	if len(to) > 0 {
		writeHeader("To", strings.Join(to, ", "))
	}
	if len(cc) > 0 {
		writeHeader("Cc", strings.Join(cc, ", "))
	}
	if replyTo != "" {
		writeHeader("Reply-To", replyTo)
	}
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", subject))
	writeHeader("Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader("MIME-Version", "1.0")
	writeHeader("Content-Type", contentType+"; charset=utf-8")

	if replyToMsgID != "" {
		writeHeader("In-Reply-To", replyToMsgID)
		writeHeader("References", replyToMsgID)
	}
	if conversationID != "" {
		writeHeader("X-E2A-Conversation-ID", conversationID)
	}

	buf.WriteString("\r\n")
	buf.WriteString(body)

	return []byte(buf.String()), nil
}

// ComposeMultipartMessage builds an RFC 2822 multipart/alternative email with text and HTML parts.
// If htmlBody is empty, falls back to a single text/plain message via ComposeMessage.
func ComposeMultipartMessage(from string, to []string, cc []string, subject, textBody, htmlBody, replyToMsgID, fromDomain, replyTo, conversationID string) ([]byte, error) {
	if htmlBody == "" {
		return ComposeMessage(from, to, cc, subject, textBody, "text/plain", replyToMsgID, fromDomain, replyTo, conversationID)
	}

	boundary := generateBoundary()

	var buf strings.Builder
	writeHeader := headerWriter(&buf)

	writeHeader("From", from)
	if len(to) > 0 {
		writeHeader("To", strings.Join(to, ", "))
	}
	if len(cc) > 0 {
		writeHeader("Cc", strings.Join(cc, ", "))
	}
	if replyTo != "" {
		writeHeader("Reply-To", replyTo)
	}
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", subject))
	writeHeader("Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader("MIME-Version", "1.0")
	writeHeader("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))

	if replyToMsgID != "" {
		writeHeader("In-Reply-To", replyToMsgID)
		writeHeader("References", replyToMsgID)
	}
	if conversationID != "" {
		writeHeader("X-E2A-Conversation-ID", conversationID)
	}

	buf.WriteString("\r\n")

	// text/plain part
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	buf.WriteString(textBody)
	buf.WriteString("\r\n")

	// text/html part
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	buf.WriteString(htmlBody)
	buf.WriteString("\r\n")

	// closing boundary
	buf.WriteString("--" + boundary + "--\r\n")

	return []byte(buf.String()), nil
}

// ComposeMessageWithAttachments builds an RFC 2822 multipart/mixed email with attachments.
// If no attachments are provided, falls back to ComposeMultipartMessage.
func ComposeMessageWithAttachments(from string, to []string, cc []string, subject, textBody, htmlBody, replyToMsgID, fromDomain, replyTo, conversationID string, attachments []Attachment) ([]byte, error) {
	// Defense-in-depth header-injection guard: reject any attachment
	// whose user-supplied Filename or ContentType contains CR or LF.
	// fmt.Sprintf("%q", ...) escapes Filename safely, but ContentType
	// is written via "%s" and would inject extra MIME headers if it
	// contained "\r\n" — so reject before composing.
	for _, att := range attachments {
		if strings.ContainsAny(att.Filename, "\r\n") {
			return nil, fmt.Errorf("attachment filename contains CR/LF: header injection refused")
		}
		if strings.ContainsAny(att.ContentType, "\r\n") {
			return nil, fmt.Errorf("attachment content_type contains CR/LF: header injection refused")
		}
	}
	if len(attachments) == 0 {
		return ComposeMultipartMessage(from, to, cc, subject, textBody, htmlBody, replyToMsgID, fromDomain, replyTo, conversationID)
	}

	mixedBoundary := generateBoundary()

	var buf strings.Builder
	writeHeader := headerWriter(&buf)

	writeHeader("From", from)
	if len(to) > 0 {
		writeHeader("To", strings.Join(to, ", "))
	}
	if len(cc) > 0 {
		writeHeader("Cc", strings.Join(cc, ", "))
	}
	if replyTo != "" {
		writeHeader("Reply-To", replyTo)
	}
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", subject))
	writeHeader("Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader("MIME-Version", "1.0")
	writeHeader("Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", mixedBoundary))

	if replyToMsgID != "" {
		writeHeader("In-Reply-To", replyToMsgID)
		writeHeader("References", replyToMsgID)
	}
	if conversationID != "" {
		writeHeader("X-E2A-Conversation-ID", conversationID)
	}

	buf.WriteString("\r\n")

	// Body part
	if htmlBody != "" {
		altBoundary := generateBoundary()
		buf.WriteString("--" + mixedBoundary + "\r\n")
		buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q\r\n\r\n", altBoundary))

		buf.WriteString("--" + altBoundary + "\r\n")
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		buf.WriteString(textBody)
		buf.WriteString("\r\n")

		buf.WriteString("--" + altBoundary + "\r\n")
		buf.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
		buf.WriteString(htmlBody)
		buf.WriteString("\r\n")

		buf.WriteString("--" + altBoundary + "--\r\n")
	} else {
		buf.WriteString("--" + mixedBoundary + "\r\n")
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		buf.WriteString(textBody)
		buf.WriteString("\r\n")
	}

	// Attachment parts
	for _, att := range attachments {
		buf.WriteString("--" + mixedBoundary + "\r\n")
		buf.WriteString(fmt.Sprintf("Content-Type: %s\r\n", att.ContentType))
		buf.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=%q\r\n", att.Filename))
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")

		// att.Data is already base64-encoded from the API request
		// Wrap at 76 chars per RFC 2045
		data := att.Data
		for len(data) > 76 {
			buf.WriteString(data[:76])
			buf.WriteString("\r\n")
			data = data[76:]
		}
		if len(data) > 0 {
			buf.WriteString(data)
			buf.WriteString("\r\n")
		}
	}

	buf.WriteString("--" + mixedBoundary + "--\r\n")

	return []byte(buf.String()), nil
}

// DecodeAttachmentData decodes a base64-encoded attachment data string.
func DecodeAttachmentData(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}

func headerWriter(buf *strings.Builder) func(string, string) {
	return func(key, value string) {
		buf.WriteString(textproto.CanonicalMIMEHeaderKey(key))
		buf.WriteString(": ")
		buf.WriteString(sanitizeHeaderValue(value))
		buf.WriteString("\r\n")
	}
}

// sanitizeHeaderValue strips CR and LF to prevent header injection.
// Without this, an attacker-controlled value like "abc\r\nBcc: leak@evil.com"
// in conversation_id (or any other passthrough header) would smuggle
// arbitrary headers into the composed message — a blind-Bcc /
// fake-DKIM-Signature primitive available to any authenticated user.
// Stripping is preferred over rejecting so the request still succeeds
// with the malicious bytes neutralised; the API layer additionally
// validates conversation_id and returns 400 on CRLF, but this is the
// last line of defense for any future caller.
func sanitizeHeaderValue(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

func generateBoundary() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure means the OS RNG is broken — nothing
		// downstream will work either. Panic so the caller surfaces a
		// 500 rather than silently emitting an all-zero boundary that
		// could collide across messages.
		panic(fmt.Sprintf("compose: crypto/rand failed: %v", err))
	}
	return fmt.Sprintf("e2a_%x", b)
}
