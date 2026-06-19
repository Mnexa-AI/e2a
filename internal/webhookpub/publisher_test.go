package webhookpub

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// fakeStore is the minimal store interface the publisher needs. We
// keep it in a test file so production code stays import-cycle-free
// and tests don't drag in pgx.
type fakeStore struct {
	webhooks []identity.Webhook
	listErr  error
}

func (f *fakeStore) ListEnabledWebhooksForRouting(ctx context.Context, userID, eventType string) ([]identity.Webhook, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []identity.Webhook
	for _, w := range f.webhooks {
		if w.UserID != userID {
			continue
		}
		if !w.Enabled {
			continue
		}
		for _, e := range w.Events {
			if e == eventType {
				out = append(out, w)
				break
			}
		}
	}
	return out, nil
}

// fakeInserter captures the InsertPending calls so tests can assert
// which webhook IDs got delivery rows.
type fakeInserter struct {
	mu        sync.Mutex
	inserted  []insertCall
	insertErr error
}

type insertCall struct {
	webhookID string
	eventType string
	messageID string
	envelope  []byte
}

func (f *fakeInserter) InsertPending(ctx context.Context, webhookID, eventType, messageID string, envelope []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, insertCall{
		webhookID: webhookID,
		eventType: eventType,
		messageID: messageID,
		envelope:  append([]byte(nil), envelope...),
	})
	return nil
}

func (f *fakeInserter) IDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.inserted))
	for _, c := range f.inserted {
		out = append(out, c.webhookID)
	}
	sort.Strings(out)
	return out
}

func newTestPublisher(webhooks []identity.Webhook) (Publisher, *fakeInserter) {
	store := &fakeStore{webhooks: webhooks}
	inserter := &fakeInserter{}
	pub := New(store, inserter, StaticFlag(true))
	return pub, inserter
}

func receivedEvent(userID, agentID, conversationID string, labels []string) Event {
	return Event{
		ID:             "evt_test",
		Type:           EventEmailReceived,
		CreatedAt:      time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC),
		UserID:         userID,
		AgentID:        agentID,
		ConversationID: conversationID,
		Labels:         labels,
		Data:           map[string]any{"hello": "world"},
	}
}

func TestPublisher_NoFiltersMatchesAll(t *testing.T) {
	w := identity.Webhook{ID: "wh_1", UserID: "u", Events: []string{EventEmailReceived}, Enabled: true}
	pub, ins := newTestPublisher([]identity.Webhook{w})

	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "conv-1", []string{"urgent"}))

	if got := ins.IDs(); len(got) != 1 || got[0] != "wh_1" {
		t.Errorf("matched = %v, want [wh_1]", got)
	}
}

func TestPublisher_FeatureFlagDisablesPublish(t *testing.T) {
	w := identity.Webhook{ID: "wh_1", UserID: "u", Events: []string{EventEmailReceived}, Enabled: true}
	pub := New(&fakeStore{webhooks: []identity.Webhook{w}}, &fakeInserter{}, StaticFlag(false))

	// Publish should be a no-op when the flag is off.
	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "", nil))
	// Nothing observable from this test other than no panic — the
	// fakeInserter never receives a call. We re-check by passing a
	// fresh inserter and asserting it stays empty.
	ins := &fakeInserter{}
	pub2 := New(&fakeStore{webhooks: []identity.Webhook{w}}, ins, StaticFlag(false))
	pub2.Publish(context.Background(), receivedEvent("u", "bot@x", "", nil))
	if got := ins.IDs(); len(got) != 0 {
		t.Errorf("flag-off published %d deliveries; want 0", len(got))
	}
}

func TestPublisher_DisabledWebhookSkipped(t *testing.T) {
	// fakeStore already filters by Enabled; this asserts the
	// production-shaped query (ListEnabledWebhooksForRouting only
	// returns enabled rows) is what the publisher relies on.
	w := identity.Webhook{ID: "wh_1", UserID: "u", Events: []string{EventEmailReceived}, Enabled: false}
	pub, ins := newTestPublisher([]identity.Webhook{w})
	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "", nil))
	if got := ins.IDs(); len(got) != 0 {
		t.Errorf("disabled webhook published %d times; want 0", len(got))
	}
}

