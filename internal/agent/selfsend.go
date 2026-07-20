package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/mail"

	"github.com/jackc/pgx/v5"
	"github.com/tokencanopy/e2a/internal/eventpayload"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/loopback"
	"github.com/tokencanopy/e2a/internal/outbound"
	"github.com/tokencanopy/e2a/internal/webhookpub"
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
// HITL approval uses the same transactional local-delivery invariants through
// approveSelfSend while preserving its pre-existing outbound resource id.
//
// Returns the GET-able outbound message resource. The provider-style RFC
// Message-ID remains an internal threading key shared with the inbound twin.
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
	idemCompleteTx AcceptIdemCompleter,
) (*identity.Message, error) {
	email := agent.EmailAddress()

	// Allocate providerID up front so the outbound row and inbound row
	// reference the same Message-ID — matching the two-row shape an
	// SMTP roundtrip produces.
	providerID := loopback.ProviderID(a.fromDomain)

	rawMessage, err := loopback.ComposeMIME(agent, req, providerID, a.fromDomain)
	if err != nil {
		return nil, fmt.Errorf("self-send compose: %w", err)
	}

	var outboundMsg *identity.Message
	var receivedEvent webhookpub.Event
	err = a.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		outboundMsg, txErr = a.store.CreateOutboundMessageTx(
			ctx, tx, agent.ID, []string{email}, nil, nil, req.Subject, msgType,
			"loopback", providerID, req.ConversationID, rawMessage,
			"sent", "", "own_address",
		)
		if txErr != nil {
			return fmt.Errorf("self-send outbound row: %w", txErr)
		}

		inboundMsg, txErr := a.store.CreateInboundMessageAuthenticatedInTx(
			ctx, tx, "", agent.ID, identity.InboundAuth{HeaderFrom: email, StoredSender: loopbackDisplayFrom(req, email)}, email, providerID, req.Subject,
			req.ConversationID, "unread", rawMessage, false, "",
			[]string{email}, nil, replyToList(req.ReplyTo), identity.InboundScreening{},
		)
		if txErr != nil {
			return fmt.Errorf("self-send inbound row: %w", txErr)
		}
		if _, txErr = tx.Exec(ctx, `UPDATE messages SET method='loopback' WHERE id=$1`, inboundMsg.ID); txErr != nil {
			return fmt.Errorf("self-send inbound method: %w", txErr)
		}
		inboundMsg.Method = "loopback"

		if a.outbox != nil {
			if receivedEvent, txErr = a.publishLoopbackEventsTx(ctx, tx, agent, outboundMsg, inboundMsg, req, msgType, rawMessage); txErr != nil {
				return txErr
			}
		}

		if idemCompleteTx != nil {
			if txErr = idemCompleteTx(ctx, tx, &OutboundResult{
				MessageID: outboundMsg.ID, SentAs: "own_address", Method: "loopback",
			}); txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if a.outbox != nil {
		a.emit().OutboxEventsPublished(webhookpub.EventEmailSent)
		a.emit().OutboxEventsPublished(webhookpub.EventEmailReceived)
	}
	a.pushLoopbackReceived(agent.ID, receivedEvent)
	return outboundMsg, nil
}

func (a *API) publishLoopbackEventsTx(
	ctx context.Context,
	tx pgx.Tx,
	agent *identity.AgentIdentity,
	outboundMsg, inboundMsg *identity.Message,
	req outbound.SendRequest,
	msgType string,
	rawMessage []byte,
) (webhookpub.Event, error) {
	sentResult := &outbound.SendResult{
		Method: "loopback", To: []string{agent.EmailAddress()},
		SentAs: "own_address", Raw: rawMessage,
	}
	sentEvent := a.buildSentEvent(agent, outboundMsg, sentResult, req, msgType)
	sentEvent.ID = webhookpub.DeterministicEventID(outboundMsg.ID, webhookpub.EventEmailSent)
	if err := a.outbox.PublishTx(ctx, tx, sentEvent); err != nil {
		return webhookpub.Event{}, fmt.Errorf("self-send email.sent event: %w", err)
	}

	receivedEvent := buildLoopbackReceivedEvent(agent, inboundMsg, req, rawMessage)
	if err := a.outbox.PublishTx(ctx, tx, receivedEvent); err != nil {
		return webhookpub.Event{}, fmt.Errorf("self-send email.received event: %w", err)
	}
	return receivedEvent, nil
}

func (a *API) approveSelfSend(
	ctx context.Context,
	agent *identity.AgentIdentity,
	messageID, userID string,
	edits identity.PendingApprovalEdit,
	idemCompleteTx ApproveIdemCompleter,
) (*identity.Message, error) {
	var req outbound.SendRequest
	var receivedEvent webhookpub.Event
	sent, err := a.store.ApproveAndDeliverLocal(ctx, messageID, userID, edits,
		func(locked *identity.Message) (identity.SendResult, error) {
			var buildErr error
			req, buildErr = buildSendRequestFromMessage(locked)
			if buildErr != nil {
				return identity.SendResult{}, buildErr
			}
			attachReferencesChain(ctx, a.store, agent.ID, &req)
			if !isSelfSend(req, agent.EmailAddress()) {
				return identity.SendResult{}, errors.New("external outbound approval must be queued")
			}
			providerID := loopback.ProviderID(a.fromDomain)
			raw, composeErr := loopback.ComposeMIME(agent, req, providerID, a.fromDomain)
			if composeErr != nil {
				return identity.SendResult{}, composeErr
			}
			return identity.SendResult{
				ProviderMessageID: providerID,
				Method:            "loopback",
				To:                []string{agent.EmailAddress()},
				Sender:            loopbackDisplayFrom(req, agent.EmailAddress()),
				Raw:               raw,
			}, nil
		},
		func(ctx context.Context, tx pgx.Tx, outboundMsg, inboundMsg *identity.Message, result identity.SendResult) error {
			if a.outbox != nil {
				var hookErr error
				receivedEvent, hookErr = a.publishLoopbackEventsTx(ctx, tx, agent, outboundMsg, inboundMsg, req, outboundMsg.Type, result.Raw)
				if hookErr != nil {
					return hookErr
				}
			}
			if idemCompleteTx != nil {
				return idemCompleteTx(ctx, tx, outboundMsg)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	if a.outbox != nil {
		a.emit().OutboxEventsPublished(webhookpub.EventEmailSent)
		a.emit().OutboxEventsPublished(webhookpub.EventEmailReceived)
	}
	a.pushLoopbackReceived(agent.ID, receivedEvent)
	return sent, nil
}

func (a *API) recordLoopbackUsage(ctx context.Context, userID string, agent *identity.AgentIdentity) {
	for _, direction := range []string{"outbound", "inbound"} {
		if _, err := a.usage.RecordAndCheck(ctx, userID, agent.ID, agent.Domain, direction); err != nil {
			log.Printf("[api] self-send %s usage recording error: %v", direction, err)
		}
	}
}

func replyToList(replyTo string) []string {
	if replyTo == "" {
		return []string{}
	}
	return []string{replyTo}
}

func buildLoopbackReceivedEvent(agent *identity.AgentIdentity, msg *identity.Message, req outbound.SendRequest, raw []byte) webhookpub.Event {
	data := eventpayload.EmailReceivedData{
		MessageID:      msg.ID,
		AgentEmail:     agent.EmailAddress(),
		Direction:      "inbound",
		ConversationID: req.ConversationID,
		HeaderFrom:     stringPointer(agent.EmailAddress()),
		EnvelopeFrom:   nil,
		To:             []string{agent.EmailAddress()},
		CC:             []string{},
		ReplyTo:        replyToList(req.ReplyTo),
		Authentication: nil,
		DeliveredTo:    agent.EmailAddress(),
		Subject:        req.Subject,
		ReceivedAt:     msg.CreatedAt.UTC(),
		Attachments:    eventpayload.AttachmentMetadata(raw),
	}
	e := webhookpub.NewEvent(webhookpub.EventEmailReceived, agent.UserID, data)
	e.ID = webhookpub.DeterministicEventID(msg.ID, webhookpub.EventEmailReceived)
	e.AgentID = agent.ID
	e.ConversationID = req.ConversationID
	e.MessageID = msg.ID
	return e
}

func stringPointer(value string) *string { return &value }

func loopbackDisplayFrom(req outbound.SendRequest, agentEmail string) string {
	if req.ReplyTo != "" {
		if address, err := mail.ParseAddress(req.ReplyTo); err == nil {
			return address.Address
		}
		return req.ReplyTo
	}
	return agentEmail
}

func (a *API) pushLoopbackReceived(agentID string, event webhookpub.Event) {
	if a.wsHub == nil || event.ID == "" || !a.wsHub.IsConnected(agentID) {
		return
	}
	payload, err := json.Marshal(event.AsEnvelope())
	if err == nil {
		a.wsHub.Send(agentID, payload)
	}
}
