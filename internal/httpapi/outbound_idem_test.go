package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// pathRecordingIdem captures the route (path) handed to Claim so a test can
// assert that two agents land under distinct idempotency routes.
type pathRecordingIdem struct{ paths []string }

func (f *pathRecordingIdem) Claim(ctx context.Context, userID, key, path, bodyHash string) (idempotency.ClaimResult, error) {
	f.paths = append(f.paths, path)
	return idempotency.ClaimResult{Outcome: idempotency.OutcomeAcquired}, nil
}
func (f *pathRecordingIdem) Complete(ctx context.Context, userID, key string, resp idempotency.CachedResponse) error {
	return nil
}
func (f *pathRecordingIdem) Release(ctx context.Context, userID, key string) error { return nil }

// TestSendIdempotencyRouteIncludesAgent is the regression for the cross-agent
// replay hole introduced by moving the sender from the body (`from`) to the
// path: two agents owned by the same user, reusing ONE Idempotency-Key + an
// identical body, must not collide. Since the agent is no longer in the body,
// the dedup body-hash alone wouldn't separate them — handleCreateMessage folds
// the agent id into the idempotency route, so the two claims land under
// different routes (→ different hash → a mismatch/422, never agent A's cached
// response replayed for agent B).
func TestSendIdempotencyRouteIncludesAgent(t *testing.T) {
	rec := &pathRecordingIdem{}
	agents := map[string]*identity.AgentIdentity{
		"a@acme.com": {ID: "a@acme.com", Email: "a@acme.com", UserID: "u_1", DomainVerified: true},
		"b@acme.com": {ID: "b@acme.com", Email: "b@acme.com", UserID: "u_1", DomainVerified: true},
	}
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) { return &identity.User{ID: "u_1"}, nil },
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			if a, ok := agents[address]; ok {
				return a, nil
			}
			return nil, errors.New("not found")
		},
		Idempotency: rec,
		DeliverOutbound: func(ctx context.Context, u *identity.User, ag *identity.AgentIdentity, req outbound.SendRequest, mt, rt string, ref *identity.Message) (*agent.OutboundResult, *agent.OutboundError) {
			return &agent.OutboundResult{MessageID: "m", Method: "smtp"}, nil
		},
	}))
	t.Cleanup(srv.Close)

	send := func(addr string) {
		b, _ := json.Marshal(map[string]any{"to": []string{"x@y.com"}, "subject": "Hi", "body": "hello"})
		req, _ := http.NewRequest("POST", srv.URL+"/v1/agents/"+addr+"/messages", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer good")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "dupkey") // SAME key for both agents
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	send("a%40acme.com")
	send("b%40acme.com")

	if len(rec.paths) != 2 {
		t.Fatalf("want 2 idempotency claims, got %d (%v)", len(rec.paths), rec.paths)
	}
	if rec.paths[0] == rec.paths[1] {
		t.Fatalf("two agents reusing one key must claim under DIFFERENT routes; both = %q", rec.paths[0])
	}
	if !strings.Contains(rec.paths[0], "a@acme.com") || !strings.Contains(rec.paths[1], "b@acme.com") {
		t.Errorf("idempotency route should fold in the agent id, got %v", rec.paths)
	}
}
