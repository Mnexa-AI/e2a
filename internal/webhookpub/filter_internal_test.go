package webhookpub

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// TestMatches covers the filter-matching guardrails (matches/contains/intersects)
// shared by the outbox drain — each axis (agent, conversation, labels) match, miss,
// and event-missing-the-field, plus the empty-filter-matches-all case.
func TestMatches(t *testing.T) {
	tests := []struct {
		name string
		e    Event
		f    identity.WebhookFilters
		want bool
	}{
		{"empty filter matches all", Event{AgentID: "a1"}, identity.WebhookFilters{}, true},
		{"agent match", Event{AgentID: "a1"}, identity.WebhookFilters{AgentIDs: []string{"a1", "a2"}}, true},
		{"agent miss", Event{AgentID: "a3"}, identity.WebhookFilters{AgentIDs: []string{"a1"}}, false},
		{"agent filter, event has none", Event{}, identity.WebhookFilters{AgentIDs: []string{"a1"}}, false},
		{"conversation match", Event{ConversationID: "c1"}, identity.WebhookFilters{ConversationIDs: []string{"c1"}}, true},
		{"conversation miss", Event{ConversationID: "c9"}, identity.WebhookFilters{ConversationIDs: []string{"c1"}}, false},
		{"conversation filter, event has none", Event{}, identity.WebhookFilters{ConversationIDs: []string{"c1"}}, false},
		{"labels intersect", Event{Labels: []string{"x", "y"}}, identity.WebhookFilters{Labels: []string{"y"}}, true},
		{"labels no intersect", Event{Labels: []string{"x"}}, identity.WebhookFilters{Labels: []string{"z"}}, false},
		{"labels filter, event has none", Event{}, identity.WebhookFilters{Labels: []string{"z"}}, false},
		{"all axes match", Event{AgentID: "a1", ConversationID: "c1", Labels: []string{"y"}},
			identity.WebhookFilters{AgentIDs: []string{"a1"}, ConversationIDs: []string{"c1"}, Labels: []string{"y"}}, true},
	}
	for _, tc := range tests {
		if got := matches(tc.e, tc.f); got != tc.want {
			t.Errorf("%s: matches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIsValidEventType(t *testing.T) {
	if !IsValidEventType(EventEmailReceived) {
		t.Errorf("%q should be a valid event type", EventEmailReceived)
	}
	if IsValidEventType("not.a.real.event") {
		t.Error("bogus event type should be invalid")
	}
}
