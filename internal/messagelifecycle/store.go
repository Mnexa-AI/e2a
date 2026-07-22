package messagelifecycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrDedupeConflict means a producer reused a message-local dedupe key for a
// semantically different lifecycle observation.
var ErrDedupeConflict = errors.New("message lifecycle dedupe conflict")

// ErrMessageNotFound intentionally covers both absent and foreign messages.
var ErrMessageNotFound = errors.New("message lifecycle message not found")

// Store reads canonical and conservatively reconstructed message lifecycle facts.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ListForMessage returns the lifecycle for one message owned by agentID.
// Reconstruction is read-only and is never persisted by this method.
func (s *Store) ListForMessage(ctx context.Context, messageID, agentID string) ([]MessageLifecycleTransition, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin message lifecycle read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var snapshot Snapshot
	var authentication []byte
	err = tx.QueryRow(ctx, `
		SELECT m.id, m.agent_id, a.user_id, m.direction, COALESCE(m.method, ''),
		       m.created_at, m.authentication, COALESCE(m.status, ''),
		       m.approval_expires_at, m.reviewed_at, m.send_job_id,
		       r.created_at, m.provider_accepted_at,
		       COALESCE(m.provider_message_id, ''),
		       COALESCE(m.email_message_id, ''),
		       COALESCE(m.delivery_status, ''),
		       COALESCE(m.delivery_failure_source, '')
		FROM messages m
		JOIN agent_identities a ON a.id = m.agent_id
		LEFT JOIN river_job r ON r.id = m.send_job_id
		WHERE m.id = $1 AND m.agent_id = $2
	`, messageID, agentID).Scan(
		&snapshot.MessageID, &snapshot.AgentID, &snapshot.UserID, &snapshot.Direction, &snapshot.Method,
		&snapshot.CreatedAt, &authentication, &snapshot.Status,
		&snapshot.ApprovalExpiresAt, &snapshot.ReviewedAt, &snapshot.SendJobID,
		&snapshot.JobCreatedAt, &snapshot.ProviderAcceptedAt,
		&snapshot.ProviderMessageID, &snapshot.EmailMessageID,
		&snapshot.DeliveryStatus, &snapshot.DeliveryFailureSource,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMessageNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load message lifecycle snapshot: %w", err)
	}
	if authentication != nil {
		snapshot.Authentication = append(json.RawMessage(nil), authentication...)
	}

	recipientRows, err := tx.Query(ctx, `
		SELECT id, address, status, COALESCE(detail, ''), updated_at
		FROM message_recipients
		WHERE message_id = $1
		ORDER BY updated_at ASC, id ASC
	`, messageID)
	if err != nil {
		return nil, fmt.Errorf("load message lifecycle recipients: %w", err)
	}
	for recipientRows.Next() {
		var recipient RecipientSnapshot
		if err := recipientRows.Scan(&recipient.ID, &recipient.Address, &recipient.Status, &recipient.Detail, &recipient.UpdatedAt); err != nil {
			recipientRows.Close()
			return nil, fmt.Errorf("scan message lifecycle recipient: %w", err)
		}
		snapshot.Recipients = append(snapshot.Recipients, recipient)
	}
	err = recipientRows.Err()
	recipientRows.Close()
	if err != nil {
		return nil, fmt.Errorf("iterate message lifecycle recipients: %w", err)
	}

	suppressionRows, err := tx.Query(ctx, `
		SELECT id, address, source, source_message_id, created_at
		FROM suppressions
		WHERE source_message_id = $1 AND user_id = $2
		ORDER BY created_at ASC, id ASC
	`, messageID, snapshot.UserID)
	if err != nil {
		return nil, fmt.Errorf("load message lifecycle suppressions: %w", err)
	}
	for suppressionRows.Next() {
		var suppression SuppressionSnapshot
		if err := suppressionRows.Scan(&suppression.ID, &suppression.Address, &suppression.Source, &suppression.SourceMessageID, &suppression.CreatedAt); err != nil {
			suppressionRows.Close()
			return nil, fmt.Errorf("scan message lifecycle suppression: %w", err)
		}
		snapshot.Suppressions = append(snapshot.Suppressions, suppression)
	}
	err = suppressionRows.Err()
	suppressionRows.Close()
	if err != nil {
		return nil, fmt.Errorf("iterate message lifecycle suppressions: %w", err)
	}

	eventRows, err := tx.Query(ctx, `
		SELECT id, type, envelope, created_at
		FROM webhook_events
		WHERE message_id = $1
		  AND user_id = $2
		  AND type = ANY($3::text[])
		ORDER BY created_at ASC, id ASC
	`, messageID, snapshot.UserID, []string{
		"email.received", "email.sent", "email.failed", "email.delivered",
		"email.bounced", "email.complained", "email.review_requested",
		"email.review_approved", "email.review_rejected", "domain.suppression_added",
	})
	if err != nil {
		return nil, fmt.Errorf("load message lifecycle events: %w", err)
	}
	for eventRows.Next() {
		var event EventSnapshot
		if err := eventRows.Scan(&event.ID, &event.Type, &event.Envelope, &event.CreatedAt); err != nil {
			eventRows.Close()
			return nil, fmt.Errorf("scan message lifecycle event: %w", err)
		}
		snapshot.Events = append(snapshot.Events, event)
	}
	err = eventRows.Err()
	eventRows.Close()
	if err != nil {
		return nil, fmt.Errorf("iterate message lifecycle events: %w", err)
	}

	persisted, err := listTransitionsTx(ctx, tx, messageID)
	if err != nil {
		return nil, fmt.Errorf("load persisted message lifecycle: %w", err)
	}
	result := MergeTransitions(persisted, Reconstruct(snapshot))
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit message lifecycle read: %w", err)
	}
	return result, nil
}

