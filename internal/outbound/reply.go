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

// BuildReferencesChain returns the References chain to write on a reply,
// per RFC 5322 § 3.6.4:
//
//   - If the parent has a References header, return: parent.References ++ [parentMsgID]
//   - Else if the parent has an In-Reply-To header, return: parent.InReplyTo ++ [parentMsgID]
//   - Else return: [parentMsgID] (the reply still has at least the parent in its chain)
//
// parentMsgID must be the canonicalized RFC 5322 Message-ID of the inbound
// being replied to (i.e. the same value the caller passes as
// SendRequest.ReplyToMessageID for the In-Reply-To header). Empty input
// yields an empty chain — callers fall back to legacy single-id behavior.
//
// This chain matters for multi-party threads where one participant's reply
// is delivered only to a subset of the recipients (e.g. agent-mediated
// scheduler scenarios). Without the full chain, a downstream reply-to-all
// has In-Reply-To pointing at a Message-ID that recipients outside the
// subset have never seen, and Gmail/other clients fork the thread.
func BuildReferencesChain(rawMessage []byte, parentMsgID string) []string {
	if parentMsgID == "" {
		return nil
	}

	var prior []string
	if len(rawMessage) > 0 {
		if msg, err := mail.ReadMessage(bytes.NewReader(rawMessage)); err == nil {
			if refs := strings.TrimSpace(msg.Header.Get("References")); refs != "" {
				prior = parseMessageIDList(refs)
			} else if irt := strings.TrimSpace(msg.Header.Get("In-Reply-To")); irt != "" {
				// In-Reply-To SHOULD contain a single id, but some clients
				// pack multiple. Treat it the same way as References as a
				// pragmatic recovery — better than dropping prior context.
				prior = parseMessageIDList(irt)
			}
		}
		// Parse failures fall through silently — better to send a reply
		// with a shorter chain than to fail the whole send. The reply
		// will still thread for participants who saw the parent.
	}

	chain := make([]string, 0, len(prior)+1)
	for _, id := range prior {
		// Drop the parent if it was already in the prior chain — append it
		// once at the end so the chain ends with the immediate parent
		// (which mirrors what In-Reply-To points at).
		if id != parentMsgID {
			chain = append(chain, id)
		}
	}
	chain = append(chain, parentMsgID)
	return chain
}

// parseMessageIDList splits a References / In-Reply-To header value into
// individual message-ids. The header format is one or more <local@domain>
// tokens separated by whitespace (RFC 5322 § 3.6.4); the official grammar
// is permissive enough that we tokenize on whitespace and then trim — any
// fragment that isn't bracketed is dropped.
func parseMessageIDList(header string) []string {
	if header == "" {
		return nil
	}
	fields := strings.Fields(header)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if strings.HasPrefix(f, "<") && strings.HasSuffix(f, ">") && len(f) > 2 {
			out = append(out, f)
		}
	}
	return out
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
