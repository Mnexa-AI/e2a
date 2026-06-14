package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Mnexa-AI/e2a/internal/ratelimit"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
)

// redeliverSinceLimiter is a package-level token bucket: 1/min per
// (user_id, webhook_id). Per design §S9: in-memory per-process →
// effective limit scales with replica count; acceptable for v1, will
// move to a Postgres-backed counter when we cross the threshold.
//
// Initialized lazily so test code that doesn't exercise this endpoint
// doesn't pay the cleanup-goroutine cost.
var redeliverSinceLimiter = ratelimit.New(time.Minute, 1)

// Slice 7: customer-driven replay endpoints.
//
//   POST /api/v1/events/{id}/redeliver         — replay one event to a webhook
//   POST /api/v1/webhooks/{id}/redeliver-since  — replay every event for a webhook since a timestamp
//
// Per design §4.6 / D2:
//   - Per-webhook only (never re-routed against the current
//     subscriber set; we use the matched_webhook_ids snapshot from
//     fan-out time).
//   - Reuses the original event id; consumer dedup on event id will
//     discard the replay if they've already processed it (D6).
//   - Replay rows set replay_id = the new delivery id, so the
//     partial unique index on (event_id, webhook_id) WHERE
//     replay_id IS NULL doesn't block them.

const (
	redeliverSinceMaxDays = 7
)


// handleRedeliverSince serves POST /webhooks/{id}/redeliver-since.
// Body: {"since": "RFC3339"}.
// Capped at 7 days back per §4.6.
// @Summary      Bulk-replay events for a webhook
// @Description  Re-fires every event the webhook originally matched since `since` (RFC3339). Window capped at 7 days. Skips events that already have a pending delivery for this webhook.
// @Tags         Webhooks
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Webhook ID"
// @Param        body body RedeliverSinceRequest true "Replay window"
// @Success      200 {object} RedeliverSinceResponse
// @Failure      400 {string} string "since out of window or malformed"
// @Router       /api/v1/webhooks/{id}/redeliver-since [post]
func (a *API) handleRedeliverSince(w http.ResponseWriter, r *http.Request) {
	if a.eventsPool == nil {
		http.Error(w, "events API not configured", http.StatusNotFound)
		return
	}
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	webhookID := mux.Vars(r)["id"]
	if webhookID == "" {
		http.Error(w, "missing webhook id", http.StatusBadRequest)
		return
	}
	var body struct {
		Since string `json:"since"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	since, err := time.Parse(time.RFC3339, body.Since)
	if err != nil {
		http.Error(w, "since must be RFC3339", http.StatusBadRequest)
		return
	}
	if time.Since(since) > redeliverSinceMaxDays*24*time.Hour {
		http.Error(w, fmt.Sprintf("since must be within %d days", redeliverSinceMaxDays), http.StatusBadRequest)
		return
	}

	a.emit().RedeliverRequests("since")

	// Per-(user, webhook) rate limit: 1/min. Caveat per design §S9:
	// in-memory per-process, so under N replicas the effective limit
	// is N/min. Documented in the customer-facing rate-limit notes.
	rlKey := user.ID + "|" + webhookID
	if ok, retryAfter := redeliverSinceLimiter.AllowWithRetryAfter(rlKey); !ok {
		secs := int(retryAfter.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", secs))
		http.Error(w, "rate limit exceeded (1/min per webhook)", http.StatusTooManyRequests)
		return
	}

	// Find every event the webhook originally matched in the window.
	rows, err := a.eventsPool.Query(r.Context(),
		`SELECT id, type, message_id, envelope FROM webhook_events
		 WHERE user_id = $1
		   AND created_at >= $2
		   AND $3 = ANY(matched_webhook_ids)
		 ORDER BY created_at ASC`,
		user.ID, since, webhookID)
	if err != nil {
		log.Printf("[replay] redeliver-since query: %v", err)
		http.Error(w, "failed to query events", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	scheduled, skipped := 0, 0
	for rows.Next() {
		var (
			eventID    string
			eventType  string
			messageID  *string
			envelope   []byte
		)
		if err := rows.Scan(&eventID, &eventType, &messageID, &envelope); err != nil {
			log.Printf("[replay] redeliver-since scan: %v", err)
			continue
		}
		// Skip events that already have a pending or in-flight
		// delivery for this webhook.
		var pendingCount int
		if err := a.eventsPool.QueryRow(r.Context(),
			`SELECT count(*) FROM webhook_subscriber_deliveries
			 WHERE event_id = $1 AND webhook_id = $2 AND status = 'pending'`,
			eventID, webhookID,
		).Scan(&pendingCount); err == nil && pendingCount > 0 {
			skipped++
			continue
		}
		if _, err := insertReplayDelivery(r.Context(), a.eventsPool, eventID, webhookID, eventType, messageID, envelope); err != nil {
			log.Printf("[replay] redeliver-since insert webhook=%s event=%s: %v", webhookID, eventID, err)
			continue
		}
		scheduled++
	}

	writeJSON(w, map[string]interface{}{
		"webhook_id":              webhookID,
		"since":                   since.Format(time.RFC3339),
		"scheduled":               scheduled,
		"skipped_already_pending": skipped,
	})
}

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