const transitionColumns = `
	id, message_id, direction, recipient, stage, outcome, reason_code,
	retryable, evidence, correlation_ids, occurred_at, reconstructed
`

// AppendTx validates and appends one lifecycle observation in the caller's
// transaction. Identical retries return the original stored transition.
func AppendTx(ctx context.Context, tx pgx.Tx, input AppendInput) (MessageLifecycleTransition, error) {
	candidate, err := NewTransition(input)
	if err != nil {
		return MessageLifecycleTransition{}, err
	}

	id, err := newTransitionID()
	if err != nil {
		return MessageLifecycleTransition{}, err
	}
	evidence, err := json.Marshal(candidate.Evidence)
	if err != nil {
		return MessageLifecycleTransition{}, fmt.Errorf("marshal lifecycle evidence: %w", err)
	}
	correlationIDs, err := json.Marshal(candidate.CorrelationIDs)
	if err != nil {
		return MessageLifecycleTransition{}, fmt.Errorf("marshal lifecycle correlation IDs: %w", err)
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO message_lifecycle_transitions (
			id, message_id, dedupe_key, direction, recipient, stage, outcome,
			reason_code, retryable, evidence, correlation_ids, occurred_at,
			reconstructed
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (message_id, dedupe_key) DO NOTHING
		RETURNING `+transitionColumns,
		id, candidate.MessageID, input.DedupeKey, candidate.Direction,
		nullableRecipient(candidate.Recipient), candidate.Stage, candidate.Outcome,
		candidate.ReasonCode, candidate.Retryable, evidence, correlationIDs,
		candidate.OccurredAt, candidate.Reconstructed,
	)
	inserted, err := scanTransition(row)
	if err == nil {
		return inserted, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return MessageLifecycleTransition{}, fmt.Errorf("insert message lifecycle transition: %w", err)
	}

	existing, err := scanTransition(tx.QueryRow(ctx, `
		SELECT `+transitionColumns+`
		FROM message_lifecycle_transitions
		WHERE message_id = $1 AND dedupe_key = $2
	`, candidate.MessageID, input.DedupeKey))
	if err != nil {
		return MessageLifecycleTransition{}, fmt.Errorf("load existing message lifecycle transition: %w", err)
	}
	if field := semanticDifference(existing, candidate); field != "" {
		return MessageLifecycleTransition{}, fmt.Errorf(
			"%w: message_id %q differs in %s",
			ErrDedupeConflict, candidate.MessageID, field,
		)
	}
	return existing, nil
}

func newTransitionID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate message lifecycle transition ID: %w", err)
	}
	return "mlt_" + hex.EncodeToString(random), nil
}

func nullableRecipient(recipient string) any {
	if recipient == "" {
		return nil
	}
	return recipient
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTransition(row rowScanner) (MessageLifecycleTransition, error) {
	var transition MessageLifecycleTransition
	var recipient *string
	var evidence, correlationIDs []byte
	if err := row.Scan(
		&transition.ID,
		&transition.MessageID,
		&transition.Direction,
		&recipient,
		&transition.Stage,
		&transition.Outcome,
		&transition.ReasonCode,
		&transition.Retryable,
		&evidence,
		&correlationIDs,
		&transition.OccurredAt,
		&transition.Reconstructed,
	); err != nil {
		return MessageLifecycleTransition{}, err
	}
	if recipient != nil {
		transition.Recipient = *recipient
	}
	if err := json.Unmarshal(evidence, &transition.Evidence); err != nil {
		return MessageLifecycleTransition{}, fmt.Errorf("decode lifecycle evidence: %w", err)
	}
	if transition.Evidence == nil {
		transition.Evidence = map[string]any{}
	}
	if err := json.Unmarshal(correlationIDs, &transition.CorrelationIDs); err != nil {
		return MessageLifecycleTransition{}, fmt.Errorf("decode lifecycle correlation IDs: %w", err)
	}
	if transition.CorrelationIDs == nil {
		transition.CorrelationIDs = map[string]string{}
	}
	transition.OccurredAt = transition.OccurredAt.UTC()
	return transition, nil
}

func semanticDifference(existing, candidate MessageLifecycleTransition) string {
	candidate.OccurredAt = postgresTime(candidate.OccurredAt)
	existing.OccurredAt = postgresTime(existing.OccurredAt)
	checks := []struct {
		name  string
		equal bool
	}{
		{"message_id", existing.MessageID == candidate.MessageID},
		{"direction", existing.Direction == candidate.Direction},
		{"recipient", existing.Recipient == candidate.Recipient},
		{"stage", existing.Stage == candidate.Stage},
		{"outcome", existing.Outcome == candidate.Outcome},
		{"reason_code", existing.ReasonCode == candidate.ReasonCode},
		{"retryable", existing.Retryable == candidate.Retryable},
		{"evidence", reflect.DeepEqual(existing.Evidence, candidate.Evidence)},
		{"correlation_ids", reflect.DeepEqual(existing.CorrelationIDs, candidate.CorrelationIDs)},
		{"occurred_at", existing.OccurredAt.Equal(candidate.OccurredAt)},
		{"reconstructed", existing.Reconstructed == candidate.Reconstructed},
	}
	for _, check := range checks {
		if !check.equal {
			return check.name
		}
	}
	return ""
}

func postgresTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func listTransitionsTx(ctx context.Context, tx pgx.Tx, messageID string) ([]MessageLifecycleTransition, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+transitionColumns+`
		FROM message_lifecycle_transitions
		WHERE message_id = $1
		ORDER BY occurred_at ASC, id ASC
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transitions []MessageLifecycleTransition
	for rows.Next() {
		transition, err := scanTransition(rows)
		if err != nil {
			return nil, err
		}
		transitions = append(transitions, transition)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return transitions, nil
}
