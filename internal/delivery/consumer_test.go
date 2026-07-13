package delivery

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/eventpayload"
	"github.com/Mnexa-AI/e2a/internal/eventpayload/goldenassert"
)

// fakeConsumerStore is an in-memory delivery.Store.
type fakeConsumerStore struct {
	// correlation: sesMessageID → (messageID, userID, agentID, subject)
	corr map[string][4]string
	// recorded outcomes + suppressions
	outcomes    [][3]string // {messageID, address, status}
	suppressed  map[string]bool
	suppressErr error
	addSuppErr  error
	alreadySupp map[string]bool // (user|address) already suppressed → added=false
}

func newFakeConsumerStore() *fakeConsumerStore {
	return &fakeConsumerStore{corr: map[string][4]string{}, suppressed: map[string]bool{}, alreadySupp: map[string]bool{}}
}

func (f *fakeConsumerStore) CorrelateBySESMessageID(ctx context.Context, id string) (string, string, string, string, bool, error) {
	v, ok := f.corr[id]
	return v[0], v[1], v[2], v[3], ok, nil
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
	data                       any
	dedupKey                   string
}

func recordingFirer() (Firer, *[]firedEvent) {
	var events []firedEvent
	f := func(ctx context.Context, userID, agentID, eventType string, data any, dedupKey string) {
		events = append(events, firedEvent{userID, agentID, eventType, data, dedupKey})
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
		store.corr["ses-1"] = [4]string{"msg_1", "u_1", "bot@x.com", "hi"}
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
		data, ok := e.data.(eventpayload.EmailDeliveredData)
		if !ok {
			t.Fatalf("data is not the canonical typed payload: %T", e.data)
		}
		if data.Subject != "hi" {
			t.Errorf("subject = %q, want the correlated message subject", data.Subject)
		}
		if data.Direction != "outbound" || data.DeliveredTo != "a@x.com" {
			t.Errorf("data=%+v", data)
		}
	})

	t.Run("hard bounce records + fires bounced + suppresses + fires suppression", func(t *testing.T) {
		store := newFakeConsumerStore()
		store.corr["ses-2"] = [4]string{"msg_2", "u_2", "bot@x.com", ""}
		fire, events := recordingFirer()
		c := NewConsumer(store, fire)
		_ = c.Process(context.Background(), &Event{
			Kind: KindBounce, SESMessageID: "ses-2",
			BounceType: "permanent", BounceSubType: "General",
			Recipients: []RecipientOutcome{{Address: "b@x.com", Status: StatusBounced, Detail: "550", Suppress: true}},
		})
		if !store.suppressed["u_2|b@x.com"] {
			t.Fatal("address should be suppressed")
		}
		var types []string
		for _, e := range *events {
			types = append(types, e.eventType)
			if e.eventType == EventEmailBounced {
				data, ok := e.data.(eventpayload.EmailBouncedData)
				if !ok {
					t.Fatalf("bounced data is not typed: %T", e.data)
				}
				if data.BounceType != "permanent" || data.BounceSubType != "General" {
					t.Errorf("bounce classification not wired through: %+v", data)
				}
			}
		}
		if !contains(types, EventEmailBounced) || !contains(types, EventSuppressionAdded) {
			t.Fatalf("expected bounced + suppression_added, got %v", types)
		}
	})

	t.Run("bounce without a classification defaults to undetermined", func(t *testing.T) {
		store := newFakeConsumerStore()
		store.corr["ses-2b"] = [4]string{"msg_2b", "u_2", "bot@x.com", ""}
		fire, events := recordingFirer()
		c := NewConsumer(store, fire)
		_ = c.Process(context.Background(), &Event{
			Kind: KindBounce, SESMessageID: "ses-2b",
			Recipients: []RecipientOutcome{{Address: "b@x.com", Status: StatusBounced}},
		})
		data := (*events)[0].data.(eventpayload.EmailBouncedData)
		if data.BounceType != "undetermined" {
			t.Errorf("bounce_type = %q, want undetermined (required field)", data.BounceType)
		}
	})

	t.Run("complaint suppresses with no agent id on the suppression event", func(t *testing.T) {
		store := newFakeConsumerStore()
		store.corr["ses-3"] = [4]string{"msg_3", "u_3", "bot@x.com", ""}
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
		store.corr["ses-4"] = [4]string{"msg_4", "u_4", "bot@x.com", ""}
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

// TestConsumerGoldenPayloads is this package's side of the cross-channel
// drift lock: the consumer's built payloads for the canonical inputs must
// marshal byte-identical to the committed golden fixtures — the same files
// the eventpayload envelope test and the TS/Python SDK tests assert against.
func TestConsumerGoldenPayloads(t *testing.T) {
	const (
		msgID   = "msg_01h2xcejqtf2nbrexx3vqjhp44"
		userID  = "user_7a6b5c4d"
		agent   = "support@agents.example.com"
		subject = "Re: Order #1234 delayed"
		fixture = "../eventpayload/testdata/"
	)

	fireGolden := func(sesEvent *Event) *[]firedEvent {
		t.Helper()
		store := newFakeConsumerStore()
		store.corr["ses-golden"] = [4]string{msgID, userID, agent, subject}
		fire, events := recordingFirer()
		c := NewConsumer(store, fire)
		if err := c.Process(context.Background(), sesEvent); err != nil {
			t.Fatal(err)
		}
		return events
	}

	t.Run("email.delivered", func(t *testing.T) {
		events := fireGolden(&Event{
			Kind: KindDelivery, SESMessageID: "ses-golden",
			Recipients: []RecipientOutcome{{Address: "alice@customer.example.com", Status: StatusDelivered}},
		})
		goldenassert.Data(t, fixture+"email.delivered.json", (*events)[0].data)
	})

	t.Run("email.bounced + domain.suppression_added", func(t *testing.T) {
		events := fireGolden(&Event{
			Kind: KindBounce, SESMessageID: "ses-golden",
			BounceType: "permanent", BounceSubType: "General",
			Recipients: []RecipientOutcome{{
				Address: "bob@customer.example.com", Status: StatusBounced,
				Detail: "550 5.1.1 no such user", Suppress: true,
			}},
		})
		if len(*events) != 2 {
			t.Fatalf("expected bounced + suppression, got %v", *events)
		}
		goldenassert.Data(t, fixture+"email.bounced.json", (*events)[0].data)
		goldenassert.Data(t, fixture+"domain.suppression_added.json", (*events)[1].data)
	})

	t.Run("email.complained", func(t *testing.T) {
		events := fireGolden(&Event{
			Kind: KindComplaint, SESMessageID: "ses-golden",
			Recipients: []RecipientOutcome{{Address: "carol@customer.example.com", Status: StatusComplained, Detail: "abuse"}},
		})
		goldenassert.Data(t, fixture+"email.complained.json", (*events)[0].data)
	})

	// Minimal (required-fields-only) variants: SES feedback with no subject
	// correlation, no diagnostic Detail, and (for bounces) no sub-type must
	// byte-match the .min.json fixtures — locking that the optional
	// subject/smtp_detail/bounce_sub_type fields are ABSENT from the wire when
	// unset, which the fully-populated fixtures above can't detect.
	fireMinimal := func(sesEvent *Event) *[]firedEvent {
		t.Helper()
		store := newFakeConsumerStore()
		store.corr["ses-golden-min"] = [4]string{msgID, userID, agent, ""} // no subject
		fire, events := recordingFirer()
		c := NewConsumer(store, fire)
		if err := c.Process(context.Background(), sesEvent); err != nil {
			t.Fatal(err)
		}
		return events
	}

	t.Run("email.delivered minimal", func(t *testing.T) {
		events := fireMinimal(&Event{
			Kind: KindDelivery, SESMessageID: "ses-golden-min",
			Recipients: []RecipientOutcome{{Address: "alice@customer.example.com", Status: StatusDelivered}},
		})
		goldenassert.Data(t, fixture+"email.delivered.min.json", (*events)[0].data)
	})

	t.Run("email.bounced minimal", func(t *testing.T) {
		events := fireMinimal(&Event{
			Kind: KindBounce, SESMessageID: "ses-golden-min",
			BounceType: "permanent",
			Recipients: []RecipientOutcome{{Address: "bob@customer.example.com", Status: StatusBounced}},
		})
		goldenassert.Data(t, fixture+"email.bounced.min.json", (*events)[0].data)
	})

	t.Run("email.complained minimal", func(t *testing.T) {
		events := fireMinimal(&Event{
			Kind: KindComplaint, SESMessageID: "ses-golden-min",
			Recipients: []RecipientOutcome{{Address: "carol@customer.example.com", Status: StatusComplained}},
		})
		goldenassert.Data(t, fixture+"email.complained.min.json", (*events)[0].data)
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
