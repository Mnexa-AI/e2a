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
