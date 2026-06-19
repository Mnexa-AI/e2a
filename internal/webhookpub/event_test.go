package webhookpub

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestEnvelope_WireKeyIsType locks the webhook delivery discriminator to the
// JSON key `type` (NOT `event`). The SDK `construct_event`/`constructEvent`
// helpers and the /v1/events REST resource (EventJSON.type) both key on
// `type`; a regression to `event` silently breaks every SDK webhook consumer
// (the helper raises "missing a string type"). A struct-field check can't
// catch a tag regression, so assert the raw bytes.
func TestEnvelope_WireKeyIsType(t *testing.T) {
	env := Event{
		ID:        "evt_1",
		Type:      EventEmailReceived,
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
		Data:      map[string]any{"message_id": "msg_1"},
	}.AsEnvelope()

	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	got := string(raw)

	if !strings.Contains(got, `"type":"email.received"`) {
		t.Errorf("envelope must carry the event type under key `type`; got %s", got)
	}
	if strings.Contains(got, `"event":`) {
		t.Errorf("envelope must NOT use the legacy `event` key (breaks SDK construct_event); got %s", got)
	}

	// Round-trip back through the Stripe-style shape a consumer parses.
	var back struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if back.Type != EventEmailReceived || back.ID != "evt_1" {
		t.Errorf("round-trip mismatch: type=%q id=%q", back.Type, back.ID)
	}
}
