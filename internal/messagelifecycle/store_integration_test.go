//go:build integration

package messagelifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

func TestMessageLifecycleMigrationRejectsCatalogDrift(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	sql, err := migrations.FS.ReadFile("073_message_lifecycle.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const code = "delivery.recipient_server_accepted"
	if _, err := tx.Exec(ctx, `
		UPDATE message_lifecycle_reason_codes
		SET outcome = 'deferred'
		WHERE code = $1
	`, code); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, string(sql))
	if err == nil {
		t.Fatal("migration accepted a drifted immutable catalog tuple")
	}
	if !strings.Contains(err.Error(), code) {
		t.Fatalf("migration error %q does not identify drifted code", err)
	}
	for _, forbidden := range []string{"deferred", "delivered", "false"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("migration error exposes tuple detail %q: %v", forbidden, err)
		}
	}
}

func TestMessageLifecycleMigrationBuildsTransitionsFromExistingExactCatalog(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	sql, err := migrations.FS.ReadFile("073_message_lifecycle.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DROP TABLE message_lifecycle_transitions`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("migration with existing exact catalog: %v", err)
	}
	var indexExists bool
	if err := tx.QueryRow(ctx, `
		SELECT to_regclass('message_lifecycle_message_order_idx') IS NOT NULL
	`).Scan(&indexExists); err != nil {
		t.Fatal(err)
	}
	if !indexExists {
		t.Fatal("message lifecycle ordering index was not created")
	}
	var transitionConstraints string
	if err := tx.QueryRow(ctx, `
		SELECT string_agg(pg_get_constraintdef(oid), E'\n' ORDER BY contype, conname)
		FROM pg_constraint
		WHERE conrelid = 'message_lifecycle_transitions'::regclass
	`).Scan(&transitionConstraints); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"PRIMARY KEY (id)",
		"CHECK ((direction = ANY",
		"FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE",
		"UNIQUE (message_id, dedupe_key)",
		"FOREIGN KEY (reason_code, stage, outcome, retryable) REFERENCES message_lifecycle_reason_codes(code, stage, outcome, retryable)",
	} {
		if !strings.Contains(transitionConstraints, required) {
			t.Errorf("transition constraints missing %q:\n%s", required, transitionConstraints)
		}
	}
	var catalogConstraints string
	if err := tx.QueryRow(ctx, `
		SELECT string_agg(pg_get_constraintdef(oid), E'\n' ORDER BY contype, conname)
		FROM pg_constraint
		WHERE conrelid = 'message_lifecycle_reason_codes'::regclass
	`).Scan(&catalogConstraints); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"PRIMARY KEY (code)",
		"CHECK ((stage = ANY",
		"CHECK ((outcome = ANY",
		"UNIQUE (code, stage, outcome, retryable)",
	} {
		if !strings.Contains(catalogConstraints, required) {
			t.Errorf("catalog constraints missing %q:\n%s", required, catalogConstraints)
		}
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

func TestAppendTxReplayNormalizesEquivalentRepresentations(t *testing.T) {
	tests := []struct {
		name     string
		original func(*AppendInput)
		replayed func(*AppendInput)
	}{
		{
			name: "JSON key order",
			original: func(in *AppendInput) {
				in.Evidence = map[string]any{"smtp_detail": "accepted", "failure_code": "none"}
			},
			replayed: func(in *AppendInput) {
				in.Evidence = map[string]any{"failure_code": "none", "smtp_detail": "accepted"}
			},
		},
		{
			name: "nil and empty maps",
			original: func(in *AppendInput) {
				in.Evidence = nil
				in.CorrelationIDs = nil
			},
			replayed: func(in *AppendInput) {
				in.Evidence = map[string]any{}
				in.CorrelationIDs = map[string]string{}
			},
		},
		{
			name: "empty correlation filtering",
			original: func(in *AppendInput) {
				in.CorrelationIDs = map[string]string{"event_id": ""}
			},
			replayed: func(in *AppendInput) { in.CorrelationIDs = nil },
		},
		{
			name: "timezone and sub-microsecond timestamp",
			original: func(in *AppendInput) {
				in.OccurredAt = time.Date(2026, 7, 21, 12, 0, 0, 123456100, time.FixedZone("PDT", -7*60*60))
			},
			replayed: func(in *AppendInput) {
				in.OccurredAt = time.Date(2026, 7, 21, 19, 0, 0, 123456999, time.UTC)
			},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := testutil.TestDB(t)
			messageID := fmt.Sprintf("msg_equivalent_%d", i)
			insertLifecycleMessage(t, pool, messageID, "outbound")
			originalInput := lifecycleInput(messageID, "equivalent")
			tt.original(&originalInput)
			original := appendAndCommit(t, pool, originalInput)
			replayedInput := lifecycleInput(messageID, "equivalent")
			tt.replayed(&replayedInput)
			replayed := appendAndCommit(t, pool, replayedInput)
			if !reflect.DeepEqual(replayed, original) {
				t.Fatalf("equivalent replay differs\noriginal: %#v\nreplayed: %#v", original, replayed)
			}
		})
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
			const sensitiveDedupeKey = "customer-secret-dedupe-key"
			originalInput := lifecycleInput(messageID, sensitiveDedupeKey)
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
			for _, sensitive := range []string{
				sensitiveDedupeKey, "250 2.0.0 accepted", "251 forwarded", "evt_123", "evt_other",
			} {
				if strings.Contains(err.Error(), sensitive) {
					_ = tx.Rollback(ctx)
					t.Fatalf("conflict error exposes sensitive content %q: %v", sensitive, err)
				}
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
	winnerTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	winner, err := AppendTx(ctx, winnerTx, input)
	if err != nil {
		_ = winnerTx.Rollback(ctx)
		t.Fatal(err)
	}

	testCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	type result struct {
		transition MessageLifecycleTransition
		err        error
	}
	started := make(chan struct{})
	resultCh := make(chan result, 1)
	go func() {
		loserTx, err := pool.Begin(testCtx)
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		close(started)
		transition, err := AppendTx(testCtx, loserTx, input)
		if err == nil {
			err = loserTx.Commit(testCtx)
		} else {
			_ = loserTx.Rollback(testCtx)
		}
		resultCh <- result{transition: transition, err: err}
	}()
	<-started
	select {
	case result := <-resultCh:
		_ = winnerTx.Rollback(ctx)
		t.Fatalf("loser returned before uncommitted winner resolved: %#v", result)
	case <-time.After(100 * time.Millisecond):
	}
	if err := winnerTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("concurrent loser append: %v", result.err)
		}
		if !reflect.DeepEqual(result.transition, winner) {
			t.Fatalf("loser result differs\nwinner: %#v\nloser:  %#v", winner, result.transition)
		}
	case <-testCtx.Done():
		t.Fatalf("concurrent loser did not finish: %v", testCtx.Err())
	}
	if count := lifecycleCount(t, pool, input.MessageID); count != 1 {
		t.Fatalf("row count = %d, want 1", count)
	}
}

