package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// isSelfSend reports whether req targets only the sender's own
// inbox — i.e., the agent is writing a note to itself. Used by
// handleSendEmail to short-circuit the SMTP path: a round-trip
// through SES + MX + the local SMTP receiver is wasted work when
// the destination is local. Returns true only when there's a
// single To recipient that matches the agent's own address (case-
// insensitive, trimmed) AND no Cc/Bcc — any mixed/external
// recipient routes through normal SMTP unchanged.
//
// Callers that have CC/BCC carrying agent aliases (e.g. the reply
// path with replyAll=true on a self-thread, where the original
// message's CC list already includes the agent) should strip them
// via stripAgentSelfAliases before checking — outbound.Sender does
// the same alias-strip downstream as a self-spam guard, so doing
// it here is purely "see through" the aliases earlier.
func isSelfSend(req outbound.SendRequest, agentEmail string) bool {
	if len(req.CC) != 0 || len(req.BCC) != 0 {
		return false
	}
	if len(req.To) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(req.To[0]), agentEmail)
}

// stripAgentSelfAliases removes case-insensitive, whitespace-trimmed
// matches of agentEmail from addrs. Used to pre-clean reply
// recipients so isSelfSend can recognize replyAll-on-a-self-thread
// as still a self-send. Returns a fresh slice; the input is not
// mutated.
func stripAgentSelfAliases(addrs []string, agentEmail string) []string {
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

// performSelfSend writes the message as BOTH an outbound row (sender's
// sent history) and an inbound row (recipient's inbox). Mirrors the
// two-row shape the SMTP roundtrip would produce naturally, so
// list_messages, threading, and downstream tooling don't need any
// special-casing.
//
// The inbound row's raw_message is a full, RFC 5322-conformant MIME
// message — composed via outbound.ComposeMessageWithAttachments, the
// same function the real SMTP path uses — and prefixed with one
// synthetic `Received:` line per RFC 5321 §4.4 documenting that
// delivery happened via the local loopback rather than over SMTP.
// Doing the composition here (instead of stashing just the body text)
// means the SDK's InboundEmail.fromPayload parser finds attachments
// + body parts naturally; without it, self-sends with attachments
// would silently drop the attachments on read.
//
// Notable behavior choices (documented because they diverge from a
// pure-SMTP send):
//   - This is the non-HITL fast path. The HITL-gated counterpart
//     (selfSendApprovalDelivery) writes ONLY the inbound row; the
//     outbound row already exists from holdForApproval and gets
//     updated to status=sent by ApproveAndSend.
//   - Webhook + WebSocket delivery are intentionally NOT fired on the
//     inbound row. Cloud-mode agents whose webhook handler is what
//     triggered the self-send would otherwise re-enter their own code
//     and loop. Local-mode agents (the common case) pick up the row
//     via the next list_messages poll, which IS the intended UX.
//   - Domain verification + rate limit are still enforced upstream;
//     loopback isn't a backdoor to bypass those gates.
//   - auth_headers stays NULL on the inbound row: no DKIM/SPF was
//     actually evaluated because nothing arrived over the wire. The
//     operator-facing signal "this row didn't come from external mail"
//     is preserved by that null column.
//
// Returns the provider-style message id used for the outbound row.
// Method on the outbound row is "loopback" so operators can tell the
// difference from "smtp" in logs and audits.
func (a *API) performSelfSend(
	ctx context.Context,
	agent *identity.AgentIdentity,
	req outbound.SendRequest,
) (string, error) {
	providerID := loopbackProviderID(a.fromDomain)
	email := agent.EmailAddress()

	if _, err := a.store.CreateOutboundMessage(
		ctx,
		agent.ID,
		[]string{email},
		nil,
		nil,
		req.Subject,
		"send",
		"loopback",
		providerID,
		req.ConversationID,
	); err != nil {
		return "", fmt.Errorf("self-send outbound row: %w", err)
	}

	rawMessage, err := composeLoopbackMIME(agent, req, providerID, a.fromDomain)
	if err != nil {
		return "", fmt.Errorf("self-send compose: %w", err)
	}

	if _, err := a.store.CreateInboundMessage(
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
		nil, // reply_to — empty per CreateInboundMessage docstring ("never silently falls back to sender")
	); err != nil {
		return "", fmt.Errorf("self-send inbound row: %w", err)
	}

	return providerID, nil
}

// composeLoopbackMIME builds the RFC 5322 / 2046 message bytes the
// inbound row will store as raw_message.
//
// We delegate to the same composer the real SMTP path uses
// (outbound.ComposeMessageWithAttachments) so the produced message is
// byte-equivalent to what an external roundtrip would have generated
// — same headers, same multipart structure, same attachment encoding.
// The SDK's InboundEmail.fromPayload → parseRawEmail pipeline then
// finds body text/html AND attachments without any loopback-specific
// branch.
//
// We prepend ONE synthetic Received: line per RFC 5321 §4.4 ("each
// time a message is relayed... the receiving SMTP server MUST insert
// a 'Received:' line"). Mature local-delivery MTAs (sendmail's local
// mailer, Postfix's local daemon, Exim's local_smtp transport) all
// add such a line even for same-host delivery; doing the same here
// keeps stored messages forensically self-documenting. The "loopback"
// keyword is the searchable signal — `grep "with loopback"` over raw
// messages finds every self-send.
func composeLoopbackMIME(
	agent *identity.AgentIdentity,
	req outbound.SendRequest,
	providerID, fromDomain string,
) ([]byte, error) {
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
			"",  // reply_to_message_id — self-send is never a reply
			nil, // references
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
			"",  // reply_to_message_id
			nil, // references
			fromDomain,
			"", // reply_to header
			req.ConversationID,
		)
	}
	if err != nil {
		return nil, err
	}

	// Prepend the synthetic Received: line. CRLF line endings to match
	// the rest of the MIME message ComposeMessage produces. The "with
	// loopback" keyword is the canonical local-delivery indicator,
	// mirroring sendmail's "with local" and Postfix's "with LMTP".
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

