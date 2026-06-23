package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Slice 6: customer-facing event log API.
//
//   GET /v1/events                — list with cursor pagination
//   GET /v1/events/{id}           — single event
//   POST /v1/events/{id}/redeliver — slice 7 replay (lives here too)
//
// All scoped by the caller's user_id (Bearer auth). The handlers
// expose the slice-1 webhook_events table as a queryable resource —
// what Stripe calls the "Events API." Customers use it for
// reconciliation and replay.

// SetPoolForEvents wires the raw pgxpool.Pool used by the events
// handlers. Kept separate from the store so a future refactor can
// route through a higher-level abstraction without changing the
// handler signatures.
func (a *API) SetPoolForEvents(p *pgxpool.Pool) { a.eventsPool = p }

// eventJSON is the wire shape returned by GET /events and
// GET /events/{id}. Mirrors design §4.6.
type eventJSON struct {
	ID             string                 `json:"id"`
	Type           string                 `json:"type" enum:"email.received,email.sent,email.review_approved,email.review_rejected,domain.sending_verified,domain.sending_failed,email.delivered,email.bounced,email.complained,domain.suppression_added,email.flagged,email.blocked,email.pending_review"`
	SchemaVersion  int                    `json:"schema_version"`
	CreatedAt      time.Time              `json:"created_at"`
	AgentID        *string                `json:"agent_id,omitempty"`
	ConversationID *string                `json:"conversation_id,omitempty"`
	MessageID      *string                `json:"message_id,omitempty"`
	Status         string                 `json:"status" enum:"pending,processed,no_match"`
	Data           map[string]interface{} `json:"data"`
	DeliveryStatus *deliveryStatusJSON    `json:"delivery_status,omitempty"`
}

type deliveryStatusJSON struct {
	MatchedWebhooks int `json:"matched_webhooks"`
	Delivered       int `json:"delivered"`
	Pending         int `json:"pending"`
	Failed          int `json:"failed"`
}

