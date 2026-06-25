package httpapi

import (
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
)

// getRaw issues a GET and returns the full response so header assertions are
// possible (getJSON discards headers).
func getRaw(t *testing.T, url, bearer string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestPollRateLimited(t *testing.T) {
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1"}, nil
			}
			return nil, errors.New("no")
		},
		ListWebhooks: func(ctx context.Context, userID string) ([]identity.Webhook, error) {
			t.Error("ListWebhooks must NOT be reached when poll-limited")
			return nil, nil
		},
		// blocked: 3s retry-after, quota 60, 0 remaining, resets in 12s.
		PollLimit: func(key string) (bool, time.Duration, int, int, int) {
			if key != "u_1" {
				t.Errorf("poll key = %q, want u_1", key)
			}
			return false, 3 * time.Second, 60, 0, 12
		},
	}))
	t.Cleanup(srv.Close)

	resp := getRaw(t, srv.URL+"/v1/webhooks", "good")
	defer resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatalf("want 429, got %d", resp.StatusCode)
	}
	for h, want := range map[string]string{
		"Retry-After": "3", "RateLimit-Limit": "60",
		"RateLimit-Remaining": "0", "RateLimit-Reset": "12",
	} {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("header %s = %q, want %q", h, got, want)
		}
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if errCode(body) != "rate_limited" {
		t.Fatalf("want rate_limited, got %v", body)
	}
}

func TestPollRateHeadersOnAllowed(t *testing.T) {
	reached := false
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			return &identity.User{ID: "u_1"}, nil
		},
		ListWebhooks: func(ctx context.Context, userID string) ([]identity.Webhook, error) {
			reached = true
			return []identity.Webhook{}, nil
		},
		PollLimit: func(key string) (bool, time.Duration, int, int, int) {
			return true, 0, 60, 59, 60
		},
	}))
	t.Cleanup(srv.Close)

	resp := getRaw(t, srv.URL+"/v1/webhooks", "good")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if !reached {
		t.Error("ListWebhooks should be reached when allowed")
	}
	if got := resp.Header.Get("RateLimit-Remaining"); got != "59" {
		t.Errorf("RateLimit-Remaining = %q, want 59", got)
	}
	if got := resp.Header.Get("RateLimit-Limit"); got != "60" {
		t.Errorf("RateLimit-Limit = %q, want 60", got)
	}
	// Retry-After must NOT be present on a successful response.
	if got := resp.Header.Get("Retry-After"); got != "" {
		t.Errorf("Retry-After should be absent on 200, got %q", got)
	}
}

// TestNonPollLimitedReadNotThrottled guards the parity fix: reads the legacy
// surface never poll-limited (listAgents/getAgent/domains/events/limits/export)
// must NOT be throttled on /v1 either — even with a PollLimit that always
// blocks, listAgents (absent from pollLimitedOps) is reached and returns 200.
func TestNonPollLimitedReadNotThrottled(t *testing.T) {
	reached := false
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			return &identity.User{ID: "u_1"}, nil
		},
		ListAgents: func(ctx context.Context, userID string) ([]identity.AgentIdentity, error) {
			reached = true
			return []identity.AgentIdentity{}, nil
		},
		// Would block every request IF it were consulted — it must not be.
		PollLimit: func(key string) (bool, time.Duration, int, int, int) {
			t.Error("PollLimit must NOT be consulted for listAgents (not a poll-limited op)")
			return false, time.Second, 60, 0, 60
		},
	}))
	t.Cleanup(srv.Close)

	resp := getRaw(t, srv.URL+"/v1/agents", "good")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("want 200 (listAgents not poll-limited), got %d", resp.StatusCode)
	}
	if !reached {
		t.Error("ListAgents should be reached")
	}
	if resp.Header.Get("RateLimit-Limit") != "" {
		t.Errorf("non-poll-limited read should carry no RateLimit-Limit header, got %q", resp.Header.Get("RateLimit-Limit"))
	}
}

func TestRegRateLimited(t *testing.T) {
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			return &identity.User{ID: "u_1"}, nil
		},
		CreateAgent: func(ctx context.Context, email, domain, name, webhookURL, agentMode, userID string) (*identity.AgentIdentity, error) {
			t.Error("CreateAgent must NOT be reached when reg-limited")
			return nil, nil
		},
		RegLimit: func(key string) (bool, time.Duration, int, int, int) {
			return false, 30 * time.Second, 200, 0, 3600
		},
	}))
	t.Cleanup(srv.Close)

	code, body := postJSON(t, srv.URL+"/v1/agents", "good", map[string]any{
		"slug": "bot",
	})
	if code != 429 || errCode(body) != "rate_limited" {
		t.Fatalf("want 429 rate_limited, got %d %v", code, body)
	}
}

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
		DeliverOutbound: func(ctx context.Context, u *identity.User, ag *identity.AgentIdentity, req outbound.SendRequest, mt, rt string, ref *identity.Message) (*agent.OutboundResult, *agent.OutboundError) {
			t.Error("DeliverOutbound must NOT be reached when rate-limited")
			return &agent.OutboundResult{}, nil
		},
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSendRateLimited(t *testing.T) {
	srv := serverWithSendLimit(t)
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages", "good", map[string]any{
		"to": []string{"a@x.com"}, "subject": "Hi", "body": "hello",
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

// TestClientIPIgnoresXForwardedFor locks in the per-IP-limiter security
// contract: the caller key comes from CF-Connecting-IP (edge-set, not
// client-controllable), never from a forgeable X-Forwarded-For. A
// regression here re-opens the rate-limit bypass where an attacker
// rotates XFF to get unlimited budget on the registration / attachment /
// feedback limiters.
func TestClientIPIgnoresXForwardedFor(t *testing.T) {
	cases := []struct {
		name string
		cf   string
		xff  string
		addr string
		want string
	}{
		{"xff is ignored entirely", "", "1.2.3.4", "10.0.0.1:5555", "10.0.0.1"},
		{"forged xff cannot override cf", "9.9.9.9", "1.2.3.4, 9.9.9.9", "10.0.0.1:5555", "9.9.9.9"},
		{"cf preferred over remoteaddr", "9.9.9.9", "", "10.0.0.1:5555", "9.9.9.9"},
		{"remoteaddr fallback when no cf", "", "", "10.0.0.1:5555", "10.0.0.1"},
		{"bracketed ipv6 remoteaddr", "", "", "[::1]:5555", "::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.addr
			if tc.cf != "" {
				r.Header.Set("CF-Connecting-IP", tc.cf)
			}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIP(r); got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