// selfSendApprovalDelivery is the HITL-gated counterpart of performSelfSend:
// it writes ONLY the inbound row and returns an identity.SendResult shaped
// for ApproveAndSend's send callback. The pre-existing held outbound row is
// finalized to status=sent by ApproveAndSend itself using the result's
// provider_message_id + method columns — calling CreateOutboundMessage here
// would create a duplicate row and unanchor the operator-visible audit
// trail (held → sent for a specific row id).
//
// Same delivery semantics as performSelfSend: loopback method, synthetic
// Received: line, no webhook/WS replay. Domain verification is checked
// upstream at handleSendEmail; the approval finalize trusts the gate it
// already passed at hold time.
func (a *API) selfSendApprovalDelivery(
	ctx context.Context,
	agent *identity.AgentIdentity,
	req outbound.SendRequest,
) (identity.SendResult, error) {
	providerID := loopbackProviderID(a.fromDomain)
	email := agent.EmailAddress()

	rawMessage, err := composeLoopbackMIME(agent, req, providerID, a.fromDomain)
	if err != nil {
		return identity.SendResult{}, fmt.Errorf("self-send approval compose: %w", err)
	}
	if _, err := a.store.CreateInboundMessage(
		ctx,
		"",
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
		return identity.SendResult{}, fmt.Errorf("self-send approval inbound row: %w", err)
	}

	return identity.SendResult{
		ProviderMessageID: providerID,
		Method:            "loopback",
		To:                []string{email},
	}, nil
}

// loopbackProviderID synthesizes an RFC 5322-shaped Message-ID for the
// outbound row's provider_message_id column. Mirrors what an external
// MTA would have stamped on the message — keeps the column non-empty
// and recognizable in operator queries (the "@loopback.<domain>" host
// portion makes self-sends greppable across the dataset).
func loopbackProviderID(fromDomain string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	host := fromDomain
	if host == "" {
		host = "e2a.local"
	}
	return fmt.Sprintf("<%s@loopback.%s>", hex.EncodeToString(b[:]), host)
}
