package agent

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Mnexa-AI/e2a/internal/eventpayload"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/outboundsend"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// --- Async outbound send adapters (async-message-pipeline.md, slice C) ---
//
// These bridge internal/outboundsend's Store/Deliverer interfaces onto the
// concrete identity.Store + webhookpub.Outbox + outbound.Sender. They live in the
// agent package (not outboundsend) because agent already owns the store, outbox,
// usage tracker, and sender — and agent may import outboundsend, never the reverse.

// AcceptIdemCompleter completes the idempotency key inside the accept-tx, given
// the freshly minted message id. The httpapi layer supplies it (it owns the key +
// the wire response shape); DeliverOutbound invokes it inside the accept-tx so the
// key commits atomically with the message + send job. nil when the request carries
// no Idempotency-Key (or no idempotency store is wired).
type AcceptIdemCompleter func(ctx context.Context, tx pgx.Tx, messageID string) error

// ApproveIdemCompleter completes an approval's idempotency key inside the
// async approve-and-enqueue transaction. It receives the resolved message so
// the httpapi layer can cache the exact SendResultView (including edited,
// method, and sent_as) that the live request returns.
type ApproveIdemCompleter func(ctx context.Context, tx pgx.Tx, approved *identity.Message) error

// OutboundEnqueuer is the accept-tx's handle on the shared River client — it
// inserts the outbound_send job in the same transaction as the message row.
// *outboundsend.Jobs satisfies it. Injected via SetOutboundEnqueuer; nil keeps
// DeliverOutbound on the synchronous path.
type OutboundEnqueuer interface {
	EnqueueSendTx(ctx context.Context, tx pgx.Tx, messageID string) (int64, error)
}

// outboundSendStore implements outboundsend.Store over identity.Store +
// webhookpub.Outbox + the usage tracker: LoadForSend reads the accepted row;
// MarkSent/MarkFailed each run one transaction that flips delivery_status and
// emits email.sent / email.failed via the outbox. MarkSent also meters usage
// post-commit (the message only becomes a billable send once submitted).
type outboundSendStore struct {
	store  *identity.Store
	outbox webhookpub.Outbox
	usage  usage.UsageTracker
}

// NewOutboundSendStore builds the outboundsend.Store adapter for main.go.
func NewOutboundSendStore(store *identity.Store, outbox webhookpub.Outbox, usageTracker usage.UsageTracker) outboundsend.Store {
	return &outboundSendStore{store: store, outbox: outbox, usage: usageTracker}
}

func (a *outboundSendStore) LoadForSend(ctx context.Context, messageID string) (*outboundsend.SendJob, error) {
	p, err := a.store.LoadOutboundForSend(ctx, messageID)
	if err != nil || p == nil {
		return nil, err
	}
	return &outboundsend.SendJob{
		MessageID:    p.ID,
		Status:       p.DeliveryStatus,
		EnvelopeFrom: p.EnvelopeFrom,
		Recipients:   p.Recipients,
		RawMessage:   p.Raw,
		SentAs:       p.SentAs,
		AcceptedAt:   p.CreatedAt,
	}, nil
}

func (a *outboundSendStore) MarkSent(ctx context.Context, messageID, providerMessageID, sentAs string) error {
	var info *identity.OutboundSentInfo
	if err := a.store.WithTx(ctx, func(tx pgx.Tx) error {
		i, err := a.store.MarkOutboundSentTx(ctx, tx, messageID, providerMessageID)
		if err != nil {
			return err
		}
		info = i
		if info == nil {
			return nil // row gone between load and mark — nothing to record
		}
		// email.sent uses the same deterministic id + best-effort semantics as the
		// synchronous publishSent: the SES submit already happened, so a failed
		// outbox write must NOT roll back the 'sent' write (that would re-drive the
		// job and double-send). Deterministic id dedupes any re-drive via ON CONFLICT.
		e := buildEmailSentEventFromRow(info, providerMessageID)
		e.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailSent)
		a.outbox.PublishBestEffortTx(ctx, tx, e)
		return nil
	}); err != nil {
		return err
	}
	if info == nil {
		return nil
	}
	// Meter after the send is durable (side-effect only — never block on quota;
	// the accept-time cap pre-check is the gate). Mirrors the synchronous path,
	// which meters only once the message row exists. KNOWN best-effort window: a
	// crash between the 'sent' commit above and this call drops the meter (the
	// re-drive no-ops on 'sent'), so a durably-sent message can go unmetered. Rare
	// + customer-favoring; fold into the MarkSent tx if billing accuracy ever
	// demands it (post-GA, with the terminal-failure guard work).
	if a.usage != nil {
		if _, err := a.usage.RecordAndCheck(ctx, info.UserID, info.Message.AgentID, info.Domain, "outbound"); err != nil {
			log.Printf("[outbound-send] usage recording error for %s: %v", messageID, err)
		}
	}
	return nil
}