func TestListForMessageOwnedForeignAndMissing(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_list_scope", "outbound")
	store := NewStore(pool)

	got, err := store.ListForMessage(ctx, "msg_list_scope", "agt_msg_list_scope")
	if err != nil || len(got) != 1 || got[0].ReasonCode != ReasonAcceptanceOutboundAPI {
		t.Fatalf("owned list = %#v, %v", got, err)
	}
	for _, tc := range []struct{ messageID, agentID string }{
		{"msg_list_scope", "agt_foreign"},
		{"msg_missing", "agt_msg_list_scope"},
	} {
		got, err := store.ListForMessage(ctx, tc.messageID, tc.agentID)
		if !errors.Is(err, ErrMessageNotFound) || got != nil {
			t.Fatalf("ListForMessage(%q,%q) = %#v, %v; want hidden not found", tc.messageID, tc.agentID, got, err)
		}
	}
}

func TestListForMessageHistoricalInboundAuthentication(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_list_auth", "inbound")
	if _, err := pool.Exec(ctx, `
		UPDATE messages
		SET method='smtp', authentication=$2, created_at=$3
		WHERE id=$1
	`, "msg_list_auth", authenticationJSON("pass"), reconstructBaseTime); err != nil {
		t.Fatal(err)
	}

	got, err := NewStore(pool).ListForMessage(ctx, "msg_list_auth", "agt_msg_list_auth")
	if err != nil {
		t.Fatal(err)
	}
	assertReasons(t, got, ReasonAcceptanceInboundSMTP, ReasonAuthenticationDMARCPass)
	if findReason(got, ReasonAuthenticationDMARCPass).Evidence["authentication"] == nil {
		t.Fatal("historical authentication evidence missing")
	}
}