func TestPublisher_AgentIDFilter(t *testing.T) {
	w := identity.Webhook{
		ID:      "wh_1",
		UserID:  "u",
		Events:  []string{EventEmailReceived},
		Enabled: true,
		Filters: identity.WebhookFilters{AgentIDs: []string{"bot@x"}},
	}
	pub, ins := newTestPublisher([]identity.Webhook{w})

	// Matching agent_id → delivered.
	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "", nil))
	if got := ins.IDs(); len(got) != 1 {
		t.Errorf("matching agent_id delivered %d times; want 1", len(got))
	}

	// Non-matching agent_id → skipped.
	pub2, ins2 := newTestPublisher([]identity.Webhook{w})
	pub2.Publish(context.Background(), receivedEvent("u", "other@x", "", nil))
	if got := ins2.IDs(); len(got) != 0 {
		t.Errorf("non-matching agent_id delivered %d times; want 0", len(got))
	}
}

func TestPublisher_LabelFilter_NullEmptyEventSemantics(t *testing.T) {
	// H5 from the design review: when filters.labels is non-empty
	// and event.labels is nil/empty, the webhook MUST skip. Same for
	// conversation_id and agent_id.
	w := identity.Webhook{
		ID:      "wh_1",
		UserID:  "u",
		Events:  []string{EventEmailReceived},
		Enabled: true,
		Filters: identity.WebhookFilters{Labels: []string{"urgent"}},
	}

	// Event with no labels (nil) — must skip.
	pub, ins := newTestPublisher([]identity.Webhook{w})
	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "", nil))
	if got := ins.IDs(); len(got) != 0 {
		t.Errorf("nil labels delivered %d; want 0 (H5)", len(got))
	}

	// Event with empty labels slice — must skip.
	pub2, ins2 := newTestPublisher([]identity.Webhook{w})
	pub2.Publish(context.Background(), receivedEvent("u", "bot@x", "", []string{}))
	if got := ins2.IDs(); len(got) != 0 {
		t.Errorf("empty labels delivered %d; want 0 (H5)", len(got))
	}

	// Event with overlapping label — match.
	pub3, ins3 := newTestPublisher([]identity.Webhook{w})
	pub3.Publish(context.Background(), receivedEvent("u", "bot@x", "", []string{"urgent", "other"}))
	if got := ins3.IDs(); len(got) != 1 {
		t.Errorf("overlapping label delivered %d; want 1", len(got))
	}

	// Event with non-overlapping label — skip.
	pub4, ins4 := newTestPublisher([]identity.Webhook{w})
	pub4.Publish(context.Background(), receivedEvent("u", "bot@x", "", []string{"other"}))
	if got := ins4.IDs(); len(got) != 0 {
		t.Errorf("non-overlapping label delivered %d; want 0", len(got))
	}
}

func TestPublisher_ConversationIDFilter_NullEmptySemantics(t *testing.T) {
	// Same H5 rule applied to conversation_id.
	w := identity.Webhook{
		ID:      "wh_1",
		UserID:  "u",
		Events:  []string{EventEmailReceived},
		Enabled: true,
		Filters: identity.WebhookFilters{ConversationIDs: []string{"conv-X"}},
	}

	// Empty event conversation_id — skip.
	pub, ins := newTestPublisher([]identity.Webhook{w})
	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "", nil))
	if got := ins.IDs(); len(got) != 0 {
		t.Errorf("empty conversation_id delivered %d; want 0 (H5)", len(got))
	}

	// Matching — deliver.
	pub2, ins2 := newTestPublisher([]identity.Webhook{w})
	pub2.Publish(context.Background(), receivedEvent("u", "bot@x", "conv-X", nil))
	if got := ins2.IDs(); len(got) != 1 {
		t.Errorf("matching conversation_id delivered %d; want 1", len(got))
	}
}

func TestPublisher_ANDAcrossFilterTypes(t *testing.T) {
	// AND across types: agent_id must match AND labels must
	// intersect.
	w := identity.Webhook{
		ID:      "wh_1",
		UserID:  "u",
		Events:  []string{EventEmailReceived},
		Enabled: true,
		Filters: identity.WebhookFilters{
			AgentIDs: []string{"bot@x"},
			Labels:   []string{"urgent"},
		},
	}

	// Match both → deliver.
	pub, ins := newTestPublisher([]identity.Webhook{w})
	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "", []string{"urgent"}))
	if got := ins.IDs(); len(got) != 1 {
		t.Errorf("agent+label match: delivered %d; want 1", len(got))
	}

	// Match agent only, miss label → skip.
	pub2, ins2 := newTestPublisher([]identity.Webhook{w})
	pub2.Publish(context.Background(), receivedEvent("u", "bot@x", "", []string{"other"}))
	if got := ins2.IDs(); len(got) != 0 {
		t.Errorf("agent-only match: delivered %d; want 0 (AND violated)", len(got))
	}

	// Miss agent, match label → skip.
	pub3, ins3 := newTestPublisher([]identity.Webhook{w})
	pub3.Publish(context.Background(), receivedEvent("u", "other@x", "", []string{"urgent"}))
	if got := ins3.IDs(); len(got) != 0 {
		t.Errorf("label-only match: delivered %d; want 0 (AND violated)", len(got))
	}
}

