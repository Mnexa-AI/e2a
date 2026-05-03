package relay

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// These tests exercise the contract between the outbound composer and the
// inbound relay: given a message composed by outbound.Compose* with specific
// inputs (conversation_id, reply-to-message-id, etc.), what sender and
// conversation_id would a recipient agent's webhook end up with?
//
// They intentionally bypass SMTP and the DB — compose bytes, parse with
// extractThreadInfo, then apply the same resolution the relay uses via
// resolveConversationID + envelope domain check. That way these tests stay
// honest about the end-to-end shape without requiring a live server.

const (
	relayFromDomain = "send.e2a.dev"
	platformFrom    = "agent@send.e2a.dev"
	gmailMTA        = "mta-1.gmail.com" // stand-in for an external MTA
)

// simulateInbound reproduces the relay's decisions for a single inbound
// message: it parses threading info, gates the X-E2A-Conversation-ID header
// on the envelope domain, and picks the display sender the way deliverToAgent
// does. lookup can be nil to simulate "no prior thread in DB".
func simulateInbound(t *testing.T, raw []byte, envelopeFrom string, lookup func(ctx context.Context, ids []string) (string, error)) (sender, conversationID string) {
	t.Helper()
	info := extractThreadInfo(raw)
	trusted := strings.EqualFold(extractDomain(envelopeFrom), relayFromDomain)
	sender = info.From
	if info.ReplyTo != "" {
		sender = info.ReplyTo
	}
	conversationID = resolveConversationID(context.Background(), info, trusted, lookup)
	return sender, conversationID
}

// composeAgentOutbound mirrors what sender.go does for a real agent send or
// reply: "{display} via e2a" <agent@send.e2a.dev> for From, agent's real
// address for Reply-To.
func composeAgentOutbound(t *testing.T, agentAddr string, to []string, subject, body, replyToMsgID, conversationID string) []byte {
	t.Helper()
	headerFrom := fmt.Sprintf("%q <%s>", agentAddr+" via e2a", platformFrom)
	raw, err := outbound.ComposeMessage(headerFrom, to, nil, subject, body, "text/plain", replyToMsgID, nil, relayFromDomain, agentAddr, conversationID)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	return raw
}

// composeHumanOutbound stands in for a Gmail user sending to an agent —
// no Reply-To, no X-E2A-* headers, envelope from Gmail's MTA.
func composeHumanOutbound(t *testing.T, human, to, subject, body, replyToMsgID string) []byte {
	t.Helper()
	var buf strings.Builder
	fmt.Fprintf(&buf, "From: %s\r\n", human)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	if replyToMsgID != "" {
		fmt.Fprintf(&buf, "In-Reply-To: %s\r\n", replyToMsgID)
		fmt.Fprintf(&buf, "References: %s\r\n", replyToMsgID)
	}
	buf.WriteString("\r\n")
	buf.WriteString(body)
	return []byte(buf.String())
}

// --- Flow 1: Human → Agent → Human --------------------------------------

func TestFlow1_HumanAgentHuman(t *testing.T) {
	const (
		human   = "user@gmail.com"
		agent   = "bot@agent.mnexa.ai"
		convID  = "conv-flow1"
		msg2SES = "<msg2-ses-id@send.e2a.dev>" // SES-assigned ID on the agent's outbound (msg 2)
	)

	// Msg 1: Human → Agent. New thread, no X-E2A header, envelope from Gmail.
	msg1 := composeHumanOutbound(t, human, agent, "Hello agent", "Question for you", "")
	sender1, conv1 := simulateInbound(t, msg1, "bounce@gmail.com", nil /* no prior thread */)
	if sender1 != human {
		t.Errorf("msg 1 sender = %q, want %q", sender1, human)
	}
	if conv1 != "" {
		t.Errorf("msg 1 conv_id = %q, want empty (human has no conv_id concept)", conv1)
	}

	// Agent now picks conversation_id=convID locally and replies.

	// Msg 2: Agent → Human via reply (In-Reply-To + X-E2A header).
	// Human's Gmail just displays it — we don't simulate inbound here. The
	// header presence on outbound is already covered in compose_test.go.
	_ = composeAgentOutbound(t, agent, []string{human}, "Re: Hello agent", "Reply body",
		"<human-msg-id@gmail.com>" /* In-Reply-To */, convID)

	// Msg 3: Human → Agent, replying to msg 2 (Gmail sets In-Reply-To = msg 2's SES id).
	//        Envelope from Gmail, so X-E2A header gate fails; conv_id recovers via lookup.
	msg3 := composeHumanOutbound(t, human, agent, "Re: Hello agent", "Follow-up", msg2SES)
	lookup := func(ctx context.Context, ids []string) (string, error) {
		for _, id := range ids {
			if id == msg2SES {
				return convID, nil // the DB finds msg 2 and returns its conv_id
			}
		}
		return "", fmt.Errorf("not found")
	}
	sender3, conv3 := simulateInbound(t, msg3, "bounce@gmail.com", lookup)
	if sender3 != human {
		t.Errorf("msg 3 sender = %q, want %q", sender3, human)
	}
	if conv3 != convID {
		t.Errorf("msg 3 conv_id = %q, want %q (recovered via In-Reply-To)", conv3, convID)
	}
}

