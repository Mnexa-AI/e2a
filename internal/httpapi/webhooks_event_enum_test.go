package httpapi

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// TestWebhookEventEnumMatchesCatalog is the drift gate the v1 surface review (#7)
// called for. Every Huma `enum:"…"` tag on a webhook/event field MUST list exactly
// the canonical webhookpub.AllEventTypes.
//
// Before this gate the tags were five hand-maintained copies that silently omitted
// email.injection_detected — so Huma 422'd a subscribe to it (the enum is validated
// from the struct tag, before the handler's correct webhookpub.IsValidEventType
// runs), stranding the screening framework's headline alert. Keeping the tags == the
// catalog is what makes the subscribable surface (spec → SDK → MCP) track the events
// the core actually emits, and makes "add a new event" a one-place change that fails
// loudly here if any copy is missed.
func TestWebhookEventEnumMatchesCatalog(t *testing.T) {
	canonical := append([]string(nil), webhookpub.AllEventTypes...)
	sort.Strings(canonical)

	// Every (struct, field) that carries the event-type enum on the /v1 webhook +
	// event surface. A new enum-bearing field must be added here too.
	cases := []struct {
		typ   reflect.Type
		field string
	}{
		{reflect.TypeOf(WebhookView{}), "Events"},
		{reflect.TypeOf(CreateWebhookRequest{}), "Events"},
		{reflect.TypeOf(UpdateWebhookRequest{}), "Events"},
		{reflect.TypeOf(TestWebhookRequest{}), "Event"},
		{reflect.TypeOf(WebhookDeliveryView{}), "EventType"},
	}
	for _, c := range cases {
		name := c.typ.Name() + "." + c.field
		f, ok := c.typ.FieldByName(c.field)
		if !ok {
			t.Fatalf("%s: field not found (struct shape changed?)", name)
		}
		enum := f.Tag.Get("enum")
		if enum == "" {
			t.Fatalf("%s: no enum tag (every event field must constrain to the catalog)", name)
		}
		got := strings.Split(enum, ",")
		sort.Strings(got)
		if !reflect.DeepEqual(got, canonical) {
			t.Errorf("%s enum drifted from webhookpub.AllEventTypes:\n  enum tag: %v\n  catalog:  %v\n  fix: add the new event to the const block, AllEventTypes, AND every enum tag",
				name, got, canonical)
		}
	}
}
