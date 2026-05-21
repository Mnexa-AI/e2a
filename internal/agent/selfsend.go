package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

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
func isSelfSend(req outbound.SendRequest, agentEmail string) bool {
	if len(req.CC) != 0 || len(req.BCC) != 0 {
		return false
	}
	if len(req.To) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(req.To[0]), agentEmail)
}

// performSelfSend writes the message as BOTH an outbound row (sender's
// sent history) and an inbound row (recipient's inbox). Mirrors the
// two-row shape the SMTP roundtrip would produce naturally, so
// list_messages, threading, and downstream tooling don't need any
// special-casing.
//
// Notable behavior choices (documented because they diverge from a
// pure-SMTP send):
//   - HITL is bypassed before this function is called. Holding a
//     self-note for approval is degenerate UX.
//   - Webhook + WebSocket delivery are intentionally NOT fired on the
//     inbound row. Cloud-mode agents whose webhook handler is what
//     triggered the self-send would otherwise re-enter their own code
//     and loop. Local-mode agents (the common case) pick up the row
//     via the next list_messages poll, which IS the intended UX.
//   - Domain verification + rate limit are still enforced upstream;
//     loopback isn't a backdoor to bypass those gates.
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

	rawBody := []byte(req.Body)
	if req.HTMLBody != "" {
		rawBody = []byte(req.HTMLBody)
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
		rawBody,
		nil, // no DKIM/SPF auth headers on a synthetic loopback
		[]string{email},
		nil, // cc
		nil, // reply_to — empty per CreateInboundMessage docstring ("never silently falls back to sender")
	); err != nil {
		return "", fmt.Errorf("self-send inbound row: %w", err)
	}

	return providerID, nil
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