func (a *outboundSendStore) MarkFailed(ctx context.Context, messageID string, attempt int, detail string) error {
	return a.store.WithTx(ctx, func(tx pgx.Tx) error {
		info, err := a.store.MarkOutboundFailedTx(ctx, tx, messageID, detail)
		if err != nil {
			return err
		}
		if info == nil {
			return nil // row gone — nothing to record
		}
		e := buildEmailFailedEventFromRow(info, detail)
		e.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailFailed)
		a.outbox.PublishBestEffortTx(ctx, tx, e)
		return nil
	})
}

// buildEmailSentEventFromRow reconstructs the email.sent event from a stored row
// (the async worker has no live SendRequest). Emits the SAME canonical
// eventpayload.EmailSentData struct as the synchronous buildSentEvent, so
// subscribers see an identical envelope on both paths (golden-fixture-locked).
func buildEmailSentEventFromRow(info *identity.OutboundSentInfo, providerMessageID string) webhookpub.Event {
	m := info.Message
	data := eventpayload.EmailSentData{
		MessageID:         m.ID,
		AgentEmail:        m.Sender,
		Direction:         "outbound",
		ConversationID:    m.ConversationID,
		ProviderMessageID: providerMessageID,
		Method:            m.Method,
		From:              m.Sender,
		To:                orEmpty(m.ToRecipients),
		CC:                m.CC,
		BCC:               m.BCC,
		Subject:           m.Subject,
		MessageType:       m.Type,
	}
	return webhookpub.Event{
		Type:           webhookpub.EventEmailSent,
		CreatedAt:      time.Now().UTC(),
		UserID:         info.UserID,
		AgentID:        m.AgentID,
		ConversationID: m.ConversationID,
		MessageID:      m.ID,
		Data:           data,
	}
}

// buildEmailFailedEventFromRow builds the email.failed event for a terminal
// outbound send failure.
//
// ReasonCode and Retryable stay unset: MarkFailed only receives the diagnostic
// string — the retry classification (permanent vs exhausted) is consumed inside
// the send worker and not plumbed here. The schema keeps both fields optional;
// populate them if/when the worker passes its classification through.
func buildEmailFailedEventFromRow(info *identity.OutboundSentInfo, detail string) webhookpub.Event {
	m := info.Message
	data := eventpayload.EmailFailedData{
		MessageID:      m.ID,
		AgentEmail:     m.Sender,
		Direction:      "outbound",
		ConversationID: m.ConversationID,
		Method:         m.Method,
		From:           m.Sender,
		To:             orEmpty(m.ToRecipients),
		CC:             m.CC,
		BCC:            m.BCC,
		Subject:        m.Subject,
		MessageType:    m.Type,
		Reason:         detail,
	}
	return webhookpub.Event{
		Type:           webhookpub.EventEmailFailed,
		CreatedAt:      time.Now().UTC(),
		UserID:         info.UserID,
		AgentID:        m.AgentID,
		ConversationID: m.ConversationID,
		MessageID:      m.ID,
		Data:           data,
	}
}

// outboundDeliverer implements outboundsend.Deliverer over Sender.SubmitOnce — a
// single SMTP submit of the persisted Sent-folder bytes (River owns retries).
type outboundDeliverer struct {
	sender *outbound.Sender
}

// NewOutboundDeliverer builds the outboundsend.Deliverer adapter for main.go.
func NewOutboundDeliverer(sender *outbound.Sender) outboundsend.Deliverer {
	return &outboundDeliverer{sender: sender}
}

func (d *outboundDeliverer) Deliver(ctx context.Context, j *outboundsend.SendJob) outboundsend.DeliverOutcome {
	providerID, err := d.sender.SubmitOnce(j.EnvelopeFrom, j.Recipients, j.RawMessage)
	if err != nil {
		// Classify (design §8): a definitely-permanent 5xx is terminal (JobCancel);
		// a provider-connection failure (relay unreachable/misconfigured) is an
		// outage → snooze without burning an attempt; everything else (4xx/unknown)
		// takes the bounded retry. Terminal-failing a send that could still succeed
		// would violate at-least-once.
		return outboundsend.DeliverOutcome{
			Err:       err,
			Permanent: outbound.IsPermanentSMTPError(err),
			Outage:    outbound.IsConnectionError(err),
		}
	}
	return outboundsend.DeliverOutcome{ProviderMessageID: providerID, SentAs: j.SentAs}
}
