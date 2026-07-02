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
	"github.com/Mnexa-AI/e2a/internal/warmup"
)

// warmupSendServer builds a minimal send-capable server whose EnforceWarmup dep
// is driven by the supplied func, and records whether the message was actually
// delivered (so a test can distinguish "throttled before send" from "allowed").
func warmupSendServer(t *testing.T, enforce func(ctx context.Context, domain string) error, delivered *bool) *httptest.Server {
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
		EnforceWarmup: enforce,
		DeliverOutbound: func(ctx context.Context, u *identity.User, a *identity.AgentIdentity, req outbound.SendRequest, mt, rt string, ref *identity.Message) (*agent.OutboundResult, *agent.OutboundError) {
			*delivered = true
			return &agent.OutboundResult{MessageID: "msg_1", Method: "smtp"}, nil
		},
	}))
	t.Cleanup(srv.Close)
	return srv
}

func warmupSend(t *testing.T, srv *httptest.Server) (int, map[string]any) {
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

// A domain over its warmup daily cap is rejected with 429 warmup_throttled,
// BEFORE any delivery, carrying the pacing details.
func TestSendWarmupThrottled(t *testing.T) {
	delivered := false
	srv := warmupSendServer(t, func(ctx context.Context, domain string) error {
		return &warmup.ThrottleError{Domain: domain, DailyCap: 50, SentToday: 50, RetryAfter: 3 * time.Hour}
	}, &delivered)

	code, body := warmupSend(t, srv)
	if code != http.StatusTooManyRequests || errCode(body) != "warmup_throttled" {
		t.Fatalf("want 429 warmup_throttled, got %d %v", code, body)
	}
	if delivered {
		t.Fatal("send must be throttled BEFORE delivery")
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
}

// Under the cap: the enforcer returns nil, the send proceeds.
func TestSendWarmupAllowed(t *testing.T) {
	delivered := false
	srv := warmupSendServer(t, func(ctx context.Context, domain string) error { return nil }, &delivered)
	code, body := warmupSend(t, srv)
	if code != http.StatusOK {
		t.Fatalf("under cap: want 200, got %d %v", code, body)
	}
	if !delivered {
		t.Fatal("under-cap send should be delivered")
	}
}

// Fail-open: a non-throttle error from the enforcer (e.g. a DB read blip) must
// NOT block real mail — the send proceeds.
func TestSendWarmupFailsOpenOnError(t *testing.T) {
	delivered := false
	srv := warmupSendServer(t, func(ctx context.Context, domain string) error {
		return errors.New("db down")
	}, &delivered)
	code, body := warmupSend(t, srv)
	if code != http.StatusOK {
		t.Fatalf("fail-open: want 200, got %d %v", code, body)
	}
	if !delivered {
		t.Fatal("a non-throttle enforcer error must fail open (deliver anyway)")
	}
}
