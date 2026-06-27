package agent

import (
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// resolveOutboundConversationID is the #328 fix: an outbound send that omits a
// conversation_id must still get a stable thread anchor (and a reply must join
// the thread it answers) so the relay's In-Reply-To lookup has something to
// recover. This pins the precedence: explicit > inherit-on-reply > mint.
func TestResolveOutboundConversationID(t *testing.T) {
	parent := &identity.Message{ConversationID: "conv_parent"}
	firstContact := &identity.Message{ConversationID: ""} // human-initiated inbound has no thread

	// 1. An explicit caller-supplied id always wins.
	if got := resolveOutboundConversationID("conv_explicit", "reply", parent); got != "conv_explicit" {
		t.Errorf("explicit id should win, got %q", got)
	}

	// 2. A reply inherits the referenced message's conversation.
	if got := resolveOutboundConversationID("", "reply", parent); got != "conv_parent" {
		t.Errorf("reply should inherit referenced conv, got %q want conv_parent", got)
	}

	// 2b. A reply to a first-contact inbound (no conv) falls through to a fresh
	// anchor — the agent "stamps its own id" that later follow-ups thread onto.
	if got := resolveOutboundConversationID("", "reply", firstContact); !strings.HasPrefix(got, "conv_") {
		t.Errorf("reply to no-thread inbound should mint, got %q", got)
	}

	// 3. A forward does NOT inherit — it starts a new thread.
	if got := resolveOutboundConversationID("", "forward", parent); got == "conv_parent" || !strings.HasPrefix(got, "conv_") {
		t.Errorf("forward must not inherit referenced conv, got %q", got)
	}

	// 3b. A plain send mints a fresh anchor (the bug was: it stayed empty).
	got1 := resolveOutboundConversationID("", "send", nil)
	got2 := resolveOutboundConversationID("", "send", nil)
	if !strings.HasPrefix(got1, "conv_") || got1 == "" {
		t.Errorf("send should mint a conv_ anchor, got %q", got1)
	}
	if got1 == got2 {
		t.Errorf("each minted anchor should be unique, got %q twice", got1)
	}
}
