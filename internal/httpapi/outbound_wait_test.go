package httpapi

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// TestSend_WaitSent_PollsToSent: with ?wait=sent, an async 'accepted' send holds
// until the delivery_status poll reaches 'sent', then returns 200 status=sent with
// the provider id (contract §2). Without wait it would return 'accepted'.
func TestSend_WaitSent_PollsToSent(t *testing.T) {
	polls := 0
	srv := testServer(t, func(d *Deps) {
		d.DeliverOutbound = func(_ context.Context, _ *identity.User, _ *identity.AgentIdentity, _ outbound.SendRequest, _, _ string, _ *identity.Message, _ agent.AcceptIdemCompleter) (*agent.OutboundResult, *agent.OutboundError) {
			return &agent.OutboundResult{MessageID: "msg_wait_1", Status: "accepted", Method: "smtp"}, nil
		}
		d.PollSendOutcome = func(_ context.Context, _ string) (identity.SendOutcome, error) {
			polls++
			if polls < 2 {
				return identity.SendOutcome{DeliveryStatus: "accepted"}, nil // still in flight
			}
			return identity.SendOutcome{DeliveryStatus: "sent", ProviderMessageID: "ses-abc", SentAs: "relay"}, nil
		}
	})
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages?wait=sent", "good",
		map[string]any{"to": []string{"x@y.com"}, "subject": "s", "body": "b"})
	if code != 200 || body["status"] != "sent" || body["provider_message_id"] != "ses-abc" {
		t.Fatalf("wait=sent: want 200 sent + provider id, got %d %v", code, body)
	}
}
