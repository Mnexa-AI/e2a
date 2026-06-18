package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// internal helpers

type eventRowForReplay struct {
	eventType         string
	messageID         *string
	envelope          []byte
	matchedWebhookIDs []string
}

func loadEventForReplay(ctx context.Context, pool *pgxpool.Pool, userID, eventID string) (*eventRowForReplay, error) {
	var (
		out       eventRowForReplay
		expiresAt time.Time
	)
	err := pool.QueryRow(ctx,
		`SELECT type, message_id, envelope, matched_webhook_ids, expires_at
		 FROM webhook_events
		 WHERE id = $1 AND user_id = $2 AND aud = 'webhook'`,
		eventID, userID,
	).Scan(&out.eventType, &out.messageID, &out.envelope, &out.matchedWebhookIDs, &expiresAt)
	if err != nil {
		if isNoRows(err) {
			return nil, errEventNotFound
		}
		return nil, err
	}
	if time.Now().After(expiresAt) {
		return nil, errEventExpired
	}
	return &out, nil
}

// insertReplayDelivery writes a webhook_subscriber_deliveries row with
// replay_id set (so the partial unique index doesn't conflict with the
// original first-delivery row). Returns the generated delivery id.
func insertReplayDelivery(ctx context.Context, pool *pgxpool.Pool, eventID, webhookID, eventType string, messageID *string, envelope []byte) (string, error) {
	deliveryID := "whd_" + replayHex16()
	_, err := pool.Exec(ctx,
		`INSERT INTO webhook_subscriber_deliveries
		    (id, webhook_id, event_id, event_type, event_payload, message_id, replay_id, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $1, 'pending')`,
		deliveryID, webhookID, eventID, eventType, envelope, messageID,
	)
	if err != nil {
		return "", err
	}
	return deliveryID, nil
}

func replayHex16() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("agent/replay: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}
