package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/sendramp"
)

// rampSendServer builds a minimal send-capable server whose DeliverOutbound
// dep is driven by the supplied func. The ramp-up gate itself lives in
// outbound.Sender.Send (below the DeliverOutbound seam), so at this layer the
// contract under test is the wire mapping: a *agent.OutboundError carrying the
// ramp-up 429 must reach the client with its structured pacing details intact.
func rampSendServer(t *testing.T, deliver func() *agent.OutboundError) *httptest.Server {
	t.Helper()
	ag := &identity.AgentIdentity{ID: "support@acme.com", Email: "support@acme.com", Domain: "acme.com", UserID: "u_1", DomainVerified: true}
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) { return &identity.User{ID: "u_1"}, nil },
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			if address == "support@acme.com" {
				return ag, nil
			}
			return nil, errors.New("not found")
		},
		DeliverOutbound: func(ctx context.Context, u *identity.User, a *identity.AgentIdentity, req outbound.SendRequest, mt, rt string, ref *identity.Message) (*agent.OutboundResult, *agent.OutboundError) {
			if oerr := deliver(); oerr != nil {
				return nil, oerr
			}
			return &agent.OutboundResult{MessageID: "msg_1", Method: "smtp"}, nil
		},
	}))
	t.Cleanup(srv.Close)
	return srv
}

func rampSend(t *testing.T, srv *httptest.Server) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"to": []string{"x@y.com"}, "subject": "Hi", "body": "hello"})
	req, _ := http.NewRequest("POST", srv.URL+"/v1/agents/support%40acme.com/messages", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer good")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// A domain over its ramp-up daily cap surfaces as 429 sending_ramp_limited with the
// pacing details (daily_cap / sent_today / retry_after_seconds) intact through
// the DeliverOutbound → error-envelope mapping. This exercises the same
// OutboundError the agent layer builds from the sender's *sendramp.ThrottleError.
func TestSendSendingRampThrottled(t *testing.T) {
	srv := rampSendServer(t, func() *agent.OutboundError {
		te := &sendramp.ThrottleError{Domain: "acme.com", DailyCap: 50, SentToday: 50, RetryAfter: 3 * time.Hour}
		return agent.SendingRampLimitError(te)
	})

	code, body := rampSend(t, srv)
	if code != http.StatusTooManyRequests || errCode(body) != "sending_ramp_limited" {
		t.Fatalf("want 429 sending_ramp_limited, got %d %v", code, body)
	}
	errObj, _ := body["error"].(map[string]any)
	det, _ := errObj["details"].(map[string]any)
	if det == nil {
		t.Fatalf("expected error.details, got %v", body)
	}
	if det["daily_cap"].(float64) != 50 || det["sent_today"].(float64) != 50 {
		t.Fatalf("expected cap/sent details, got %v", body)
	}
	if det["retry_after_seconds"].(float64) != float64(3*60*60) {
		t.Fatalf("expected retry_after_seconds=10800, got %v", det["retry_after_seconds"])
	}
	if det["domain"] != "acme.com" {
		t.Fatalf("expected domain detail, got %v", det)
	}
}

// A plain OutboundError without details keeps its envelope shape (no details
// key materializes from nil).
func TestSendOutboundErrorWithoutDetails(t *testing.T) {
	srv := rampSendServer(t, func() *agent.OutboundError {
		return &agent.OutboundError{Status: http.StatusForbidden, Code: "blocked_by_policy", Msg: "message blocked by outbound policy"}
	})
	code, body := rampSend(t, srv)
	if code != http.StatusForbidden || errCode(body) != "blocked_by_policy" {
		t.Fatalf("want 403 blocked_by_policy, got %d %v", code, body)
	}
}

// The happy path is unaffected.
func TestSendSendingRampAllowed(t *testing.T) {
	srv := rampSendServer(t, func() *agent.OutboundError { return nil })
	code, body := rampSend(t, srv)
	if code != http.StatusOK {
		t.Fatalf("under cap: want 200, got %d %v", code, body)
	}
}
