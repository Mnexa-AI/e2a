package webhookpub

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestDeterministicEventID_StableAcrossInvocations(t *testing.T) {
	id1 := DeterministicEventID("msg_abc123", "email.received")
	id2 := DeterministicEventID("msg_abc123", "email.received")
	if id1 != id2 {
		t.Fatalf("DeterministicEventID not stable: got %q vs %q", id1, id2)
	}
}

func TestDeterministicEventID_PrefixAndLength(t *testing.T) {
	id := DeterministicEventID("msg_abc123", "email.received")
	if !strings.HasPrefix(id, "evt_") {
		t.Errorf("id should start with evt_: %q", id)
	}
	// "evt_" + 32 hex = 36 chars
	if len(id) != 36 {
		t.Errorf("id length = %d, want 36 (evt_ + 32-hex): %q", len(id), id)
	}
}

func TestDeterministicEventID_DifferentInputsDifferentIDs(t *testing.T) {
	cases := [][]string{
		{"msg_a", "email.received"},
		{"msg_b", "email.received"},
		{"msg_a", "email.sent"},
		{"msg_a", "email.received", "extra"},
	}
	seen := make(map[string]bool)
	for _, args := range cases {
		id := DeterministicEventID(args...)
		if seen[id] {
			t.Errorf("collision on inputs %v -> %s", args, id)
		}
		seen[id] = true
	}
}

func TestDeterministicEventID_PipeDelimiterPreventsAmbiguity(t *testing.T) {
	// Per design §5.1: ("abc","def") must not collide with ("abcdef","").
	a := DeterministicEventID("abc", "def")
	b := DeterministicEventID("abcdef", "")
	if a == b {
		t.Errorf("delimiter ambiguity: (abc,def) collides with (abcdef,) -> %s", a)
	}
}

// fakeExec captures Exec calls for assertion without touching a DB.
// `err` (if set) is returned on every call. `errOnContains` (if set)
// is returned only on calls whose SQL contains the substring — used
// to exercise selective failure modes like "pg_notify fails but the
// INSERT succeeds."
type fakeExec struct {
	calls         []string
	args          [][]any
	err           error
	errOnContains string
	errSelective  error
}

func (f *fakeExec) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.calls = append(f.calls, sql)
	f.args = append(f.args, args)
	if f.errOnContains != "" && strings.Contains(sql, f.errOnContains) {
		return pgconn.CommandTag{}, f.errSelective
	}
	if f.err != nil {
		return pgconn.CommandTag{}, f.err
	}
	return pgconn.CommandTag{}, nil
}

func TestWriteOutboxRow_RejectsEmptyID(t *testing.T) {
	fe := &fakeExec{}
	_, err := writeOutboxRow(context.Background(), fe, Event{UserID: "u1", Type: "email.received"})
	if err == nil {
		t.Fatalf("expected error on empty ID")
	}
	if !strings.Contains(err.Error(), "non-empty ID") {
		t.Errorf("error should mention ID: %v", err)
	}
	if len(fe.calls) != 0 {
		t.Errorf("Exec should not be called: %v", fe.calls)
	}
}

func TestWriteOutboxRow_RejectsEmptyUserID(t *testing.T) {
	fe := &fakeExec{}
	_, err := writeOutboxRow(context.Background(), fe, Event{ID: "evt_abc", Type: "email.received"})
	if err == nil {
		t.Fatalf("expected error on empty UserID")
	}
}

func TestWriteOutboxRow_RejectsEmptyType(t *testing.T) {
	fe := &fakeExec{}
	_, err := writeOutboxRow(context.Background(), fe, Event{ID: "evt_abc", UserID: "u1"})
	if err == nil {
		t.Fatalf("expected error on empty Type")
	}
}

// TestWriteOutboxRow_PgNotifyFailureIsSoft pins the C2 fix: when
// pg_notify fails (realistically: NOTIFY queue overflow at ~8MB),
// writeOutboxRow MUST log and return nil so the caller's tx commits.
//
// Before the fix the pg_notify error was returned, propagating out of
// PublishTx → out of WithTx → causing the entire trigger transaction
// to roll back. In the relay path that meant the inbound `messages`
// row was lost too: SMTP returned a 4xx to the MTA, MTA retried into
// the same broken state, and inbound mail piled up undelivered while
// the only signal was a "failed to record inbound message" log line.
//
// The INSERT having succeeded is enough for at-least-once: the worker's
// 1-second fallback poll picks up the row regardless of whether the
// NOTIFY fired. The NOTIFY is a latency optimization, not a correctness
// primitive.
func TestWriteOutboxRow_PgNotifyFailureIsSoft(t *testing.T) {
	notifyErr := errors.New("ERROR: too many notifications in the NOTIFY queue (SQLSTATE 54000)")
	fe := &fakeExec{
		errOnContains: "pg_notify",
		errSelective:  notifyErr,
	}
	e := Event{
		ID:     "evt_notify_fail",
		Type:   EventEmailReceived,
		UserID: "u_42",
	}
	if _, err := writeOutboxRow(context.Background(), fe, e); err != nil {
		t.Fatalf("writeOutboxRow should swallow pg_notify failure, got: %v", err)
	}
	// Both Exec calls must have been attempted — the INSERT first, then
	// the (failing) pg_notify. The INSERT must NOT have been skipped.
	if len(fe.calls) != 2 {
		t.Fatalf("expected 2 Exec calls (INSERT + NOTIFY), got %d: %v", len(fe.calls), fe.calls)
	}
	if !strings.Contains(fe.calls[0], "INSERT INTO webhook_events") {
		t.Errorf("first call must be the INSERT: %s", fe.calls[0])
	}
	if !strings.Contains(fe.calls[1], "pg_notify") {
		t.Errorf("second call must be the pg_notify: %s", fe.calls[1])
	}
}

