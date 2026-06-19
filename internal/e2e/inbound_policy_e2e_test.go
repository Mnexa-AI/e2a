//go:build integration

package e2e_test

import (
	"context"
	"net/smtp"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Slice 7a — inbound trust policy ingestion gate (api-v1-redesign decision 10).
//
// The relay evaluates the agent's inbound_policy on arrival. A non-matching
// message is FLAGGED — still delivered (email.received still fires, the row is
// persisted) — and additionally emits email.flagged so operators get a signal.
// Nothing is dropped. These e2e tests drive a real SMTP delivery through the
// relay and assert both the persisted flag and the emitted events.

// flaggedFixture wires a verified agent with the given ingestion policy and a
// subscriber listening for both email.received and email.flagged. Returns the
// pool (to read the persisted row), the agent, and the capturing receiver.
func setupFlaggedAgent(t *testing.T, policy string, allowlist []string, email, domain string) (*testutil.E2ATestServer, *pgxpool.Pool, *identity.AgentIdentity, *testutil.SubscriberReceiverResult) {
	t.Helper()
	pool := testutil.TestDB(t)
	receiver := testutil.SubscriberReceiver(t)
	ts := testutil.TestServer(t, pool)

	user, _, agent := setupDomainAndAgent(t, ts, email, domain, "", "")
	if err := ts.Store.UpdateAgentInboundPolicy(context.Background(), agent.ID, user.ID, policy, allowlist); err != nil {
		t.Fatalf("UpdateAgentInboundPolicy: %v", err)
	}
	registerWebhook(t, ts, user.ID, receiver.Server.URL+"/received",
		[]string{"email.received", "email.flagged"}, identity.WebhookFilters{})
	return ts, pool, agent, receiver
}

func eventTypes(caps []testutil.SubscriberCaptured) map[string]int {
	out := map[string]int{}
	for _, c := range caps {
		if et, ok := c.Envelope["type"].(string); ok {
			out[et]++
		}
	}
	return out
}

// eventData returns the data block of the first captured envelope of the given
// type, or nil if none was captured.
func eventData(caps []testutil.SubscriberCaptured, eventType string) map[string]any {
	for _, c := range caps {
		if et, _ := c.Envelope["type"].(string); et == eventType {
			d, _ := c.Envelope["data"].(map[string]any)
			return d
		}
	}
	return nil
}

func readFlagged(t *testing.T, pool *pgxpool.Pool, agentID string) (bool, string) {
	t.Helper()
	var flagged bool
	var reason string
	err := pool.QueryRow(context.Background(),
		`SELECT flagged, COALESCE(flag_reason, '') FROM messages
		 WHERE agent_id = $1 AND direction = 'inbound' ORDER BY created_at DESC LIMIT 1`,
		agentID,
	).Scan(&flagged, &reason)
	if err != nil {
		t.Fatalf("read flagged: %v", err)
	}
	return flagged, reason
}

// TestInboundPolicy_AllowlistFlagsNonMember: an allowlist-policy agent receives
// mail from a sender NOT on the list — the message is delivered AND flagged,
// and email.flagged fires alongside email.received.
func TestInboundPolicy_AllowlistFlagsNonMember(t *testing.T) {
	ts, pool, agent, receiver := setupFlaggedAgent(t, "allowlist",
		[]string{"friend@trusted.com"}, "agent@allow.example.com", "allow.example.com")

	msg := "From: stranger@evil.com\r\nTo: agent@allow.example.com\r\nSubject: Hi\r\n\r\nHello"
	if err := smtp.SendMail(ts.SMTPAddr, nil, "stranger@evil.com", []string{"agent@allow.example.com"}, []byte(msg)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	tick(t, ts)
	got := receiver.WaitFor(t, 5*time.Second, func(c []testutil.SubscriberCaptured) bool {
		return eventTypes(c)["email.flagged"] >= 1 && eventTypes(c)["email.received"] >= 1
	})
	types := eventTypes(got)
	if types["email.received"] < 1 {
		t.Errorf("email.received not delivered (message must NOT be dropped): %v", types)
	}
	if types["email.flagged"] < 1 {
		t.Errorf("email.flagged not delivered for non-allowlisted sender: %v", types)
	}

	// The persisted row must carry the flag + a reason.
	flagged, reason := readFlagged(t, pool, agent.ID)
	if !flagged {
		t.Error("persisted inbound row not flagged")
	}
	if reason == "" {
		t.Error("flagged row has empty flag_reason")
	}
}

// TestInboundPolicy_AllowlistAcceptsMember: a sender ON the allowlist is NOT
// flagged and no email.flagged is emitted.
func TestInboundPolicy_AllowlistAcceptsMember(t *testing.T) {
	ts, pool, agent, receiver := setupFlaggedAgent(t, "allowlist",
		[]string{"friend@trusted.com"}, "agent@allow2.example.com", "allow2.example.com")

	msg := "From: friend@trusted.com\r\nTo: agent@allow2.example.com\r\nSubject: Hi\r\n\r\nHello"
	if err := smtp.SendMail(ts.SMTPAddr, nil, "friend@trusted.com", []string{"agent@allow2.example.com"}, []byte(msg)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	tick(t, ts)
	got := receiver.WaitFor(t, 5*time.Second, func(c []testutil.SubscriberCaptured) bool {
		return eventTypes(c)["email.received"] >= 1
	})
	if n := eventTypes(got)["email.flagged"]; n != 0 {
		t.Errorf("allowlisted sender should not be flagged, got %d email.flagged", n)
	}
	if flagged, _ := readFlagged(t, pool, agent.ID); flagged {
		t.Error("allowlisted sender persisted as flagged")
	}
}

// TestInboundPolicy_EvaluatesFromNotReplyTo pins the security-critical property:
// the policy is evaluated against the authenticated From identity, NOT the
// attacker-controllable Reply-To. A spoofer puts a trusted address in Reply-To
// but sends From an untrusted address — it must STILL be flagged.
func TestInboundPolicy_EvaluatesFromNotReplyTo(t *testing.T) {
	ts, pool, agent, receiver := setupFlaggedAgent(t, "allowlist",
		[]string{"friend@trusted.com"}, "agent@replyto.example.com", "replyto.example.com")

	// From is the untrusted sender; Reply-To claims the trusted address.
	msg := "From: stranger@evil.com\r\n" +
		"Reply-To: friend@trusted.com\r\n" +
		"To: agent@replyto.example.com\r\nSubject: Hi\r\n\r\nHello"
	if err := smtp.SendMail(ts.SMTPAddr, nil, "stranger@evil.com", []string{"agent@replyto.example.com"}, []byte(msg)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	tick(t, ts)
	got := receiver.WaitFor(t, 5*time.Second, func(c []testutil.SubscriberCaptured) bool {
		return eventTypes(c)["email.flagged"] >= 1
	})
	if n := eventTypes(got)["email.flagged"]; n < 1 {
		t.Error("Reply-To spoof of a trusted address must NOT bypass the gate — expected email.flagged")
	}
	if flagged, _ := readFlagged(t, pool, agent.ID); !flagged {
		t.Error("Reply-To spoof persisted as un-flagged (gate read Reply-To, not From)")
	}

	// The flagged event must name the AUTHENTICATED From (the identity that
	// failed the gate), not the trusted-looking Reply-To. Surfacing the
	// Reply-To here would let an attacker make a rejected message look like it
	// came from the address they impersonated (review finding #1).
	if fe := eventData(got, "email.flagged"); fe != nil {
		if fe["from"] != "stranger@evil.com" {
			t.Errorf("email.flagged from = %v, want the authenticated From stranger@evil.com", fe["from"])
		}
		if fe["display_sender"] != "friend@trusted.com" {
			t.Errorf("email.flagged display_sender = %v, want the Reply-To friend@trusted.com", fe["display_sender"])
		}
	} else {
		t.Error("no email.flagged data captured")
	}
}

// TestInboundPolicy_ReceivedSurfacesAuthenticatedFrom pins the surfacing
// contract that makes the verdict honest: when the display sender (Reply-To)
// differs from the authenticated From, email.received carries BOTH —
// `authenticated_from` is the gated/verified identity, `from` is the reply
// target. A consumer of a verified_only/allowlist agent must trust
// `authenticated_from`, not `from`. Without this, an allowlisted (or
// DMARC-aligned) From + attacker Reply-To yields an unflagged message whose
// displayed sender is attacker-chosen (review finding #1, the unflagged inverse).
func TestInboundPolicy_ReceivedSurfacesAuthenticatedFrom(t *testing.T) {
	ts, pool, agent, receiver := setupFlaggedAgent(t, "allowlist",
		[]string{"friend@trusted.com"}, "agent@authfrom.example.com", "authfrom.example.com")

	// From is on the allowlist (so NOT flagged); Reply-To points elsewhere.
	msg := "From: friend@trusted.com\r\n" +
		"Reply-To: attacker@evil.com\r\n" +
		"To: agent@authfrom.example.com\r\nSubject: Hi\r\n\r\nHello"
	if err := smtp.SendMail(ts.SMTPAddr, nil, "friend@trusted.com", []string{"agent@authfrom.example.com"}, []byte(msg)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	tick(t, ts)
	got := receiver.WaitFor(t, 5*time.Second, func(c []testutil.SubscriberCaptured) bool {
		return eventTypes(c)["email.received"] >= 1
	})
	// Allowlisted From → not flagged.
	if n := eventTypes(got)["email.flagged"]; n != 0 {
		t.Errorf("allowlisted From should not be flagged, got %d email.flagged", n)
	}
	if flagged, _ := readFlagged(t, pool, agent.ID); flagged {
		t.Error("allowlisted From persisted as flagged")
	}
	// email.received must distinguish the gated identity from the reply target.
	re := eventData(got, "email.received")
	if re == nil {
		t.Fatal("no email.received data captured")
	}
	if re["authenticated_from"] != "friend@trusted.com" {
		t.Errorf("authenticated_from = %v, want the gated From friend@trusted.com", re["authenticated_from"])
	}
	if re["from"] != "attacker@evil.com" {
		t.Errorf("from = %v, want the reply target attacker@evil.com (display sender)", re["from"])
	}
}

// TestInboundPolicy_OpenNeverFlags: the default open policy flags nothing, even
// for an arbitrary external sender.
func TestInboundPolicy_OpenNeverFlags(t *testing.T) {
	ts, pool, agent, receiver := setupFlaggedAgent(t, "open",
		nil, "agent@open.example.com", "open.example.com")

	msg := "From: anyone@wherever.com\r\nTo: agent@open.example.com\r\nSubject: Hi\r\n\r\nHello"
	if err := smtp.SendMail(ts.SMTPAddr, nil, "anyone@wherever.com", []string{"agent@open.example.com"}, []byte(msg)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	tick(t, ts)
	got := receiver.WaitFor(t, 5*time.Second, func(c []testutil.SubscriberCaptured) bool {
		return eventTypes(c)["email.received"] >= 1
	})
	if n := eventTypes(got)["email.flagged"]; n != 0 {
		t.Errorf("open policy emitted %d email.flagged; want 0", n)
	}
	if flagged, _ := readFlagged(t, pool, agent.ID); flagged {
		t.Error("open policy flagged a message")
	}
}
