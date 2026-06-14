package agent

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Exported seams over the events query layer so the v1 httpapi layer can
// reuse the exact webhook-event SQL + wire shapes instead of re-implementing
// them. These move home (or into a shared events package) at the 1Z cutover.

// EventJSON is the exported event wire shape (alias of the internal type, so
// the JSON contract is identical).
type EventJSON = eventJSON

// DeliveryStatusJSON is the exported per-event delivery roll-up.
type DeliveryStatusJSON = deliveryStatusJSON

// Exported sentinels for the events lookup.
var (
	ErrEventNotFound = errEventNotFound
	ErrEventExpired  = errEventExpired
)

// ListEventsForUser wraps the internal listEvents query.
func ListEventsForUser(ctx context.Context, pool *pgxpool.Pool, userID, eventType, agentID, conversationID, messageID string, since, until *time.Time, cursorCreatedAt time.Time, cursorID string, limit int) ([]EventJSON, error) {
	return listEvents(ctx, pool, userID, eventType, agentID, conversationID, messageID, since, until, cursorCreatedAt, cursorID, limit)
}

// GetEventForUser wraps the internal getEvent query.
func GetEventForUser(ctx context.Context, pool *pgxpool.Pool, userID, eventID string) (*EventJSON, error) {
	return getEvent(ctx, pool, userID, eventID)
}

// ReplayEvent is the exported snapshot used to schedule a redelivery.
type ReplayEvent struct {
	EventType         string
	MessageID         *string
	Envelope          []byte
	MatchedWebhookIDs []string
}

// LoadReplayEvent wraps loadEventForReplay (ownership-scoped). Returns
// ErrEventNotFound / ErrEventExpired for the respective cases.
func LoadReplayEvent(ctx context.Context, pool *pgxpool.Pool, userID, eventID string) (*ReplayEvent, error) {
	row, err := loadEventForReplay(ctx, pool, userID, eventID)
	if err != nil {
		return nil, err
	}
	return &ReplayEvent{
		EventType:         row.eventType,
		MessageID:         row.messageID,
		Envelope:          row.envelope,
		MatchedWebhookIDs: row.matchedWebhookIDs,
	}, nil
}

// InsertReplayDelivery wraps insertReplayDelivery, scheduling one redelivery.
func InsertReplayDelivery(ctx context.Context, pool *pgxpool.Pool, eventID, webhookID, eventType string, messageID *string, envelope []byte) (string, error) {
	return insertReplayDelivery(ctx, pool, eventID, webhookID, eventType, messageID, envelope)
}