func TestListForMessageHistoricalOutboundRecipientEventAndSuppression(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_list_outbound", "outbound")
	if _, err := pool.Exec(ctx, `UPDATE messages SET method='smtp', created_at=$2 WHERE id=$1`, "msg_list_outbound", reconstructBaseTime); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO message_recipients (id, message_id, address, status, detail, updated_at)
		VALUES ('rcp_list', $1, 'delivered@example.com', 'delivered', 'state detail', $2)
	`, "msg_list_outbound", reconstructBaseTime.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO suppressions (id, user_id, address, source, source_message_id, created_at)
		VALUES ('sup_list', $1, 'bounce@example.com', 'bounce', $2, $3)
	`, "usr_msg_list_outbound", "msg_list_outbound", reconstructBaseTime.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	event := eventSnapshot("evt_list", "email.delivered", reconstructBaseTime.Add(time.Minute), map[string]any{
		"message_id": "msg_list_outbound", "delivered_to": "delivered@example.com", "smtp_detail": "event detail",
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO webhook_events (id, user_id, type, envelope, message_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, event.ID, "usr_msg_list_outbound", event.Type, event.Envelope, "msg_list_outbound", event.CreatedAt); err != nil {
		t.Fatal(err)
	}

	got, err := NewStore(pool).ListForMessage(ctx, "msg_list_outbound", "agt_msg_list_outbound")
	if err != nil {
		t.Fatal(err)
	}
	assertReasons(t, got, ReasonAcceptanceOutboundAPI, ReasonDeliveryRecipientServerAccepted, ReasonSuppressionHardBounceApplied)
	delivery := findReason(got, ReasonDeliveryRecipientServerAccepted)
	if delivery.CorrelationIDs["event_id"] != "evt_list" || delivery.Evidence["smtp_detail"] != "event detail" {
		t.Fatalf("delivery did not prefer retained event: %#v", delivery)
	}
}

func TestListForMessageDeterministicPersistedPrecedenceOrderingAndNoWrites(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	insertLifecycleMessage(t, pool, "msg_list_merge", "outbound")
	when := reconstructBaseTime
	if _, err := pool.Exec(ctx, `UPDATE messages SET method='smtp', created_at=$2 WHERE id=$1`, "msg_list_merge", when); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO message_recipients (id, message_id, address, status, updated_at)
		VALUES ('rcp_merge', $1, 'a@example.com', 'delivered', $2)
	`, "msg_list_merge", when); err != nil {
		t.Fatal(err)
	}
	persisted := appendAndCommit(t, pool, AppendInput{
		MessageID: "msg_list_merge", DedupeKey: "persisted-delivery", Direction: "outbound",
		Recipient: "a@example.com", ReasonCode: ReasonDeliveryRecipientServerAccepted,
		OccurredAt: when, Evidence: map[string]any{"smtp_detail": "persisted"},
	})

	store := NewStore(pool)
	before := lifecycleCount(t, pool, "msg_list_merge")
	first, err := store.ListForMessage(ctx, "msg_list_merge", "agt_msg_list_merge")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ListForMessage(ctx, "msg_list_merge", "agt_msg_list_merge")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeat reads differ\nfirst: %#v\nsecond:%#v", first, second)
	}
	if after := lifecycleCount(t, pool, "msg_list_merge"); after != before {
		t.Fatalf("ListForMessage wrote transitions: before=%d after=%d", before, after)
	}
	if len(first) != 2 || findReason(first, ReasonAcceptanceOutboundAPI) == nil {
		t.Fatalf("missing reconstructed earlier acceptance: %#v", first)
	}
	delivery := findReason(first, ReasonDeliveryRecipientServerAccepted)
	if delivery == nil || delivery.ID != persisted.ID || delivery.Reconstructed || delivery.Evidence["smtp_detail"] != "persisted" {
		t.Fatalf("persisted transition did not win: %#v", delivery)
	}
	for i := 1; i < len(first); i++ {
		if first[i].OccurredAt.Before(first[i-1].OccurredAt) || (first[i].OccurredAt.Equal(first[i-1].OccurredAt) && first[i].ID < first[i-1].ID) {
			t.Fatalf("not ordered by (occurred_at,id): %#v", first)
		}
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
	Begin(context.Context) (pgx.Tx, error)
}, messageID, direction string) {
	t.Helper()
	ctx := context.Background()
	userID := "usr_" + messageID
	agentID := "agt_" + messageID
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, name, google_subject)
		VALUES ($1, $2, '', $3)
	`, userID, userID+"@example.com", userID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_identities (id, registered_domain, user_id, name)
		VALUES ($1, 'agents.e2a.dev', $2, '')
	`, agentID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO messages (id, agent_id, direction)
		VALUES ($1, $2, $3)
	`, messageID, agentID, direction); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
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
