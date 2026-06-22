package httpapi

import (
	"sort"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// TestWebhookEventEnumMatchesCatalog is the drift gate for the webhook event
// vocabulary. The subscribable event-type enum is hand-copied into several Huma
// struct tags (CreateWebhookRequest.events, UpdateWebhookRequest.events, the
// test-webhook event, WebhookDeliveryView.event_type, EventJSON.type, …) with
// nothing keeping them in sync with the canonical webhookpub.AllEventTypes. A new
// event added to the catalog but forgotten in one tag would be silently
// unsubscribable / unfilterable on that surface — the #7 GA-blocker class, which
// TestSpecGoldenNoDrift cannot catch (handler and golden drift together).
//
// This renders the live spec, finds EVERY enum that is an event enum (one that
// contains email.received), and asserts it equals AllEventTypes exactly — so any
// drift fails CI regardless of which copy fell out of sync.
func TestWebhookEventEnumMatchesCatalog(t *testing.T) {
	doc := renderSpec(t)
	want := append([]string(nil), webhookpub.AllEventTypes...)
	sort.Strings(want)

	found := 0
	walkEnums(doc, func(enum []string) {
		if !enumContains(enum, webhookpub.EventEmailReceived) {
			return // not an event enum
		}
		found++
		got := append([]string(nil), enum...)
		sort.Strings(got)
		if !enumEqual(got, want) {
			t.Errorf("an event enum has drifted from webhookpub.AllEventTypes:\n  got:  %v\n  want: %v", got, want)
		}
	})
	if found == 0 {
		t.Fatal("found no event enums in the spec — the spec walk or the marker is wrong")
	}
	// The known copies are EventJSON.type plus the webhook request/response tags.
	// A copy that lost its enum tag (degrading to a free-form string) would drop
	// the count — catch that too.
	if found < 5 {
		t.Errorf("expected at least 5 event-enum copies in the spec, found %d — a copy may have lost its enum tag", found)
	}
}

// walkEnums calls fn with the string values of every `enum` array anywhere in the
// rendered spec (request bodies, response schemas, parameters).
func walkEnums(node any, fn func([]string)) {
	switch n := node.(type) {
	case map[string]any:
		if raw, ok := n["enum"].([]any); ok {
			enum := make([]string, 0, len(raw))
			allStr := true
			for _, v := range raw {
				s, ok := v.(string)
				if !ok {
					allStr = false
					break
				}
				enum = append(enum, s)
			}
			if allStr {
				fn(enum)
			}
		}
		for _, v := range n {
			walkEnums(v, fn)
		}
	case []any:
		for _, v := range n {
			walkEnums(v, fn)
		}
	}
}

func enumContains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func enumEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
