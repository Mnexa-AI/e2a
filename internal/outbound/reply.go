package outbound

import (
	"bytes"
	"fmt"
	"net/mail"
	"strings"
)

// ReplyRecipients holds the resolved To and CC lists for a reply.
type ReplyRecipients struct {
	To []string
	CC []string
}

// ParseReplyRecipients resolves reply recipients from a raw inbound email.
//
// Reply To is determined by Reply-To if present, otherwise From — both parsed
// as address lists. All parsed mailboxes become To recipients.
//
// If replyAll is true, original To and CC recipients are added to CC.
// The agent's own address is excluded from all fields. Explicit extraCC
// addresses are merged into CC.
//
// Normalization, deduplication, and self-removal are handled downstream by
// Sender.Send(). This function does best-effort parsing and returns raw
// lowercase addresses.
func ParseReplyRecipients(rawMessage []byte, replyAll bool, extraCC []string) (*ReplyRecipients, error) {
	result := &ReplyRecipients{}

	// Parse raw message headers
	if len(rawMessage) > 0 {
		msg, err := mail.ReadMessage(bytes.NewReader(rawMessage))
		if err != nil {
			if replyAll {
				return nil, fmt.Errorf("reply_all requires parseable inbound headers: %w", err)
			}
			// Non-reply-all: fall through, caller uses inbound.Sender
		} else {
			// Resolve reply To: Reply-To if present, otherwise From
			replyToHeader := msg.Header.Get("Reply-To")
			if replyToHeader != "" {
				result.To = parseAddressList(replyToHeader)
			} else {
				fromHeader := msg.Header.Get("From")
				result.To = parseAddressList(fromHeader)
			}

			if replyAll {
				// Add original To and CC to reply CC
				toHeader := msg.Header.Get("To")
				ccHeader := msg.Header.Get("Cc")
				result.CC = append(result.CC, parseAddressList(toHeader)...)
				result.CC = append(result.CC, parseAddressList(ccHeader)...)
			}
		}
	} else if replyAll {
		return nil, fmt.Errorf("reply_all requires inbound raw_message but none was stored")
	}

	// Merge explicit CC from request (always, even without raw message)
	for _, addr := range extraCC {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			result.CC = append(result.CC, strings.ToLower(addr))
		}
	}

	return result, nil
}

// parseAddressList parses an RFC 5322 address list header and returns
// lowercased bare email addresses. Invalid entries are silently skipped.
func parseAddressList(header string) []string {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	addrs, err := mail.ParseAddressList(header)
	if err != nil {
		// Try single address as fallback
		addr, err := mail.ParseAddress(header)
		if err != nil {
			return nil
		}
		return []string{strings.ToLower(addr.Address)}
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, strings.ToLower(a.Address))
	}
	return out
}