func TestPublisher_ORWithinFilterType(t *testing.T) {
	// Multiple values within a single filter type are OR-matched:
	// agent_id = {A, B} matches either A or B.
	w := identity.Webhook{
		ID:      "wh_1",
		UserID:  "u",
		Events:  []string{EventEmailReceived},
		Enabled: true,
		Filters: identity.WebhookFilters{AgentIDs: []string{"a@x", "b@x"}},
	}

	pub, ins := newTestPublisher([]identity.Webhook{w})
	pub.Publish(context.Background(), receivedEvent("u", "a@x", "", nil))
	pub.Publish(context.Background(), receivedEvent("u", "b@x", "", nil))
	pub.Publish(context.Background(), receivedEvent("u", "c@x", "", nil))

	// Two of three events should have produced deliveries.
	if got := ins.IDs(); len(got) != 2 {
		t.Errorf("OR-within-type delivered %d; want 2", len(got))
	}
}

func TestPublisher_FansOutToMultipleSubscribers(t *testing.T) {
	w1 := identity.Webhook{ID: "wh_1", UserID: "u", Events: []string{EventEmailReceived}, Enabled: true}
	w2 := identity.Webhook{ID: "wh_2", UserID: "u", Events: []string{EventEmailReceived}, Enabled: true}
	w3 := identity.Webhook{ID: "wh_3", UserID: "u", Events: []string{EventEmailReceived}, Enabled: true}
	pub, ins := newTestPublisher([]identity.Webhook{w1, w2, w3})

	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "", nil))

	got := ins.IDs()
	want := []string{"wh_1", "wh_2", "wh_3"}
	if len(got) != 3 {
		t.Errorf("fan-out delivered to %d subscribers; want 3", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("subscriber %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPublisher_EnvelopeShape(t *testing.T) {
	w := identity.Webhook{ID: "wh_1", UserID: "u", Events: []string{EventEmailReceived}, Enabled: true}
	store := &fakeStore{webhooks: []identity.Webhook{w}}
	ins := &fakeInserter{}
	pub := New(store, ins, StaticFlag(true))

	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "", nil))

	if len(ins.inserted) != 1 {
		t.Fatalf("inserted %d rows; want 1", len(ins.inserted))
	}
	var env Envelope
	if err := json.Unmarshal(ins.inserted[0].envelope, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Type != EventEmailReceived {
		t.Errorf("envelope.type = %q, want %q", env.Type, EventEmailReceived)
	}
	if env.ID == "" {
		t.Error("envelope.id is empty")
	}
	if env.CreatedAt.IsZero() {
		t.Error("envelope.created_at is zero")
	}
}

func TestPublisher_SwallowsStoreError(t *testing.T) {
	// Publisher must not panic or block on store error — log + drop
	// is the right behavior (the trigger's primary state change has
	// already committed).
	store := &fakeStore{listErr: errors.New("simulated DB error")}
	ins := &fakeInserter{}
	pub := New(store, ins, StaticFlag(true))

	// Should not panic.
	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "", nil))
	if got := ins.IDs(); len(got) != 0 {
		t.Errorf("store error produced %d deliveries; want 0", len(got))
	}
}

func TestPublisher_SwallowsInsertError(t *testing.T) {
	w1 := identity.Webhook{ID: "wh_1", UserID: "u", Events: []string{EventEmailReceived}, Enabled: true}
	w2 := identity.Webhook{ID: "wh_2", UserID: "u", Events: []string{EventEmailReceived}, Enabled: true}
	store := &fakeStore{webhooks: []identity.Webhook{w1, w2}}
	// Inserter that fails every call: publisher should log+continue
	// per webhook, never panic.
	ins := &fakeInserter{insertErr: errors.New("simulated DB error")}
	pub := New(store, ins, StaticFlag(true))

	pub.Publish(context.Background(), receivedEvent("u", "bot@x", "", nil))
}