func listEvents(ctx context.Context, pool *pgxpool.Pool, userID, eventType, agentID, conversationID, messageID string, since, until *time.Time, cursorCreatedAt time.Time, cursorID string, limit int) ([]eventJSON, error) {
	// Build query. user_id is always the first predicate.
	args := []any{userID}
	whereClauses := []string{"user_id = $1", "aud = 'webhook'"}
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if eventType != "" {
		whereClauses = append(whereClauses, "type = "+addArg(eventType))
	}
	if agentID != "" {
		whereClauses = append(whereClauses, "agent_id = "+addArg(agentID))
	}
	if conversationID != "" {
		whereClauses = append(whereClauses, "conversation_id = "+addArg(conversationID))
	}
	if messageID != "" {
		whereClauses = append(whereClauses, "message_id = "+addArg(messageID))
	}
	if since != nil {
		whereClauses = append(whereClauses, "created_at >= "+addArg(*since))
	}
	if until != nil {
		whereClauses = append(whereClauses, "created_at < "+addArg(*until))
	}
	if !cursorCreatedAt.IsZero() {
		// (created_at, id) < (cursor_created_at, cursor_id) — DESC walking
		p1 := addArg(cursorCreatedAt)
		p2 := addArg(cursorID)
		whereClauses = append(whereClauses, fmt.Sprintf("(created_at, id) < (%s, %s)", p1, p2))
	}

	where := ""
	for i, c := range whereClauses {
		if i == 0 {
			where = "WHERE " + c
		} else {
			where += " AND " + c
		}
	}
	limitArg := addArg(limit)
	sql := `SELECT id, type, schema_version, created_at, agent_id, conversation_id, message_id, status, envelope, expires_at
	        FROM webhook_events ` + where + `
	        ORDER BY created_at DESC, id DESC
	        LIMIT ` + limitArg

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []eventJSON
	for rows.Next() {
		var (
			ej          eventJSON
			schemaVer   int16
			envelopeRaw []byte
			expiresAt   time.Time
		)
		if err := rows.Scan(&ej.ID, &ej.Type, &schemaVer, &ej.CreatedAt,
			&ej.AgentID, &ej.ConversationID, &ej.MessageID, &ej.Status,
			&envelopeRaw, &expiresAt,
		); err != nil {
			return nil, err
		}
		ej.SchemaVersion = int(schemaVer)

		// Extract envelope.data so the response surfaces just the
		// customer-meaningful payload (not the full envelope wrapper).
		var env struct {
			Data map[string]interface{} `json:"data"`
		}
		// Tolerate older/malformed envelopes AND a valid envelope whose `data`
		// is null/absent — `data` is a required object on the wire, so it must
		// never serialize as JSON null (B3).
		if err := json.Unmarshal(envelopeRaw, &env); err != nil || env.Data == nil {
			env.Data = map[string]interface{}{}
		}
		ej.Data = env.Data
		out = append(out, ej)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// Surface delivery_status on the list too, matching getEvent — otherwise the
	// same event has a roll-up by id but not in the list (B4a). Bounded by the
	// page limit; a single GROUP-BY batch is a possible perf follow-up.
	for i := range out {
		ds, derr := loadDeliveryStatus(ctx, pool, out[i].ID)
		if derr != nil {
			log.Printf("[events] loadDeliveryStatus(list): %v", derr)
			continue
		}
		out[i].DeliveryStatus = ds
	}
	return out, nil
}

var (
	errEventNotFound = errors.New("event not found")
	errEventExpired  = errors.New("event expired")
)

func getEvent(ctx context.Context, pool *pgxpool.Pool, userID, eventID string) (*eventJSON, error) {
	var (
		ej          eventJSON
		schemaVer   int16
		envelopeRaw []byte
		expiresAt   time.Time
	)
	err := pool.QueryRow(ctx,
		`SELECT id, type, schema_version, created_at, agent_id, conversation_id, message_id, status, envelope, expires_at
		 FROM webhook_events
		 WHERE id = $1 AND user_id = $2 AND aud = 'webhook'`,
		eventID, userID,
	).Scan(&ej.ID, &ej.Type, &schemaVer, &ej.CreatedAt,
		&ej.AgentID, &ej.ConversationID, &ej.MessageID, &ej.Status,
		&envelopeRaw, &expiresAt)
	if err != nil {
		// pgx returns sql.ErrNoRows wrapped — detect by ID lookup
		// returning zero rows.
		if isNoRows(err) {
			return nil, errEventNotFound
		}
		return nil, err
	}
	if time.Now().After(expiresAt) {
		return nil, errEventExpired
	}
	ej.SchemaVersion = int(schemaVer)
	var env struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(envelopeRaw, &env); err != nil || env.Data == nil {
		env.Data = map[string]interface{}{} // never serialize required `data` as null (B3)
	}
	ej.Data = env.Data

	// Populate delivery_status by counting webhook_subscriber_deliveries.
	ds, err := loadDeliveryStatus(ctx, pool, eventID)
	if err != nil {
		log.Printf("[events] loadDeliveryStatus: %v", err)
	} else {
		ej.DeliveryStatus = ds
	}

	return &ej, nil
}

func loadDeliveryStatus(ctx context.Context, pool *pgxpool.Pool, eventID string) (*deliveryStatusJSON, error) {
	rows, err := pool.Query(ctx,
		`SELECT status, count(*) FROM webhook_subscriber_deliveries
		 WHERE event_id = $1
		 GROUP BY status`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ds := &deliveryStatusJSON{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		ds.MatchedWebhooks += count
		switch status {
		case "delivered":
			ds.Delivered = count
		case "pending":
			ds.Pending = count
		case "failed":
			ds.Failed = count
		}
	}
	return ds, rows.Err()
}
func isNoRows(err error) bool {
	// pgx's no-rows error embeds the standard sql.ErrNoRows.
	return err != nil && err.Error() == "no rows in result set"
}

// identityStoreRef is referenced here only as a compile-time hint that
// the events API depends on the same identity package other handlers
// use; no functional dependency.
var _ = (*identity.Store)(nil)
