package agent

import (
	"context"
	"fmt"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/loopback"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// isSelfSend / stripAgentSelfAliases delegate to internal/loopback so the
// hitlworker package can share the same predicate logic without an upward
// import. Kept as unexported aliases here to minimize the diff at every
// call site and to preserve the existing test export below.
func isSelfSend(req outbound.SendRequest, agentEmail string) bool {
	return loopback.IsSelfSend(req, agentEmail)
}

// StripAgentSelfAliases is the exported seam over stripAgentSelfAliases so the
// v1 httpapi reply/forward builders reuse the same self-alias stripping.
func StripAgentSelfAliases(addrs []string, agentEmail string) []string {
	return stripAgentSelfAliases(addrs, agentEmail)
}

func stripAgentSelfAliases(addrs []string, agentEmail string) []string {
	return loopback.StripAgentSelfAliases(addrs, agentEmail)
}

// performSelfSend writes the message as BOTH an outbound row (sender's
// sent history) and an inbound row (recipient's inbox). Mirrors the
// two-row shape the SMTP roundtrip would produce naturally, so
// list_messages, threading, and downstream tooling don't need any
// special-casing.
//
// This is the non-HITL fast path. The HITL-gated counterpart
// (selfSendApprovalDelivery) writes ONLY the inbound row; the outbound
// row already exists from holdForApproval and gets updated to
// status=sent by ApproveAndSend.
//
// Returns the provider-style message id used for the outbound row.
// Method on the outbound row is "loopback" so operators can tell the
// difference from "smtp" in logs and audits.
// msgType is one of "send", "reply", or "forward" — recorded on the
// outbound row so audit queries can distinguish a self-note from a
// self-reply or self-forward. Without it, the loopback branch of
// handleReplyToMessage / handleForwardMessage would store "send" and
// fork the audit shape from the SMTP branch (which records the
// caller's actual intent).
func (a *API) performSelfSend(
	ctx context.Context,
	agent *identity.AgentIdentity,
	req outbound.SendRequest,
	msgType string,
) (string, error) {
	email := agent.EmailAddress()

	// Allocate providerID up front so the outbound row and inbound row
	// reference the same Message-ID — matching the two-row shape an
	// SMTP roundtrip produces.
	providerID := loopback.ProviderID(a.fromDomain)

	if _, err := a.store.CreateOutboundMessage(
		ctx,
		agent.ID,
		[]string{email},
		nil,
		nil,
		req.Subject,
		msgType,
		"loopback",
		providerID,
		req.ConversationID,
	); err != nil {
		return "", fmt.Errorf("self-send outbound row: %w", err)
	}

	rawMessage, err := loopback.ComposeMIME(agent, req, providerID, a.fromDomain)
	if err != nil {
		return "", fmt.Errorf("self-send compose: %w", err)
	}
	if _, err := a.store.CreateInboundMessage(
		ctx,
		"",
		agent.ID,
		email,
		email,
		providerID,
		req.Subject,
		req.ConversationID,
		"unread",
		rawMessage,
		nil,
		[]string{email},
		nil,
		nil,
	); err != nil {
		return "", fmt.Errorf("self-send inbound row: %w", err)
	}

	return providerID, nil
}

// selfSendApprovalDelivery is the HITL-gated counterpart of performSelfSend:
// it writes ONLY the inbound row (via loopback.DeliverInbound) and returns
// an identity.SendResult shaped for ApproveAndSend's send callback. The
// pre-existing held outbound row is finalized to status=sent by
// ApproveAndSend itself using the result's provider_message_id + method
// columns — calling CreateOutboundMessage here would create a duplicate
// row and unanchor the operator-visible audit trail (held → sent for a
// specific row id).
//
// Failure-mode note: the inbound row write happens INSIDE ApproveAndSend's
// send callback but uses a non-tx connection. If the callback succeeds but
// the subsequent tx UPDATE / Commit fails, the inbound row will exist
// while the outbound row stays pending_approval. Same crash-window class
// as the existing SES-side issue documented on ApproveAndSend's docstring.
// Operator-visible symptom: a "self-message" inbox row with no matching
// status=sent outbound row.
func (a *API) selfSendApprovalDelivery(
	ctx context.Context,
	agent *identity.AgentIdentity,
	req outbound.SendRequest,
) (identity.SendResult, error) {
	return loopback.DeliverInbound(ctx, a.store, agent, req, a.fromDomain)
}
