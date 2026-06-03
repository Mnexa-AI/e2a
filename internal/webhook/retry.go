package webhook

import (
	"context"
	"log"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// retryBackoffs is the per-attempt delay schedule for failed
// deliveries. Total envelope ~72h spread over 8 attempts (slice 5
// extension from the original 5-attempt / ~4h schedule). Matches the
// industry-standard ~24-72h retry window — Svix defaults to ~24h
// across ~15 attempts; Stripe goes to 72h. We picked Stripe's window.
//
// Schedule lookup is gracefully bounded by len(retryBackoffs) — see
// nextRetryAt. In-flight rows with the legacy max_attempts=5 cap
// terminate after the 4h entry; new rows get max_attempts=8 from the
// updated migration 027 default.
var retryBackoffs = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	4 * time.Hour,
	8 * time.Hour,
	16 * time.Hour,
	24 * time.Hour,
}

func nextRetryAt(attempt int) (time.Time, bool) {
	if attempt >= len(retryBackoffs) {
		return time.Time{}, false
	}
	return time.Now().Add(retryBackoffs[attempt]), true
}

type RetryWorker struct {
	deliveryStore *DeliveryStore
	deliverer     *Deliverer
	identityStore *identity.Store
	interval      time.Duration
	batchSize     int
}

func NewRetryWorker(deliveryStore *DeliveryStore, deliverer *Deliverer, identityStore *identity.Store) *RetryWorker {
	return &RetryWorker{
		deliveryStore: deliveryStore,
		deliverer:     deliverer,
		identityStore: identityStore,
		interval:      30 * time.Second,
		batchSize:     20,
	}
}

func (w *RetryWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

func (w *RetryWorker) processBatch(ctx context.Context) {
	deliveries, err := w.deliveryStore.GetPendingDeliveries(ctx, w.batchSize)
	if err != nil {
		log.Printf("[retry] failed to fetch pending deliveries: %v", err)
		return
	}

	for _, d := range deliveries {
		w.processOne(ctx, d)
	}
}

func (w *RetryWorker) processOne(ctx context.Context, d Delivery) {
	agent, err := w.identityStore.GetAgentByID(ctx, d.AgentID)
	if err != nil {
		log.Printf("[retry] agent %s not found, marking failed: %v", d.AgentID, err)
		w.deliveryStore.MarkFailed(ctx, d.MessageID, "agent not found")
		return
	}

	// Read message content from messages table
	msg, err := w.identityStore.GetMessageWithContent(ctx, d.MessageID, d.AgentID)
	if err != nil {
		log.Printf("[retry] message %s not found for delivery %s, marking failed: %v", d.MessageID, d.MessageID, err)
		w.deliveryStore.MarkFailed(ctx, d.MessageID, "message not found")
		return
	}

	payload := Payload{
		MessageID:      msg.ID,
		ConversationID: msg.ConversationID,
		From:           msg.Sender,
		To:             msg.ToRecipients,
		CC:             msg.CC,
		Recipient:      msg.Recipient,
		RawMessage:     msg.RawMessage,
		AuthHeaders:    msg.AuthHeaders,
		ReceivedAt:     msg.CreatedAt,
	}

	err = w.deliverer.DeliverHTTP(ctx, agent, payload)
	if err == nil {
		w.deliveryStore.MarkDelivered(ctx, d.MessageID)
		log.Printf("[retry] delivery %s succeeded on attempt %d", d.MessageID, d.Attempts+1)
		return
	}

	nextAttempt := d.Attempts + 1
	if nextAttempt >= d.MaxAttempts {
		w.deliveryStore.MarkFailed(ctx, d.MessageID, err.Error())
		log.Printf("[retry] delivery %s failed permanently after %d attempts: %v", d.MessageID, nextAttempt, err)
		return
	}

	retryAt, ok := nextRetryAt(nextAttempt)
	if !ok {
		w.deliveryStore.MarkFailed(ctx, d.MessageID, err.Error())
		return
	}

	w.deliveryStore.MarkAttemptFailed(ctx, d.MessageID, err.Error(), retryAt)
	log.Printf("[retry] delivery %s attempt %d failed, next retry at %s: %v", d.MessageID, nextAttempt, retryAt.Format(time.RFC3339), err)
}
