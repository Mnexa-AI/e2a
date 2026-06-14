package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// serverWithSendLimit builds a minimal server whose SendLimit always blocks,
// to assert the 429 path on the outbound chokepoint.
func serverWithSendLimit(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1"}, nil
			}
			return nil, errors.New("no")
		},
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			return &identity.AgentIdentity{ID: "support@acme.com", Email: "support@acme.com", UserID: "u_1", DomainVerified: true}, nil
		},
		SendLimit: func(key string) (bool, time.Duration) { return false, 7 * time.Second },
		DeliverOutbound: func(ctx context.Context, u *identity.User, ag *identity.AgentIdentity, req outbound.SendRequest, mt, rt string) (*agent.OutboundResult, *agent.OutboundError) {
			t.Error("DeliverOutbound must NOT be reached when rate-limited")
			return &agent.OutboundResult{}, nil
		},
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSendRateLimited(t *testing.T) {
	srv := serverWithSendLimit(t)
	code, body := postJSON(t, srv.URL+"/v1/send", "good", map[string]any{
		"from": "support@acme.com", "to": []string{"a@x.com"}, "subject": "Hi", "body": "hello",
	})
	if code != 429 || errCode(body) != "rate_limited" {
		t.Fatalf("want 429 rate_limited, got %d %v", code, body)
	}
	e, _ := body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	if d == nil || d["retry_after_seconds"].(float64) != 7 {
		t.Fatalf("expected retry_after_seconds=7 in details, got %v", body)
	}
}