// TestWriteOutboxRow_InsertFailureStillPropagates ensures the soft-
// failure treatment for pg_notify doesn't accidentally swallow INSERT
// errors too. A failed INSERT means the webhook_events row didn't get
// written — at-least-once is broken, so the caller's tx MUST roll
// back. The errSelective fake fails on the INSERT specifically.
func TestWriteOutboxRow_InsertFailureStillPropagates(t *testing.T) {
	insertErr := errors.New("ERROR: duplicate key value violates unique constraint")
	fe := &fakeExec{
		errOnContains: "INSERT INTO webhook_events",
		errSelective:  insertErr,
	}
	e := Event{
		ID:     "evt_insert_fail",
		Type:   EventEmailReceived,
		UserID: "u_42",
	}
	_, err := writeOutboxRow(context.Background(), fe, e)
	if err == nil {
		t.Fatalf("INSERT failure must propagate to caller; got nil error")
	}
	if !strings.Contains(err.Error(), "insert webhook_events") {
		t.Errorf("error should mention insert: %v", err)
	}
	// pg_notify should NOT have been called after the INSERT failed —
	// nothing to notify about.
	if len(fe.calls) != 1 {
		t.Errorf("only INSERT should have been attempted; got %d calls: %v", len(fe.calls), fe.calls)
	}
}

func TestWriteOutboxRow_HappyPath_TwoExecCalls(t *testing.T) {
	// One INSERT + one pg_notify.
	fe := &fakeExec{}
	e := Event{
		ID:             "evt_abc123",
		Type:           EventEmailReceived,
		UserID:         "u_42",
		AgentID:        "agent@example.com",
		ConversationID: "conv_x",
		MessageID:      "msg_y",
		Data:           map[string]any{"hello": "world"},
	}
	if _, err := writeOutboxRow(context.Background(), fe, e); err != nil {
		t.Fatalf("writeOutboxRow err: %v", err)
	}
	if len(fe.calls) != 2 {
		t.Fatalf("expected 2 Exec calls (INSERT + NOTIFY), got %d: %v", len(fe.calls), fe.calls)
	}
	if !strings.Contains(fe.calls[0], "INSERT INTO webhook_events") {
		t.Errorf("first call should be INSERT: %s", fe.calls[0])
	}
	if !strings.Contains(fe.calls[0], "ON CONFLICT (id) DO NOTHING") {
		t.Errorf("INSERT should be idempotent: %s", fe.calls[0])
	}
	if !strings.Contains(fe.calls[1], "pg_notify") {
		t.Errorf("second call should be pg_notify: %s", fe.calls[1])
	}
}

func TestWriteOutboxRow_NilsEmptyOptionals(t *testing.T) {
	// AgentID/ConversationID/MessageID are passed as *string; empty
	// strings should become nil, not "".
	fe := &fakeExec{}
	e := Event{
		ID:     "evt_abc",
		Type:   EventEmailReceived,
		UserID: "u_42",
		// AgentID, ConversationID, MessageID all empty
	}
	if _, err := writeOutboxRow(context.Background(), fe, e); err != nil {
		t.Fatalf("writeOutboxRow err: %v", err)
	}
	if len(fe.args) < 1 || len(fe.args[0]) < 7 {
		t.Fatalf("not enough args captured: %v", fe.args)
	}
	// args order in writeOutboxRow: id, userID, type, envelopeJSON, agentID, conversationID, messageID
	// indices            0,    1,      2,     3,            4,        5,              6
	// args[4..6] are *string for agent_id, conversation_id, message_id.
	// Empty event fields should become typed-nil pointers so pgx
	// passes SQL NULL to the column. Use reflect to dereference the
	// interface and check the underlying pointer is nil.
	for i, name := range []string{"agent_id", "conversation_id", "message_id"} {
		v := reflect.ValueOf(fe.args[0][4+i])
		if v.Kind() != reflect.Ptr {
			t.Errorf("%s arg should be *string, got %s", name, v.Kind())
			continue
		}
		if !v.IsNil() {
			t.Errorf("expected %s = nil, got %v", name, fe.args[0][4+i])
		}
	}
}

func TestOutbox_PublishTx_FlagOffNoOp(t *testing.T) {
	// PublishTx must be a complete no-op when the flag is disabled.
	// Verified by ensuring no panic / error even with a totally
	// invalid event.
	o := &outbox{pool: nil, flag: StaticFlag(false)}
	err := o.PublishTx(context.Background(), nil, Event{}) // no ID, no userID
	if err != nil {
		t.Errorf("flag-off PublishTx should return nil: %v", err)
	}
}

func TestOutbox_PublishBestEffortTx_FlagOffNoOp(t *testing.T) {
	// Best-effort: flag off → complete no-op, no panic.
	o := &outbox{pool: nil, flag: StaticFlag(false)}
	o.PublishBestEffortTx(context.Background(), nil, Event{})
}
