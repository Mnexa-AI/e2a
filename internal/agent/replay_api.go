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

	"github.com/Mnexa-AI/e2a/internal/idempotency"
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

// redeliverRequest is the body of POST /events/{id}/redeliver.
// webhook_id omitted = fan to every webhook in matched_webhook_ids.
type redeliverRequest struct {
	WebhookID string `json:"webhook_id,omitempty"`
}

type redeliverResponse struct {
	DeliveryID string `json:"delivery_id,omitempty"`
	EventID    string `json:"event_id"`
	WebhookID  string `json:"webhook_id,omitempty"`
	Status     string `json:"status"`
	// Multi-webhook fan-out (empty body case).
	Deliveries []redeliverDelivery `json:"deliveries,omitempty"`
}

type redeliverDelivery struct {
	WebhookID  string `json:"webhook_id"`
	DeliveryID string `json:"delivery_id,omitempty"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

// @Summary      Replay a webhook event
// @Description  Replay an event to one webhook (body `{webhook_id}`) or to every originally-matched webhook (empty body). Reuses the original event id so consumer dedup discards the replay if already processed.
// @Tags         Events
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Event ID"
// @Param        body body RedeliverRequest false "{webhook_id} for targeted replay; empty for fan-out"
// @Success      200 {object} RedeliverResponse
// @Failure      404 {string} string "Event not found"
// @Failure      409 {string} string "Webhook not in originally-matched set"
// @Failure      410 {string} string "Event past retention"
// @Router       /api/v1/events/{id}/redeliver [post]
func (a *API) handleRedeliverEvent(w http.ResponseWriter, r *http.Request) {
	if a.eventsPool == nil {
		http.Error(w, "events API not configured", http.StatusNotFound)
		return
	}
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	eventID := mux.Vars(r)["id"]
	if eventID == "" {
		http.Error(w, "missing event id", http.StatusBadRequest)
		return
	}

	var req redeliverRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}

	scope := "single"
	if req.WebhookID == "" {
		scope = "fanout"
	}
	a.emit().RedeliverRequests(scope)

	// 5-minute idempotency window per design §5.4. Synthetic key
	// derived from (user, event_id, webhook_id_or_empty, "replay") so
	// the same request body within the window returns the cached
	// response. Uses the existing idempotency store the API was wired
	// with at SetIdempotencyStore time; when nil (tests that didn't
	// wire it) the idempotency check is skipped.
	var idemKey string
	if a.idempotency != nil {
		idemKey = "replay:" + eventID + ":" + req.WebhookID
		claim, err := a.idempotency.Claim(r.Context(), user.ID, idemKey, "/api/v1/events/redeliver", "")
		if err != nil {
			log.Printf("[replay] idempotency claim err: %v", err)
			http.Error(w, "idempotency store error", http.StatusInternalServerError)
			return
		}
		switch claim.Outcome {
		case idempotency.OutcomeReplay:
			w.Header().Set("Content-Type", claim.Cached.ContentType)
			w.WriteHeader(claim.Cached.StatusCode)
			w.Write(claim.Cached.Body)
			return
		case idempotency.OutcomeInFlight:
			http.Error(w, "replay in progress", http.StatusConflict)
			return
		case idempotency.OutcomeMismatch:
			// Shouldn't happen — body hash is empty so any second
			// call with the same key claims fine. Defensive.
			http.Error(w, "idempotency body mismatch", http.StatusUnprocessableEntity)
			return
		}
		// Acquired: release on early-return paths below; complete on
		// the success path.
		defer func() {
			if idemKey != "" {
				_ = a.idempotency.Release(r.Context(), user.ID, idemKey)
			}
		}()
	}

	// Load the event + matched snapshot.
	row, err := loadEventForReplay(r.Context(), a.eventsPool, user.ID, eventID)
	if err != nil {
		switch err {
		case errEventNotFound:
			http.Error(w, "event not found", http.StatusNotFound)
		case errEventExpired:
			http.Error(w, "event expired (past 30-day retention)", http.StatusGone)
		default:
			log.Printf("[replay] loadEvent: %v", err)
			http.Error(w, "failed to load event", http.StatusInternalServerError)
		}
		return
	}

	// Targeted replay (webhook_id specified).
	if req.WebhookID != "" {
		if !containsString(row.matchedWebhookIDs, req.WebhookID) {
			http.Error(w, "webhook was not among the originally-matched subscribers", http.StatusConflict)
			return
		}
		dl, err := insertReplayDelivery(r.Context(), a.eventsPool, eventID, req.WebhookID, row.eventType, row.messageID, row.envelope)
		if err != nil {
			log.Printf("[replay] insertReplayDelivery: %v", err)
			http.Error(w, "failed to schedule replay", http.StatusInternalServerError)
			return
		}
		respBody := redeliverResponse{
			DeliveryID: dl, EventID: eventID, WebhookID: req.WebhookID, Status: "pending",
		}
		completeIdempotency(r.Context(), a, user.ID, idemKey, 200, respBody)
		idemKey = "" // signal deferred Release to skip
		writeJSON(w, respBody)
		return
	}

	// Bulk replay (empty body) — fan to every originally-matched webhook.
	deliveries := make([]redeliverDelivery, 0, len(row.matchedWebhookIDs))
	for _, whID := range row.matchedWebhookIDs {
		dl, err := insertReplayDelivery(r.Context(), a.eventsPool, eventID, whID, row.eventType, row.messageID, row.envelope)
		if err != nil {
			log.Printf("[replay] insertReplayDelivery webhook=%s: %v", whID, err)
			deliveries = append(deliveries, redeliverDelivery{
				WebhookID: whID, Status: "skipped", Reason: "failed to schedule",
			})
			continue
		}
		deliveries = append(deliveries, redeliverDelivery{
			WebhookID: whID, DeliveryID: dl, Status: "pending",
		})
	}
	respBody := redeliverResponse{EventID: eventID, Status: "scheduled", Deliveries: deliveries}
	completeIdempotency(r.Context(), a, user.ID, idemKey, 200, respBody)
	idemKey = "" // signal deferred Release to skip
	writeJSON(w, respBody)
}

// completeIdempotency caches the response so retries within the 5-min
// window get OutcomeReplay. No-op when idemKey is empty (caller didn't
// claim) or the store is nil.
func completeIdempotency(ctx context.Context, a *API, userID, idemKey string, status int, body interface{}) {
	if a.idempotency == nil || idemKey == "" {
		return
	}
	cached, err := json.Marshal(body)
	if err != nil {
		log.Printf("[replay] marshal cached body: %v", err)
		return
	}
	if err := a.idempotency.Complete(ctx, userID, idemKey, idempotency.CachedResponse{
		StatusCode:  status,
		ContentType: "application/json",
		Body:        cached,
	}); err != nil {
		log.Printf("[replay] idempotency complete err: %v", err)
	}
}

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

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
