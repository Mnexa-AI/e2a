package webhook

import (
	"context"
	"log"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// PersistentDeliverer wraps Deliverer with DB persistence for retry support.
type PersistentDeliverer struct {
	deliverer *Deliverer
	store     *DeliveryStore
}

func NewPersistentDeliverer(deliverer *Deliverer, store *DeliveryStore) *PersistentDeliverer {
	return &PersistentDeliverer{
		deliverer: deliverer,
		store:     store,
	}
}

// Deliver attempts delivery and persists the result. On failure, the delivery
// is queued for retry. Always returns nil — the message is safely persisted.
// Use this for SMTP inbound where the message is already accepted.
func (pd *PersistentDeliverer) Deliver(ctx context.Context, agent *identity.AgentIdentity, p Payload) error {
	err := pd.deliverer.DeliverHTTP(ctx, agent, p)

	if err == nil {
		// Delivered successfully — record as delivered
		d, createErr := pd.store.CreateDelivery(ctx, p.MessageID, "")
		if createErr != nil {
			log.Printf("[webhook] failed to record delivery: %v", createErr)
			return nil
		}
		pd.store.MarkDelivered(ctx, d.MessageID)
	} else {
		// Failed — persist for retry with the initial error
		_, createErr := pd.store.CreateDelivery(ctx, p.MessageID, err.Error())
		if createErr != nil {
			log.Printf("[webhook] failed to persist delivery for retry: %v", createErr)
			return nil
		}
		log.Printf("[webhook] delivery failed, queued for retry: %v", err)
	}

	return nil
}

// DeliverSync attempts delivery, persists the result, and returns the error.
// Use this for API-initiated sends where the caller needs immediate feedback.
func (pd *PersistentDeliverer) DeliverSync(ctx context.Context, agent *identity.AgentIdentity, p Payload) error {
	err := pd.deliverer.DeliverHTTP(ctx, agent, p)

	if err == nil {
		d, createErr := pd.store.CreateDelivery(ctx, p.MessageID, "")
		if createErr != nil {
			log.Printf("[webhook] failed to record delivery: %v", createErr)
			return nil
		}
		pd.store.MarkDelivered(ctx, d.MessageID)
	} else {
		_, createErr := pd.store.CreateDelivery(ctx, p.MessageID, err.Error())
		if createErr != nil {
			log.Printf("[webhook] failed to persist delivery for retry: %v", createErr)
		}
	}

	return err
}
