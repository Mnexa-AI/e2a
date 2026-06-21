package agent

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// TestEventJSONTypeEnumMatchesCatalog is the drift gate for the GET /v1/events response
// schema. eventJSON.Type is a hand-maintained copy of the event-type enum (the OpenAPI
// EventJSON.type schema and the generated SDK EventJSON.TypeEnum both derive from it),
// so it MUST list exactly webhookpub.AllEventTypes — otherwise the events log returns
// rows (e.g. email.injection_detected) that a strict generated client rejects on read.
//
// The httpapi-side TestWebhookEventEnumMatchesCatalog can't reach this unexported type
// (and httpapi→agent is the only import direction), which is exactly how this copy
// drifted past the first drift gate; this co-located test closes that blind spot.
// (v1 surface review #7; the gap was caught by the adversarial review of PR #262.)
func TestEventJSONTypeEnumMatchesCatalog(t *testing.T) {
	canonical := append([]string(nil), webhookpub.AllEventTypes...)
	sort.Strings(canonical)

	f, ok := reflect.TypeOf(eventJSON{}).FieldByName("Type")
	if !ok {
		t.Fatal("eventJSON.Type not found (struct shape changed?)")
	}
	enum := f.Tag.Get("enum")
	if enum == "" {
		t.Fatal("eventJSON.Type has no enum tag (every event field must constrain to the catalog)")
	}
	got := strings.Split(enum, ",")
	sort.Strings(got)
	if !reflect.DeepEqual(got, canonical) {
		t.Errorf("eventJSON.Type enum drifted from webhookpub.AllEventTypes:\n  enum:    %v\n  catalog: %v\n  fix: add the new event to the const block, AllEventTypes, AND every enum tag", got, canonical)
	}
}
