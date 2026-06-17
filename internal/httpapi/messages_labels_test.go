package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// labelDeps builds a server whose ModifyMessageLabels fake maps message ids to
// outcomes: "msg_cap" → cap exceeded, "msg_missing" → not found, anything else
// → echo the add list as the post-update set.
func labelDeps() Deps {
	return Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			if address == "support@acme.com" {
				a := sampleAgent()
				return &a, nil
			}
			return nil, errors.New("not found")
		},
		ModifyMessageLabels: func(ctx context.Context, messageID, agentID string, add, remove []string) ([]string, error) {
			if agentID != "support@acme.com" {
				return nil, errors.New("unexpected agent")
			}
			switch messageID {
			case "msg_cap":
				return nil, identity.ErrLabelLimitExceeded
			case "msg_missing":
				return nil, identity.ErrMessageNotFound
			default:
				return add, nil
			}
		},
		Legacy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }),
	}
}

func TestUpdateMessageLabels_AddRemove(t *testing.T) {
	srv := httptest.NewServer(New(labelDeps()))
	t.Cleanup(srv.Close)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com/messages/msg_a", "good", map[string]any{
		"add_labels": []string{"urgent", "vip"},
	})
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["message_id"] != "msg_a" {
		t.Fatalf("message_id = %v, want msg_a", body["message_id"])
	}
	labels, _ := body["labels"].([]any)
	if len(labels) != 2 || labels[0] != "urgent" {
		t.Fatalf("labels = %v, want [urgent vip]", body["labels"])
	}
}

func TestUpdateMessageLabels_CapExceededIs400(t *testing.T) {
	srv := httptest.NewServer(New(labelDeps()))
	t.Cleanup(srv.Close)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com/messages/msg_cap", "good", map[string]any{
		"add_labels": []string{"x"},
	})
	if code != 400 {
		t.Fatalf("cap exceeded: status %d (want 400) body %v", code, body)
	}
}

func TestUpdateMessageLabels_NotFoundIs404(t *testing.T) {
	srv := httptest.NewServer(New(labelDeps()))
	t.Cleanup(srv.Close)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com/messages/msg_missing", "good", map[string]any{
		"add_labels": []string{"x"},
	})
	if code != 404 {
		t.Fatalf("missing message: status %d (want 404) body %v", code, body)
	}
}

// An invalid label is rejected by shared validation (400) before the store is
// touched — proves the label rules don't diverge from the legacy surface.
func TestUpdateMessageLabels_InvalidLabelIs400(t *testing.T) {
	srv := httptest.NewServer(New(labelDeps()))
	t.Cleanup(srv.Close)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com/messages/msg_a", "good", map[string]any{
		"add_labels": []string{"BAD LABEL!"},
	})
	if code != 400 {
		t.Fatalf("invalid label: status %d (want 400) body %v", code, body)
	}
}

// Per-agent op: an agent-scoped credential bound to this agent may label its
// own messages (it is NOT account-only). resolveOwnedAgent pins the binding.
func TestUpdateMessageLabels_AgentScopedAllowedOnOwnAgent(t *testing.T) {
	deps := labelDeps()
	deps.PrincipalAuthenticator = func(r *http.Request) (*identity.Principal, error) {
		if r.Header.Get("Authorization") == "Bearer agent" {
			return &identity.Principal{
				User:    &identity.User{ID: "u_1", Email: "owner@acme.com"},
				Scope:   identity.ScopeAgent,
				AgentID: "support@acme.com",
			}, nil
		}
		return nil, errors.New("unauthorized")
	}
	srv := httptest.NewServer(New(deps))
	t.Cleanup(srv.Close)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/agents/support%40acme.com/messages/msg_a", "agent", map[string]any{
		"add_labels": []string{"urgent"},
	})
	if code != 200 {
		t.Fatalf("agent-scoped own-agent label: status %d (want 200) body %v", code, body)
	}
}
