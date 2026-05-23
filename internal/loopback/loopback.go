// Package loopback delivers agent-to-self messages without touching the
// upstream SMTP relay. A self-send (agent emails itself) is a degenerate
// case for SMTP: the relay would refuse it as a self-spam guard, and the
// roundtrip through SES + MX + the local SMTP receiver is wasted work
// when the destination is local anyway.
//
// Two callers, two entry points:
//
//   - internal/agent reaches us via DeliverInbound from the HITL
//     approval finalizer (selfSendApprovalDelivery) and from its own
//     fast path (performSelfSend). The fast path writes the outbound
//     row itself; the approval path lets ApproveAndSend update the
//     pre-existing held outbound row.
//   - internal/hitlworker reaches us via DeliverInbound from the TTL
//     auto-approve callback. Same shape as the user-driven approval
//     path — the held outbound row already exists; we only write the
//     inbound counterpart.
//
// Living here (rather than in internal/agent) lets the worker import us
// without dragging the entire agent package's surface in. Mirrors the
// existing duplication strategy for sendRequestFromStoredMessage but
// avoids it for the larger MIME-composition body.
package loopback

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"context"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// IsSelfSend reports whether req targets only the sender's own inbox —
// i.e., the agent is writing a note to itself. Returns true only when
// there's a single To recipient that matches the agent's own address
// (case-insensitive, trimmed) AND no Cc/Bcc — any mixed/external
// recipient routes through normal SMTP unchanged.
//
// Callers that have CC/BCC carrying agent aliases (e.g. the reply path
// with replyAll=true on a self-thread, where the original message's CC
// list already includes the agent) should strip them via
// StripAgentSelfAliases before checking — outbound.Sender does the same
// alias-strip downstream as a self-spam guard, so doing it here is
// purely "see through" the aliases earlier.
func IsSelfSend(req outbound.SendRequest, agentEmail string) bool {
	if len(req.CC) != 0 || len(req.BCC) != 0 {
		return false
	}
	if len(req.To) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(req.To[0]), agentEmail)
}

// StripAgentSelfAliases removes case-insensitive, whitespace-trimmed
// matches of agentEmail from addrs. Used to pre-clean reply recipients
// so IsSelfSend can recognize replyAll-on-a-self-thread as still a
// self-send. Returns a fresh slice; the input is not mutated.
func StripAgentSelfAliases(addrs []string, agentEmail string) []string {
	if len(addrs) == 0 {
		return addrs
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if !strings.EqualFold(strings.TrimSpace(a), agentEmail) {
			out = append(out, a)
		}
	}
	return out
}

// ProviderID synthesizes an RFC 5322-shaped Message-ID for the outbound
// row's provider_message_id column. Mirrors what an external MTA would
// have stamped — keeps the column non-empty and recognizable in
// operator queries (the "@loopback.<domain>" host portion makes
// self-sends greppable across the dataset).
func ProviderID(fromDomain string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	host := fromDomain
	if host == "" {
		host = "e2a.local"
	}
	return fmt.Sprintf("<%s@loopback.%s>", hex.EncodeToString(b[:]), host)
}

