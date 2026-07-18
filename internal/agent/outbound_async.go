package agent

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tokencanopy/e2a/internal/delivery"
	"github.com/tokencanopy/e2a/internal/eventpayload"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/outbound"
	"github.com/tokencanopy/e2a/internal/outboundsend"
	"github.com/tokencanopy/e2a/internal/sendramp"
	"github.com/tokencanopy/e2a/internal/usage"
	"github.com/tokencanopy/e2a/internal/webhookpub"
)

// --- Async outbound send adapters (async-message-pipeline.md, slice C) ---
//
// These bridge internal/outboundsend's Store/Deliverer interfaces onto the
// concrete identity.Store + webhookpub.Outbox + outbound.Sender. They live in the
// agent package (not outboundsend) because agent already owns the store, outbox,
// usage tracker, and sender — and agent may import outboundsend, never the reverse.

// AcceptIdemCompleter completes the idempotency key inside the delivery tx with
// the exact result the wire will render. External sends cache 202/accepted;
// terminal local loopback caches 200/sent. nil when the request carries no
// Idempotency-Key (or no idempotency store is wired).
type AcceptIdemCompleter func(ctx context.Context, tx pgx.Tx, result *OutboundResult) error

// ApproveIdemCompleter completes an approval's idempotency key inside the
// async approve-and-enqueue transaction. It receives the resolved message so
// the httpapi layer can cache the exact SendResultView (including edited,
// method, and sent_as) that the live request returns.
type ApproveIdemCompleter func(ctx context.Context, tx pgx.Tx, approved *identity.Message) error

// OutboundEnqueuer is the accept-tx's handle on the shared River client — it
// inserts the outbound_send job in the same transaction as the message row.
// *outboundsend.Jobs satisfies it. Main always injects it; a nil value fails
// closed before provider I/O.
type OutboundEnqueuer interface {
	EnqueueSendTx(ctx context.Context, tx pgx.Tx, messageID string) (int64, error)
}

// outboundSendStore implements outboundsend.Store over identity.Store +
// webhookpub.Outbox + the usage tracker. ClaimSend persists the short-lived
// sending state before provider I/O; MarkSent/MarkFailed then record one monotonic
// terminal outcome in fresh transactions. Successful sends are metered post-commit
// (the message only becomes billable once submitted).
type outboundSendStore struct {
	store  *identity.Store
	outbox webhookpub.Outbox
	usage  usage.UsageTracker
}

// NewOutboundSendStore builds the outboundsend.Store adapter for main.go.
func NewOutboundSendStore(store *identity.Store, outbox webhookpub.Outbox, usageTracker usage.UsageTracker) outboundsend.Store {
	return &outboundSendStore{store: store, outbox: outbox, usage: usageTracker}
}

type outboundRampGate struct {
	store    *sendramp.Store
	schedule sendramp.Schedule
}

// NewOutboundRampGate adapts the durable sendramp store to the worker-owned
// gate contract. The schedule is snapshotted by Store on the first eligible
// send; config changes therefore affect only domains that have not armed yet.
func NewOutboundRampGate(store *sendramp.Store, schedule sendramp.Schedule) outboundsend.RampGate {
	return &outboundRampGate{store: store, schedule: schedule}
}

func (g *outboundRampGate) Reserve(ctx context.Context, req outboundsend.RampRequest) (outboundsend.RampDecision, error) {
	d, err := g.store.Reserve(ctx, sendramp.ReserveRequest{
		MessageID: req.MessageID,
		UserID:    req.UserID,
		Domain:    req.Domain,
		Units:     req.Units,
		Day:       time.Now().UTC(),
		Schedule:  g.schedule,
	})
	return outboundsend.RampDecision{Allowed: d.Allowed, RetryAt: d.RetryAt}, err
}

func (a *outboundSendStore) ClaimSend(ctx context.Context, messageID string, jobID int64) (*outboundsend.SendJob, error) {
	p, err := a.store.ClaimOutboundForSend(ctx, messageID, jobID)
	if err != nil || p == nil {
		return nil, err
	}
	return &outboundsend.SendJob{
		MessageID:         p.ID,
		UserID:            p.UserID,
		Domain:            p.Domain,
		MessageType:       p.MessageType,
		Status:            p.DeliveryStatus,
		EnvelopeFrom:      p.EnvelopeFrom,
		Recipients:        p.Recipients,
		RawMessage:        p.Raw,
		SentAs:            p.SentAs,
		AcceptedAt:        p.CreatedAt,
		ProviderAccepted:  p.ProviderAccepted,
		ProviderMessageID: p.ProviderMessageID,
	}, nil
}

