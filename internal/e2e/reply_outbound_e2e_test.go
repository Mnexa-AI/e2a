//go:build integration

package e2e_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestReplyForwardOutboundE2E drives the reply-to-/forward-your-own-outbound
// feature through the real API + store + relay:
//   - an agent sends a message (outbound) to alice, cc carol;
//   - GET returns that outbound message (200);
//   - POST /reply on that OUTBOUND id now succeeds (previously 404) and the
//     reply is addressed to the original recipient (alice), inheriting the
//     thread;
//   - reply_all re-includes the original cc (carol) plus any new cc;
//   - POST /forward on the outbound id succeeds and starts a new thread;
//   - a bogus id still 404s.
func TestReplyForwardOutboundE2E(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool, testutil.WithOutboundSMTP("127.0.0.1", 1025, "test.e2a.dev"))
	_, key, agent := setupDomainAndAgent(t, ts, "agent@rout.example.com", "rout.example.com", "", "")
	ctx := context.Background()

	// (1) Agent sends first — an outbound message to alice, cc carol.
	status, body := authedJSON(t, "POST", sendURL(ts.HTTPServer.URL, agent.EmailAddress()), key.PlaintextKey,
		`{"to":["alice@gmail.com"],"cc":["carol@gmail.com"],"subject":"Kickoff","body":"Let's start."}`)
	if status != 200 {
		t.Fatalf("send status=%d body=%s", status, body)
	}
	sent := parseThreadSend(t, body)
	sentConv := convOf(t, ts, agent.ID, sent.MessageID)

	// (2) GET the outbound message — must resolve (this is the asymmetry the
	// reply path used to violate).
	status, gbody := authedJSON(t, "GET",
		ts.HTTPServer.URL+"/v1/agents/"+agent.EmailAddress()+"/messages/"+sent.MessageID, key.PlaintextKey, "")
	if status != 200 {
		t.Fatalf("GET outbound status=%d body=%s", status, gbody)
	}

	// (3) Reply to the agent's OWN outbound message. Was 404; now 200, addressed
	// to the original recipient and threaded onto the same conversation.
	status, body = authedJSON(t, "POST", subResource(ts.HTTPServer.URL, agent.EmailAddress(), sent.MessageID, "reply"),
		key.PlaintextKey, `{"body":"following up"}`)
	if status != 200 {
		t.Fatalf("reply-to-outbound status=%d body=%s", status, body)
	}
	rep := parseThreadSend(t, body)

	repMsg, err := ts.Store.GetMessageWithContent(ctx, rep.MessageID, agent.ID)
	if err != nil {
		t.Fatalf("load reply row: %v", err)
	}
	if !reflect.DeepEqual(repMsg.ToRecipients, []string{"alice@gmail.com"}) {
		t.Errorf("reply To = %v, want original recipient [alice@gmail.com]", repMsg.ToRecipients)
	}
	if len(repMsg.CC) != 0 {
		t.Errorf("plain reply CC = %v, want empty", repMsg.CC)
	}
	if repMsg.ConversationID != sentConv {
		t.Errorf("reply conversation_id = %q, want %q (reply must inherit the outbound's thread)", repMsg.ConversationID, sentConv)
	}

	// (4) reply_all re-includes the original cc (carol) and merges a new cc.
	status, body = authedJSON(t, "POST", subResource(ts.HTTPServer.URL, agent.EmailAddress(), sent.MessageID, "reply"),
		key.PlaintextKey, `{"body":"all","reply_all":true,"cc":["erin@gmail.com"]}`)
	if status != 200 {
		t.Fatalf("reply_all-to-outbound status=%d body=%s", status, body)
	}
	repAll := parseThreadSend(t, body)
	repAllMsg, err := ts.Store.GetMessageWithContent(ctx, repAll.MessageID, agent.ID)
	if err != nil {
		t.Fatalf("load reply_all row: %v", err)
	}
	if !reflect.DeepEqual(repAllMsg.ToRecipients, []string{"alice@gmail.com"}) {
		t.Errorf("reply_all To = %v, want [alice@gmail.com]", repAllMsg.ToRecipients)
	}
	if !reflect.DeepEqual(repAllMsg.CC, []string{"carol@gmail.com", "erin@gmail.com"}) {
		t.Errorf("reply_all CC = %v, want [carol@gmail.com erin@gmail.com]", repAllMsg.CC)
	}

	// (5) Forward the agent's own outbound to a new recipient — new thread.
	status, body = authedJSON(t, "POST", subResource(ts.HTTPServer.URL, agent.EmailAddress(), sent.MessageID, "forward"),
		key.PlaintextKey, `{"to":["dave@gmail.com"],"body":"fyi"}`)
	if status != 200 {
		t.Fatalf("forward-outbound status=%d body=%s", status, body)
	}
	fwd := parseThreadSend(t, body)
	if c := convOf(t, ts, agent.ID, fwd.MessageID); c == sentConv {
		t.Errorf("forward conversation_id = %q, want a fresh thread (forward must not inherit)", c)
	}

	// (6) Reply to a non-existent id still 404s.
	status, body = authedJSON(t, "POST", subResource(ts.HTTPServer.URL, agent.EmailAddress(), "msg_does_not_exist", "reply"),
		key.PlaintextKey, `{"body":"x"}`)
	if status != 404 {
		t.Fatalf("reply to missing id status=%d body=%s, want 404", status, body)
	}
}
