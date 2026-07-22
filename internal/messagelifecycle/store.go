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
)

// ErrDedupeConflict means a producer reused a message-local dedupe key for a
// semantically different lifecycle observation.
var ErrDedupeConflict = errors.New("message lifecycle dedupe conflict")

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