// SuppressedRecipients backs the SendWorker's pre-provider suppression guard:
// the subset of recipients on the owning account's suppression list (the
// store normalizes both sides).
func (a *outboundSendStore) SuppressedRecipients(ctx context.Context, userID string, recipients []string) ([]string, error) {
	return a.store.SuppressedAddresses(ctx, userID, recipients)
}

func (a *outboundSendStore) ReleaseSend(ctx context.Context, messageID string, jobID int64) error {
	return a.store.ReleaseOutboundSendClaim(ctx, messageID, jobID)
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
			return nil
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
	a.meterSent(ctx, info, messageID)
	return nil
}

// meterSent meters a durably-sent message after its commit (side-effect only —
// never block on quota; the accept-time cap pre-check is the gate). Mirrors the
// synchronous path, which meters only once the message row exists. KNOWN
// best-effort window: a crash between the 'sent' commit and this call drops the
// meter (the re-drive no-ops on 'sent'), so a durably-sent message can go
// unmetered. Rare + customer-favoring; fold into the MarkSent tx if billing
// accuracy ever demands it.
func (a *outboundSendStore) meterSent(ctx context.Context, info *identity.OutboundSentInfo, messageID string) {
	if a.usage == nil {
		return
	}
	if _, err := a.usage.RecordAndCheck(ctx, info.UserID, info.Message.AgentID, info.Domain, "outbound"); err != nil {
		log.Printf("[outbound-send] usage recording error for %s: %v", messageID, err)
	}
}

// MarkFailed is the guarded terminal write (async-send-contract §3.1): inside
// one transaction it first checks for provider-accept evidence — an SNS
// notification that proved the provider accepted this message's submission —
// and, when present, settles the row as SENT (+ email.sent, + metering) instead
// of declaring a false terminal failure. Only an evidence-free row is marked
// failed (+ email.failed) with the caller's failure provenance. Both events use
// deterministic ids, so duplicate finalizations stay idempotent, and
// MarkOutboundFailedTx's own `provider_accepted_at IS NULL` CAS closes the race
// where evidence lands between the two statements (the row is then left for the
// reconciler's next pass to settle as sent).
func (a *outboundSendStore) MarkFailed(ctx context.Context, messageID string, attempt int, detail string, source delivery.FailureSource) error {
	var resolved *identity.OutboundSentInfo
	var resolvedProviderID string
	if err := a.store.WithTx(ctx, func(tx pgx.Tx) error {
		info, providerID, err := a.store.ResolveOutboundProviderAcceptedTx(ctx, tx, messageID)
		if err != nil {
			return err
		}
		if info != nil {
			resolved, resolvedProviderID = info, providerID
			e := buildEmailSentEventFromRow(info, providerID)
			e.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailSent)
			a.outbox.PublishBestEffortTx(ctx, tx, e)
			return nil
		}
		finfo, err := a.store.MarkOutboundFailedTx(ctx, tx, messageID, detail, source)
		if err != nil {
			return err
		}
		if finfo == nil {
			return nil // row gone, already terminal, or evidence raced in — nothing to record
		}
		// Emit with the detail actually stored on the row (a deferred final
		// attempt's diagnostic is preferred over a generic sweep detail).
		e := buildEmailFailedEventFromRow(finfo, finfo.Message.DeliveryDetail)
		e.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailFailed)
		a.outbox.PublishBestEffortTx(ctx, tx, e)
		return nil
	}); err != nil {
		return err
	}
	if resolved != nil {
		log.Printf("[outbound-send] %s: terminal-failure guard settled as sent on provider evidence (provider id %q)", messageID, resolvedProviderID)
		a.meterSent(ctx, resolved, messageID)
	}
	return nil
}

func (a *outboundSendStore) DeferTerminalFailure(ctx context.Context, messageID string, jobID int64, detail string) error {
	return a.store.DeferOutboundTerminalFailure(ctx, messageID, jobID, detail)
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
	providerID, err := d.sender.SubmitOnce(j.MessageID, j.EnvelopeFrom, j.Recipients, j.RawMessage)
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
