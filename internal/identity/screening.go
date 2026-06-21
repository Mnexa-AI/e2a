package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Screening source values (screening_events.source).
const (
	ScreeningSourceGate = "gate"
	ScreeningSourceScan = "scan"
)

// Review reason values (messages.review_reason and screening_events.reason).
const (
	ReviewReasonSenderGate    = "sender_gate"
	ReviewReasonRecipientGate = "recipient_gate"
	ReviewReasonInboundScan   = "inbound_scan"
	ReviewReasonOutboundScan  = "outbound_scan"
	ReviewReasonOutboundSend  = "outbound_send"
)

// ScreeningEvent is one row of the durable, append-only screening audit log
// (migration 037). It records a single producer's verdict on a message — a gate
// violation (source=gate; the scan-only columns are nil) or a scan detection
// (source=scan; detector/score/categories/spans/raw populated). message_id is a soft
// reference: the trail outlives the message's 30-day TTL.
type ScreeningEvent struct {
	ID          string          `json:"id"`
	MessageID   string          `json:"message_id"`
	AgentID     string          `json:"agent_id"`
	Direction   string          `json:"direction"` // inbound | outbound
	Source      string          `json:"source"`    // gate | scan
	Reason      string          `json:"reason"`    // sender_gate | recipient_gate | inbound_scan | outbound_scan | outbound_send
	Action      string          `json:"action"`    // flag | review | block
	SubjectAddr string          `json:"subject_addr,omitempty"`
	Detector    string          `json:"detector,omitempty"`
	Score       *float64        `json:"score,omitempty"`
	Categories  json.RawMessage `json:"categories,omitempty"`
	Spans       json.RawMessage `json:"spans,omitempty"`
	Raw         json.RawMessage `json:"raw,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// NewScreeningEventID returns a fresh random screening-event id.
func NewScreeningEventID() string { return "scr_" + generateID() }

// DeterministicScreeningEventID derives a stable id from the dedupe key
// (message + source + reason + detector) so that re-screening the same message — e.g.
// an MTA-retried inbound delivery — inserts the SAME row via ON CONFLICT DO NOTHING
// instead of a duplicate. Mirrors webhookpub.DeterministicEventID.
func DeterministicScreeningEventID(messageID, source, reason, detector string) string {
	h := sha256.Sum256([]byte(messageID + "|" + source + "|" + reason + "|" + detector))
	return "scr_" + hex.EncodeToString(h[:16])
}

// CreateScreeningEvent appends a screening event. The insert is idempotent on the
// primary key: callers that set a DeterministicScreeningEventID get exactly-once
// recording across retries. If ev.ID is empty a random id is assigned. A conflicting
// id is a no-op (returns nil).
func (s *Store) CreateScreeningEvent(ctx context.Context, ev ScreeningEvent) error {
	if ev.ID == "" {
		ev.ID = NewScreeningEventID()
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO screening_events
		    (id, message_id, agent_id, direction, source, reason, action,
		     subject_addr, detector, score, categories, spans, raw)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 ON CONFLICT (id) DO NOTHING`,
		ev.ID, ev.MessageID, ev.AgentID, ev.Direction, ev.Source, ev.Reason, ev.Action,
		nullString(ev.SubjectAddr), nullString(ev.Detector), ev.Score,
		nullJSON(ev.Categories), nullJSON(ev.Spans), nullJSON(ev.Raw),
	)
	if err != nil {
		return fmt.Errorf("create screening event: %w", err)
	}
	return nil
}

// ListScreeningEventsByMessage returns every screening event recorded against a
// message, newest first — the breakdown a reviewer sees for a held item.
func (s *Store) ListScreeningEventsByMessage(ctx context.Context, messageID string) ([]ScreeningEvent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, message_id, agent_id, direction, source, reason, action,
		        subject_addr, detector, score, categories, spans, raw, created_at
		   FROM screening_events
		  WHERE message_id = $1
		  ORDER BY created_at DESC, id`,
		messageID,
	)
	if err != nil {
		return nil, fmt.Errorf("list screening events by message: %w", err)
	}
	defer rows.Close()
	return scanScreeningEvents(rows)
}

// ListScreeningEventsByAgent returns an agent's most recent screening events
// (newest first, capped by limit) for the security/analytics view.
func (s *Store) ListScreeningEventsByAgent(ctx context.Context, agentID string, limit int) ([]ScreeningEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, message_id, agent_id, direction, source, reason, action,
		        subject_addr, detector, score, categories, spans, raw, created_at
		   FROM screening_events
		  WHERE agent_id = $1
		  ORDER BY created_at DESC, id
		  LIMIT $2`,
		agentID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list screening events by agent: %w", err)
	}
	defer rows.Close()
	return scanScreeningEvents(rows)
}

// rowScanner is the minimal surface of pgx.Rows used here, so scanScreeningEvents
// stays decoupled from the concrete driver type.
type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanScreeningEvents(rows rowScanner) ([]ScreeningEvent, error) {
	var out []ScreeningEvent
	for rows.Next() {
		var ev ScreeningEvent
		var subjectAddr, detector *string
		var categories, spans, raw []byte
		if err := rows.Scan(
			&ev.ID, &ev.MessageID, &ev.AgentID, &ev.Direction, &ev.Source, &ev.Reason, &ev.Action,
			&subjectAddr, &detector, &ev.Score, &categories, &spans, &raw, &ev.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan screening event: %w", err)
		}
		if subjectAddr != nil {
			ev.SubjectAddr = *subjectAddr
		}
		if detector != nil {
			ev.Detector = *detector
		}
		ev.Categories = jsonOrNil(categories)
		ev.Spans = jsonOrNil(spans)
		ev.Raw = jsonOrNil(raw)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate screening events: %w", err)
	}
	return out, nil
}

// nullString returns nil for empty strings so they persist as SQL NULL.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullJSON returns nil for empty/zero JSON so it persists as SQL NULL.
func nullJSON(j json.RawMessage) any {
	if len(j) == 0 {
		return nil
	}
	return []byte(j)
}

func jsonOrNil(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}

// SetMessageScreening denormalizes a screening verdict onto an already-created
// message row (review_reason / scan_score / scan_action). The inbound path sets
// these at INSERT time; the outbound path creates the row first (send or HITL
// hold) and annotates after, so it needs this UPDATE. Agent-scoped to keep the
// write within the owning tenant. scanScore may be nil (gate-only verdicts).
func (s *Store) SetMessageScreening(ctx context.Context, messageID, agentID, reviewReason string, scanScore *float64, scanAction string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE messages
		    SET review_reason = $3, scan_score = $4, scan_action = $5
		  WHERE id = $1 AND agent_id = $2`,
		messageID, agentID, nullIfEmptyString(reviewReason), scanScore, nullIfEmptyString(scanAction))
	return err
}
