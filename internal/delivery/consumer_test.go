package delivery

import (
	"context"
	"testing"
)

// fakeConsumerStore is an in-memory delivery.Store.
type fakeConsumerStore struct {
	// correlation: sesMessageID → (messageID, userID, agentID)
	corr map[string][3]string
	// recorded outcomes + suppressions
	outcomes    [][3]string // {messageID, address, status}
	suppressed  map[string]bool
	suppressErr error
	addSuppErr  error
	alreadySupp map[string]bool // (user|address) already suppressed → added=false
}

func newFakeConsumerStore() *fakeConsumerStore {
	return &fakeConsumerStore{corr: map[string][3]string{}, suppressed: map[string]bool{}, alreadySupp: map[string]bool{}}
}

func (f *fakeConsumerStore) CorrelateBySESMessageID(ctx context.Context, id string) (string, string, string, bool, error) {
	v, ok := f.corr[id]
	return v[0], v[1], v[2], ok, nil
}
func (f *fakeConsumerStore) RecordDeliveryOutcome(ctx context.Context, messageID, address string, st Status, detail string) error {
	f.outcomes = append(f.outcomes, [3]string{messageID, address, string(st)})
	return nil
}
func (f *fakeConsumerStore) AddSuppression(ctx context.Context, userID, address, reason, source, srcMsg string) (bool, error) {
	if f.addSuppErr != nil {
		return false, f.addSuppErr
	}
	key := userID + "|" + address
	if f.alreadySupp[key] {
		return false, nil
	}
	f.suppressed[key] = true
	return true, nil
}

type firedEvent struct {
	userID, agentID, eventType string
	data                       map[string]any
}

func recordingFirer() (Firer, *[]firedEvent) {
	var events []firedEvent
	f := func(ctx context.Context, userID, agentID, eventType string, data map[string]any, dedupKey string) {
		events = append(events, firedEvent{userID, agentID, eventType, data})
	}
	return f, &events
}

func TestConsumerProcess(t *testing.T) {
	t.Run("uncorrelated message is a no-op ack", func(t *testing.T) {
		store := newFakeConsumerStore()
		fire, events := recordingFirer()
		c := NewConsumer(store, fire)
		err := c.Process(context.Background(), &Event{
			Kind: KindDelivery, SESMessageID: "unknown",
			Recipients: []RecipientOutcome{{Address: "a@x.com", Status: StatusDelivered}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(store.outcomes) != 0 || len(*events) != 0 {
			t.Fatal("nothing should be recorded for an uncorrelated message")
		}
	})

	t.Run("delivery records outcome + fires email.delivered with agent id", func(t *testing.T) {
		store := newFakeConsumerStore()
		store.corr["ses-1"] = [3]string{"msg_1", "u_1", "bot@x.com"}
		fire, events := recordingFirer()
		c := NewConsumer(store, fire)
		err := c.Process(context.Background(), &Event{
			Kind: KindDelivery, SESMessageID: "ses-1",
			Recipients: []RecipientOutcome{{Address: "a@x.com", Status: StatusDelivered}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(store.outcomes) != 1 || store.outcomes[0] != [3]string{"msg_1", "a@x.com", "delivered"} {
			t.Fatalf("outcomes=%v", store.outcomes)
		}
		if len(*events) != 1 {
			t.Fatalf("events=%v", *events)
		}
		e := (*events)[0]
		if e.eventType != EventEmailDelivered || e.userID != "u_1" || e.agentID != "bot@x.com" {
			t.Fatalf("event=%+v", e)
		}
	})

	t.Run("hard bounce records + fires bounced + suppresses + fires suppression", func(t *testing.T) {
		store := newFakeConsumerStore()
		store.corr["ses-2"] = [3]string{"msg_2", "u_2", "bot@x.com"}
		fire, events := recordingFirer()
		c := NewConsumer(store, fire)
		_ = c.Process(context.Background(), &Event{
			Kind: KindBounce, SESMessageID: "ses-2",
			Recipients: []RecipientOutcome{{Address: "b@x.com", Status: StatusBounced, Detail: "550", Suppress: true}},
		})
		if !store.suppressed["u_2|b@x.com"] {
			t.Fatal("address should be suppressed")
		}
		var types []string
		for _, e := range *events {
			types = append(types, e.eventType)
		}
		if !contains(types, EventEmailBounced) || !contains(types, EventSuppressionAdded) {
			t.Fatalf("expected bounced + suppression_added, got %v", types)
		}
	})

	t.Run("complaint suppresses with no agent id on the suppression event", func(t *testing.T) {
		store := newFakeConsumerStore()
		store.corr["ses-3"] = [3]string{"msg_3", "u_3", "bot@x.com"}
		fire, events := recordingFirer()
		c := NewConsumer(store, fire)
		_ = c.Process(context.Background(), &Event{
			Kind: KindComplaint, SESMessageID: "ses-3",
			Recipients: []RecipientOutcome{{Address: "c@x.com", Status: StatusComplained, Suppress: true}},
		})
		for _, e := range *events {
			if e.eventType == EventSuppressionAdded && e.agentID != "" {
				t.Errorf("suppression event is account-scoped; agentID should be empty, got %q", e.agentID)
			}
		}
	})

	t.Run("re-suppression fires the event at most once", func(t *testing.T) {
		store := newFakeConsumerStore()
		store.corr["ses-4"] = [3]string{"msg_4", "u_4", "bot@x.com"}
		store.alreadySupp["u_4|d@x.com"] = true // already on the list
		fire, events := recordingFirer()
		c := NewConsumer(store, fire)
		_ = c.Process(context.Background(), &Event{
			Kind: KindComplaint, SESMessageID: "ses-4",
			Recipients: []RecipientOutcome{{Address: "d@x.com", Status: StatusComplained, Suppress: true}},
		})
		for _, e := range *events {
			if e.eventType == EventSuppressionAdded {
				t.Error("suppression_added must not fire when the address was already suppressed")
			}
		}
	})
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