// --- Flow 2: Agent A → Agent B → Agent A --------------------------------

func TestFlow2_AgentAgent(t *testing.T) {
	const (
		alice  = "test-alice@agent.mnexa.ai"
		bob    = "test-bob@agent.mnexa.ai"
		convID = "081158ac-bf25-4eb6-a6b0-02828ec670c3"
	)

	// Msg 1: Alice → Bob via send (new thread, no In-Reply-To).
	msg1 := composeAgentOutbound(t, alice, []string{bob}, "e2e ping", "Hi Bob", "", convID)
	// Envelope from our own relay — header is trusted.
	sender1, conv1 := simulateInbound(t, msg1, platformFrom, nil)
	if sender1 != alice {
		t.Errorf("msg 1 sender = %q, want %q (Reply-To)", sender1, alice)
	}
	if conv1 != convID {
		t.Errorf("msg 1 conv_id = %q, want %q (X-E2A-Conversation-Id, same-platform)", conv1, convID)
	}

	// Msg 2: Bob → Alice via reply — both In-Reply-To and X-E2A header.
	msg2 := composeAgentOutbound(t, bob, []string{alice}, "Re: e2e ping", "Hi Alice",
		"<msg1-ses-id@send.e2a.dev>", convID)
	sender2, conv2 := simulateInbound(t, msg2, platformFrom, nil)
	if sender2 != bob {
		t.Errorf("msg 2 sender = %q, want %q", sender2, bob)
	}
	if conv2 != convID {
		t.Errorf("msg 2 conv_id = %q, want %q", conv2, convID)
	}
}

// --- Flow 3: Human → A → B → Human → B ----------------------------------

func TestFlow3_HumanAgentAgentHumanAgent(t *testing.T) {
	const (
		human   = "user@gmail.com"
		agentA  = "a@agent.mnexa.ai"
		agentB  = "b@agent.mnexa.ai"
		convID  = "conv-flow3"
		msg3SES = "<msg3-ses-id@send.e2a.dev>" // B's outbound to human
	)

	// Msg 1: Human → A. First contact, no headers.
	msg1 := composeHumanOutbound(t, human, agentA, "Please delegate", "Do the thing", "")
	sender1, conv1 := simulateInbound(t, msg1, "bounce@gmail.com", nil)
	if sender1 != human {
		t.Errorf("msg 1 sender = %q, want %q", sender1, human)
	}
	if conv1 != "" {
		t.Errorf("msg 1 conv_id = %q, want empty", conv1)
	}
	// A now assigns convID locally.

	// Msg 2: A → B via send, carrying convID in the header.
	msg2 := composeAgentOutbound(t, agentA, []string{agentB}, "Delegating", "Please handle", "", convID)
	sender2, conv2 := simulateInbound(t, msg2, platformFrom, nil)
	if sender2 != agentA {
		t.Errorf("msg 2 sender = %q, want %q", sender2, agentA)
	}
	if conv2 != convID {
		t.Errorf("msg 2 conv_id = %q, want %q (header, same-platform)", conv2, convID)
	}

	// Msg 3: B → Human via send, same convID in header. Human's Gmail ignores
	// the custom header; header presence is covered in compose_test.go.
	_ = composeAgentOutbound(t, agentB, []string{human}, "Response", "Here you go", "", convID)

	// Msg 4: Human → B, replying to msg 3. Envelope from Gmail; lookup recovers convID.
	msg4 := composeHumanOutbound(t, human, agentB, "Re: Response", "Thanks", msg3SES)
	lookup := func(ctx context.Context, ids []string) (string, error) {
		for _, id := range ids {
			if id == msg3SES {
				return convID, nil
			}
		}
		return "", fmt.Errorf("not found")
	}
	sender4, conv4 := simulateInbound(t, msg4, "bounce@gmail.com", lookup)
	if sender4 != human {
		t.Errorf("msg 4 sender = %q, want %q", sender4, human)
	}
	if conv4 != convID {
		t.Errorf("msg 4 conv_id = %q, want %q (In-Reply-To lookup)", conv4, convID)
	}
}

// --- Spoofing / trust gate ----------------------------------------------

func TestFlow_ExternalSenderCannotForgeConversationID(t *testing.T) {
	// An external sender (envelope from Gmail MTA) sets X-E2A-Conversation-Id
	// trying to inject into an existing thread. The gate must ignore the header.
	raw := []byte("From: attacker@evil.com\r\n" +
		"To: victim@agent.mnexa.ai\r\n" +
		"Subject: pwnd\r\n" +
		"X-E2A-Conversation-Id: victim-private-thread\r\n" +
		"\r\n" +
		"Body\r\n")

	lookup := func(ctx context.Context, ids []string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	// envelope MAIL FROM is external — gate should fail closed.
	sender, conv := simulateInbound(t, raw, "bounce@"+gmailMTA, lookup)
	if sender != "attacker@evil.com" {
		t.Errorf("sender = %q, want attacker@evil.com", sender)
	}
	if conv != "" {
		t.Errorf("conv_id = %q, want empty (forged header must be dropped)", conv)
	}
}