// ComposeMIME builds the RFC 5322 / 2046 message bytes the inbound row
// will store as raw_message.
//
// Delegates to the same composer the real SMTP path uses
// (outbound.ComposeMessageWithAttachments) so the produced message is
// byte-equivalent to what an external roundtrip would have generated —
// same headers, same multipart structure, same attachment encoding.
// The SDK's InboundEmail.fromPayload → parseRawEmail pipeline then
// finds body text/html AND attachments without any loopback-specific
// branch.
//
// Prepends ONE synthetic Received: line per RFC 5321 §4.4 ("each time a
// message is relayed... the receiving SMTP server MUST insert a
// 'Received:' line"). Mature local-delivery MTAs (sendmail's local
// mailer, Postfix's local daemon, Exim's local_smtp transport) all add
// such a line even for same-host delivery; doing the same here keeps
// stored messages forensically self-documenting. The "loopback" keyword
// is the searchable signal — `grep "with loopback"` over raw messages
// finds every self-send.
//
// Threading: req.ReplyToMessageID and req.References are passed through
// to the composer, so self-replies that reach this path via the HITL
// approval finalizer preserve In-Reply-To / References headers — same
// shape an SMTP-routed reply would carry.
func ComposeMIME(agent *identity.AgentIdentity, req outbound.SendRequest, providerID, fromDomain string) ([]byte, error) {
	email := agent.EmailAddress()
	headerFrom := fmt.Sprintf("%q <%s>", agent.Name, email)
	if agent.Name == "" {
		headerFrom = email
	}

	var msg []byte
	var err error
	if len(req.Attachments) > 0 {
		msg, err = outbound.ComposeMessageWithAttachments(
			headerFrom,
			[]string{email},
			nil, // cc
			req.Subject,
			req.Body,
			req.HTMLBody,
			req.ReplyToMessageID,
			req.References,
			fromDomain,
			"", // reply_to header
			req.ConversationID,
			req.Attachments,
		)
	} else {
		// No attachments → simpler single-part path. Lets us keep the
		// stored MIME small for the common "note to self" case rather
		// than always emitting a multipart envelope.
		contentType := "text/plain"
		body := req.Body
		if req.HTMLBody != "" {
			contentType = "text/html"
			body = req.HTMLBody
		}
		msg, err = outbound.ComposeMessage(
			headerFrom,
			[]string{email},
			nil, // cc
			req.Subject,
			body,
			contentType,
			req.ReplyToMessageID,
			req.References,
			fromDomain,
			"", // reply_to header
			req.ConversationID,
		)
	}
	if err != nil {
		return nil, err
	}

	host := fromDomain
	if host == "" {
		host = "e2a.local"
	}
	received := fmt.Sprintf(
		"Received: by %s (e2a) with loopback id %s for <%s>;\r\n\t%s\r\n",
		host,
		providerID,
		email,
		time.Now().UTC().Format(time.RFC1123Z),
	)
	return append([]byte(received), msg...), nil
}

// InboundWriter is the subset of *identity.Store DeliverInbound uses.
// Lets tests swap in fakes; production code passes the real store.
type InboundWriter interface {
	CreateInboundMessage(ctx context.Context, id, agentID, senderEmail, recipient, emailMessageID, subject, conversationID, deliveryStatus string, rawMessage []byte, authHeaders map[string]string, toRecipients, cc, replyTo []string) (*identity.Message, error)
}

// DeliverInbound writes the recipient-side row for a loopback self-send
// and returns an identity.SendResult shaped for the ApproveAndSend send
// callback (or the worker's ExpireApproveAndSend send callback).
//
// Does NOT write the outbound row — the caller is one of:
//   - HITL approval: the held outbound row already exists;
//     ApproveAndSend's UPDATE flips it to status=sent + method=loopback
//     using the result's columns.
//   - hitlworker TTL auto-approve: same shape via ExpireApproveAndSend.
//
// The non-HITL fast path in internal/agent (performSelfSend) calls
// CreateOutboundMessage itself before calling DeliverInbound; that
// path has no held row to update.
//
// Notable choices documented because they diverge from a pure-SMTP
// send:
//   - Webhook + WebSocket delivery are intentionally NOT fired on the
//     inbound row. Cloud-mode agents whose webhook handler triggered
//     the send would otherwise re-enter their own code and loop.
//     Local-mode agents pick up the row via the next list_messages
//     poll, which IS the intended UX.
//   - auth_headers stays NULL: no DKIM/SPF was actually evaluated
//     because nothing arrived over the wire. The operator-facing signal
//     "this row didn't come from external mail" is preserved by that
//     null column.
//   - Domain verification + rate limit are enforced upstream by the
//     caller. Loopback isn't a backdoor for those gates.
func DeliverInbound(ctx context.Context, store InboundWriter, agent *identity.AgentIdentity, req outbound.SendRequest, fromDomain string) (identity.SendResult, error) {
	providerID := ProviderID(fromDomain)
	email := agent.EmailAddress()

	rawMessage, err := ComposeMIME(agent, req, providerID, fromDomain)
	if err != nil {
		return identity.SendResult{}, fmt.Errorf("loopback compose: %w", err)
	}
	if _, err := store.CreateInboundMessage(
		ctx,
		"", // generate fresh id; mirrors the SMTP path which never reuses outbound ids
		agent.ID,
		email, // sender
		email, // recipient (per-delivery target)
		providerID,
		req.Subject,
		req.ConversationID,
		"unread",
		rawMessage,
		nil, // no DKIM/SPF auth headers on a synthetic loopback
		[]string{email},
		nil, // cc
		nil, // reply_to
	); err != nil {
		return identity.SendResult{}, fmt.Errorf("loopback inbound row: %w", err)
	}

	return identity.SendResult{
		ProviderMessageID: providerID,
		Method:            "loopback",
		To:                []string{email},
	}, nil
}
