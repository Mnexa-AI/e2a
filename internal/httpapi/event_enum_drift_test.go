package httpapi

import (
	"sort"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// TestWebhookEventEnumMatchesCatalog is the drift gate for the webhook event
// vocabulary. The subscribable event-type enum is hand-copied into the REQUEST
// Huma struct tags (CreateWebhookRequest.events, UpdateWebhookRequest.events, the
// test-webhook event) with nothing keeping them in sync with the canonical
// webhookpub.AllEventTypes. A new event added to the catalog but forgotten in one
// tag would be silently unsubscribable / unfilterable on that surface — the #7
// GA-blocker class, which TestSpecGoldenNoDrift cannot catch (handler and golden
// drift together).
//
// RESPONSE-side event fields (WebhookView.events, WebhookDeliveryView.event_type,
// EventJSON.type) are deliberately NOT closed enums — they are open strings so the
// server can emit a newly-added event type without breaking strict spec-generated
// clients (the GA stability contract; see docs/api.md "Versioning & stability").
// So this gate governs only the REQUEST-side copies, where a closed enum is
// correct: we validate and reject an unknown event type a client tries to
// subscribe to or test with.
//
// This renders the live spec, finds EVERY enum that is an event enum (one that
// contains email.received), and asserts it equals AllEventTypes exactly — so any
// drift in a request-side copy fails CI regardless of which copy fell out of sync.
func TestWebhookEventEnumMatchesCatalog(t *testing.T) {
	doc := renderSpec(t)
	want := append([]string(nil), webhookpub.AllEventTypes...)
	sort.Strings(want)

	found := 0
	walkEnums(doc, func(enum []string) {
		// An event enum is any enum that touches the event namespace — ANY value
		// looking like email.* / domain.*. Membership-based (not a single marker
		// value) so a copy that drops a value, adds a bogus one, or typos a value
		// still trips the gate. Every such enum must equal the full catalog.
		if !looksLikeEventEnum(enum) {
			return
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
	// The remaining closed copies are the 3 REQUEST-side tags:
	// CreateWebhookRequest.events, UpdateWebhookRequest.events, and the
	// test-webhook event. A request-side copy that lost its enum tag (degrading
	// to a free-form string that no longer validates subscriptions) would drop
	// the count — catch that too. (Response-side copies are intentionally open and
	// must NOT be counted here; if you add a new request surface that subscribes
	// to / filters by event type, bump this.)
	if found < 3 {
		t.Errorf("expected at least 3 request-side event-enum copies in the spec, found %d — a request copy may have lost its enum tag", found)
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

// looksLikeEventEnum reports whether an enum touches the webhook event namespace
// (any value prefixed email. / domain.). Used to identify event enums by
// membership rather than a single marker value, so the gate can't be evaded by a
// copy that omits one particular value.
func looksLikeEventEnum(enum []string) bool {
	for _, v := range enum {
		if strings.HasPrefix(v, "email.") || strings.HasPrefix(v, "domain.") {
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
