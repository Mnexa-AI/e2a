//go:build integration

package e2e_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/testutil"
)

// TestReplyThreadingSESMessageIDE2E pins the SES reply-threading fix through
// the real API + store + relay against a fake SES that mimics production SMTP:
// the 250 response carries the assigned id BARE (no angle brackets, no domain),
// while the Message-ID SES stamps on the delivered message is
// <id@<region>.amazonses.com>. The relay must qualify the captured id with the
// provider domain so that:
//  1. the stored provider_message_id equals the on-wire Message-ID verbatim;
//  2. a reply to the agent's own outbound anchors In-Reply-To/References on
//     that exact value.
//
// Before the fix, the bare id was stored and echoed into the reply's threading
// headers; the missing @<region>.amazonses.com meant no msg-id match, and every
// compliant client (Gmail included) forked the reply into a separate thread.
func TestReplyThreadingSESMessageIDE2E(t *testing.T) {
	pool := testutil.TestDB(t)
	// In-process fake SES: returns bare ids in the 250 response, like the real
	// email-smtp.<region>.amazonaws.com endpoint. Tests dial 127.0.0.1, so the
	// SES-host region derivation can't fire — the message_id_domain override
	// exercises the same qualification path production takes.
	fakeSMTP, smtpDone := testutil.FakeSMTPServer(t)
	ts := testutil.TestServer(t, pool,
		testutil.WithOutboundSMTP(fakeSMTP.Host, fakeSMTP.Port, "test.e2a.dev"),
		testutil.WithOutboundSMTPMessageIDDomain("us-east-2.amazonses.com"))
	_, key, agent := setupDomainAndAgent(t, ts, "agent@sesthread.example.com", "sesthread.example.com", "", "")
	ctx := context.Background()

	// (1) Send. The fake's 250 carried a bare id; the captured provider id must
	// come back domain-qualified — i.e. equal to the on-wire Message-ID.
	status, body := authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey,
		`{"to":["alice@gmail.com"],"subject":"Kickoff","text":"first touch"}`)
	if status != 200 {
		t.Fatalf("send status=%d body=%s", status, body)
	}
	sent := parseThreadSend(t, body)
	qualified := regexp.MustCompile(`^<[^@>]+@us-east-2\.amazonses\.com>$`)
	if !qualified.MatchString(sent.ProviderMessageID) {
		t.Fatalf("provider_message_id = %q, want the domain-qualified on-wire form <id@us-east-2.amazonses.com>",
			sent.ProviderMessageID)
	}

	// (2) Reply to the agent's own outbound. Its threading headers must carry
	// the parent's real Message-ID verbatim — the qualified id, not the bare one.
	status, body = authedJSON(t, "POST", subResource(ts.HTTPServer.URL, agent.EmailAddress(), sent.MessageID, "reply"),
		key.PlaintextKey, `{"text":"second touch"}`)
	if status != 200 {
		t.Fatalf("reply-to-outbound status=%d body=%s", status, body)
	}
	rep := parseThreadSend(t, body)
	repMsg, err := ts.Store.GetMessageWithContent(ctx, rep.MessageID, agent.ID)
	if err != nil {
		t.Fatalf("load reply row: %v", err)
	}
	raw := string(repMsg.RawMessage)
	if !strings.Contains(raw, "In-Reply-To: "+sent.ProviderMessageID) {
		t.Errorf("reply In-Reply-To not anchored on the parent's on-wire Message-ID %q; raw headers=\n%s",
			sent.ProviderMessageID, raw[:min(len(raw), 600)])
	}
	if !strings.Contains(raw, "References: "+sent.ProviderMessageID) {
		t.Errorf("reply References missing the parent's on-wire Message-ID %q; raw headers=\n%s",
			sent.ProviderMessageID, raw[:min(len(raw), 600)])
	}

	// (3) Same assertion on what actually crossed the wire to the provider —
	// the second message the fake accepted is the reply.
	msgs := smtpDone()
	if len(msgs) != 2 {
		t.Fatalf("fake SMTP saw %d messages, want 2 (send + reply)", len(msgs))
	}
	if !strings.Contains(msgs[1].Data, "In-Reply-To: "+sent.ProviderMessageID) {
		t.Errorf("on-wire reply In-Reply-To missing qualified parent id %q", sent.ProviderMessageID)
	}
}
