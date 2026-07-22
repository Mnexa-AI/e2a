//go:build integration

package messagelifecycle

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/migrations"
)

func TestMessageLifecycleMigrationCatalogMatchesCanonicalCatalog(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)

	rows, err := pool.Query(ctx, `
		SELECT code, stage, outcome, retryable
		FROM message_lifecycle_reason_codes
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	got := make(map[ReasonCode]Definition)
	for rows.Next() {
		var code ReasonCode
		var definition Definition
		if err := rows.Scan(&code, &definition.Stage, &definition.Outcome, &definition.Retryable); err != nil {
			t.Fatal(err)
		}
		got[code] = definition
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := Catalog(); !reflect.DeepEqual(got, want) {
		t.Fatalf("database catalog mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestMessageLifecycleMigrationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	sql, err := migrations.FS.ReadFile("073_message_lifecycle.sql")
	if err != nil {
		t.Fatal(err)
	}

	before := lifecycleCatalogRows(t, pool)
	for i := 0; i < 2; i++ {
		if err := identity.RunMigrations(ctx, pool, migrations.FS, identity.ModeAuto); err != nil {
			t.Fatalf("full migration run %d: %v", i+1, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("migration rerun %d: %v", i+1, err)
		}
	}
	after := lifecycleCatalogRows(t, pool)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("catalog changed after migration replay\nbefore: %#v\nafter:  %#v", before, after)
	}
	if len(after) != 30 {
		t.Fatalf("catalog row count = %d, want 30", len(after))
	}
}

func TestMessageLifecycleMigrationRejectsContradictoryTuple(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_fk", "outbound")

	_, err := pool.Exec(ctx, `
		INSERT INTO message_lifecycle_transitions
			(id, message_id, dedupe_key, direction, stage, outcome, reason_code, retryable, occurred_at)
		VALUES
			('mlt_fk', 'msg_fk', 'fk', 'outbound', 'accepted', 'failed', 'acceptance.outbound_api', false, now())
	`)
	if err == nil {
		t.Fatal("contradictory reason tuple unexpectedly persisted")
	}
}

func TestAppendTxStoresCanonicalTransition(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_append", "outbound")
	input := lifecycleInput("msg_append", "first")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := AppendTx(ctx, tx, input)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(got.ID, "mlt_") || len(got.ID) != len("mlt_")+32 {
		t.Fatalf("ID = %q, want mlt_ plus 32 lowercase hex characters", got.ID)
	}
	if got.OccurredAt.Location() != time.UTC {
		t.Fatalf("OccurredAt location = %v, want UTC", got.OccurredAt.Location())
	}
	if got.Evidence == nil || got.CorrelationIDs == nil {
		t.Fatalf("maps must be non-nil: evidence=%#v correlation=%#v", got.Evidence, got.CorrelationIDs)
	}
	if got.Reconstructed {
		t.Fatal("new transition must not be reconstructed")
	}
	if got.MessageID != input.MessageID || got.Direction != input.Direction || got.Recipient != input.Recipient || got.ReasonCode != input.ReasonCode {
		t.Fatalf("stored transition does not match input: %#v", got)
	}
}

func TestAppendTxIdenticalReplayReturnsOriginal(t *testing.T) {
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_replay", "outbound")
	input := lifecycleInput("msg_replay", "same")

	first := appendAndCommit(t, pool, input)
	second := appendAndCommit(t, pool, input)
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("replay differs\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if got := lifecycleCount(t, pool, input.MessageID); got != 1 {
		t.Fatalf("row count = %d, want 1", got)
	}
}

func TestAppendTxDedupeConflictForSemanticDifferences(t *testing.T) {
	baseTime := time.Date(2026, 7, 21, 12, 0, 0, 123000000, time.FixedZone("PDT", -7*60*60))
	tests := []struct {
		name   string
		modify func(*AppendInput)
	}{
		{"reason", func(in *AppendInput) { in.ReasonCode = ReasonDeliveryTemporaryDelay }},
		{"direction", func(in *AppendInput) { in.Direction = "inbound" }},
		{"recipient", func(in *AppendInput) { in.Recipient = "other@example.com" }},
		{"evidence", func(in *AppendInput) { in.Evidence = map[string]any{"smtp_detail": "251 forwarded"} }},
		{"correlation", func(in *AppendInput) { in.CorrelationIDs = map[string]string{"event_id": "evt_other"} }},
		{"occurred_at", func(in *AppendInput) { in.OccurredAt = in.OccurredAt.Add(time.Second) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pool := testutil.TestDB(t)
			messageID := "msg_conflict_" + tt.name
			insertLifecycleMessage(t, pool, messageID, "outbound")
			originalInput := lifecycleInput(messageID, "same-key")
			originalInput.OccurredAt = baseTime
			original := appendAndCommit(t, pool, originalInput)

			conflicting := originalInput
			tt.modify(&conflicting)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = AppendTx(ctx, tx, conflicting)
			if !errors.Is(err, ErrDedupeConflict) {
				_ = tx.Rollback(ctx)
				t.Fatalf("AppendTx error = %v, want ErrDedupeConflict", err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if got := lifecycleCount(t, pool, messageID); got != 1 {
				t.Fatalf("row count = %d, want 1", got)
			}
			stored := loadOnlyTransition(t, pool, messageID)
			if !reflect.DeepEqual(stored, original) {
				t.Fatalf("original mutated\ngot:  %#v\nwant: %#v", stored, original)
			}
		})
	}
}

func TestAppendTxRollbackLeavesNoTransition(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_rollback", "outbound")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AppendTx(ctx, tx, lifecycleInput("msg_rollback", "rollback")); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if got := lifecycleCount(t, pool, "msg_rollback"); got != 0 {
		t.Fatalf("row count after rollback = %d, want 0", got)
	}
}

func TestLifecycleOrderedListBreaksEqualTimestampsByID(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_order", "outbound")
	when := time.Date(2026, 7, 21, 19, 0, 0, 0, time.UTC)
	var wantIDs []string
	for _, key := range []string{"one", "two", "three"} {
		input := lifecycleInput("msg_order", key)
		input.OccurredAt = when
		wantIDs = append(wantIDs, appendAndCommit(t, pool, input).ID)
	}
	sort.Strings(wantIDs)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := listTransitionsTx(ctx, tx, "msg_order")
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := make([]string, len(got))
	for i := range got {
		gotIDs[i] = got[i].ID
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("ordered IDs = %v, want %v", gotIDs, wantIDs)
	}
}

func TestLifecycleMessageDeletionCascades(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_cascade", "outbound")
	appendAndCommit(t, pool, lifecycleInput("msg_cascade", "cascade"))
	if _, err := pool.Exec(ctx, `DELETE FROM messages WHERE id = 'msg_cascade'`); err != nil {
		t.Fatal(err)
	}
	if got := lifecycleCount(t, pool, "msg_cascade"); got != 0 {
		t.Fatalf("row count after message deletion = %d, want 0", got)
	}
}

func TestAppendTxConcurrentDuplicateConverges(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_concurrent", "outbound")
	input := lifecycleInput("msg_concurrent", "concurrent")

	start := make(chan struct{})
	results := make(chan MessageLifecycleTransition, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := pool.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			<-start
			transition, err := AppendTx(ctx, tx, input)
			if err != nil {
				_ = tx.Rollback(ctx)
				errs <- err
				return
			}
			if err := tx.Commit(ctx); err != nil {
				errs <- err
				return
			}
			results <- transition
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Fatalf("concurrent append: %v", err)
	}
	var got []MessageLifecycleTransition
	for transition := range results {
		got = append(got, transition)
	}
	if len(got) != 2 || !reflect.DeepEqual(got[0], got[1]) {
		t.Fatalf("concurrent results did not converge: %#v", got)
	}
	if count := lifecycleCount(t, pool, input.MessageID); count != 1 {
		t.Fatalf("row count = %d, want 1", count)
	}
}

func lifecycleInput(messageID, dedupeKey string) AppendInput {
	return AppendInput{
		MessageID:      messageID,
		DedupeKey:      dedupeKey,
		Direction:      "outbound",
		Recipient:      "person@example.com",
		ReasonCode:     ReasonDeliveryRecipientServerAccepted,
		Evidence:       map[string]any{"smtp_detail": "250 2.0.0 accepted"},
		CorrelationIDs: map[string]string{"event_id": "evt_123"},
		OccurredAt:     time.Date(2026, 7, 21, 12, 0, 0, 123000000, time.FixedZone("PDT", -7*60*60)),
	}
}

func insertLifecycleMessage(t *testing.T, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, messageID, direction string) {
	t.Helper()
	ctx := context.Background()
	userID := "usr_" + messageID
	agentID := "agt_" + messageID
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, name, google_subject)
		VALUES ($1, $2, '', $3);
		INSERT INTO agent_identities (id, registered_domain, user_id, name)
		VALUES ($4, 'agents.e2a.dev', $1, '');
		INSERT INTO messages (id, agent_id, direction)
		VALUES ($5, $4, $6)
	`, userID, userID+"@example.com", userID, agentID, messageID, direction); err != nil {
		t.Fatal(err)
	}
}

func appendAndCommit(t *testing.T, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, input AppendInput) MessageLifecycleTransition {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transition, err := AppendTx(ctx, tx, input)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return transition
}

func lifecycleCount(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, messageID string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM message_lifecycle_transitions WHERE message_id = $1
	`, messageID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func loadOnlyTransition(t *testing.T, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, messageID string) MessageLifecycleTransition {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items, err := listTransitionsTx(ctx, tx, messageID)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("transition count = %d, want 1", len(items))
	}
	return items[0]
}

func lifecycleCatalogRows(t *testing.T, pool interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) [][4]any {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT code, stage, outcome, retryable
		FROM message_lifecycle_reason_codes
		ORDER BY code
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result [][4]any
	for rows.Next() {
		var code, stage, outcome string
		var retryable bool
		if err := rows.Scan(&code, &stage, &outcome, &retryable); err != nil {
			t.Fatal(err)
		}
		result = append(result, [4]any{code, stage, outcome, retryable})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
