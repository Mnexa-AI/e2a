package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tokencanopy/e2a/internal/identity"
)

type agentSuppressionFixture struct {
	mu         sync.Mutex
	rows       map[string]identity.AgentSuppression
	listFor    []string
	hookScopes []identity.AgentSuppressionHookScope
	hookErr    error
}

func newAgentSuppressionsServer(t *testing.T, mutate func(*Deps, *agentSuppressionFixture)) *httptest.Server {
	t.Helper()
	fixture := &agentSuppressionFixture{rows: map[string]identity.AgentSuppression{}}
	user := &identity.User{ID: "u_1", Email: "owner@example.com"}
	deps := Deps{
		PrincipalAuthenticator: func(r *http.Request) (*identity.Principal, error) {
			switch r.Header.Get("Authorization") {
			case "Bearer account":
				return &identity.Principal{User: user, Scope: identity.ScopeAccount}, nil
			case "Bearer agent":
				return &identity.Principal{User: user, Scope: identity.ScopeAgent, AgentID: "support@example.com"}, nil
			default:
				return nil, errors.New("unauthorized")
			}
		},
		GetAgent: func(_ context.Context, address string) (*identity.AgentIdentity, error) {
			switch address {
			case "support@example.com", "other@example.com":
				return &identity.AgentIdentity{ID: address, Email: address, UserID: "u_1"}, nil
			case "foreign@example.com":
				return &identity.AgentIdentity{ID: address, Email: address, UserID: "u_2"}, nil
			default:
				return nil, identity.ErrAgentNotFound
			}
		},
		AddAgentSuppression: func(ctx context.Context, userID, agentID, address, reason, source string, hook identity.AgentSuppressionTxHook) (identity.AgentSuppression, bool, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			agentID = identity.NormalizeEmail(agentID)
			address = identity.NormalizeEmail(address)
			key := userID + "\x00" + agentID + "\x00" + address
			if existing, ok := fixture.rows[key]; ok {
				return existing, false, nil
			}
			row := identity.AgentSuppression{AgentEmail: agentID, Address: address, Reason: reason, Source: source, CreatedAt: time.Unix(1700000000, 0).UTC()}
			if hook != nil {
				if err := hook(ctx, nil, identity.AgentSuppressionHookScope{UserID: userID, AgentID: agentID, Address: address, Source: source}); err != nil {
					return identity.AgentSuppression{}, false, err
				}
			}
			fixture.rows[key] = row
			return row, true, nil
		},
		AgentSuppressionAddedHook: func(_ context.Context, _ pgx.Tx, scope identity.AgentSuppressionHookScope) error {
			fixture.hookScopes = append(fixture.hookScopes, scope)
			return fixture.hookErr
		},
		ListAgentSuppressions: func(_ context.Context, userID, agentID string, limit int, after time.Time, afterAddress string) ([]identity.AgentSuppression, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			fixture.listFor = append(fixture.listFor, agentID)
			all := []identity.AgentSuppression{
				{AgentEmail: agentID, Address: "z@example.net", Source: "manual", CreatedAt: time.Unix(1700000200, 0).UTC()},
				{AgentEmail: agentID, Address: "a@example.net", Reason: "asked", Source: "unsubscribe", CreatedAt: time.Unix(1700000100, 0).UTC()},
			}
			start := 0
			if !after.IsZero() {
				for i := range all {
					if all[i].CreatedAt.Equal(after) && all[i].Address == afterAddress {
						start = i + 1
					}
				}
			}
			all = all[start:]
			if limit > 0 && len(all) > limit {
				all = all[:limit]
			}
			return all, nil
		},
		RemoveAgentSuppression: func(_ context.Context, userID, agentID, address string) (bool, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			key := userID + "\x00" + identity.NormalizeEmail(agentID) + "\x00" + identity.NormalizeEmail(address)
			_, ok := fixture.rows[key]
			delete(fixture.rows, key)
			return ok, nil
		},
		CursorSecret: "agent-suppression-test-secret",
	}
	if mutate != nil {
		mutate(&deps, fixture)
	}
	srv := httptest.NewServer(New(deps))
	t.Cleanup(srv.Close)
	return srv
}

func TestAgentSuppressionsRequireAccountScope(t *testing.T) {
	srv := newAgentSuppressionsServer(t, nil)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/agents/support%40example.com/suppressions", nil},
		{http.MethodPost, "/v1/agents/support%40example.com/suppressions", map[string]any{"address": "person@example.net"}},
		{http.MethodDelete, "/v1/agents/support%40example.com/suppressions/person%40example.net?confirm=DELETE", nil},
	} {
		code, body := sendJSON(t, tc.method, srv.URL+tc.path, "agent", tc.body)
		if code != http.StatusForbidden || errCode(body) != "forbidden" {
			t.Fatalf("%s %s = %d %v; want 403 forbidden", tc.method, tc.path, code, body)
		}
	}
}

func TestAgentSuppressionsDoNotEnumerateMissingOrForeignAgents(t *testing.T) {
	srv := newAgentSuppressionsServer(t, nil)
	var firstMessage string
	for _, agent := range []string{"missing%40example.com", "foreign%40example.com"} {
		code, body := sendJSON(t, http.MethodGet, srv.URL+"/v1/agents/"+agent+"/suppressions", "account", nil)
		if code != http.StatusNotFound || errCode(body) != "not_found" {
			t.Fatalf("GET %s = %d %v; want 404 not_found", agent, code, body)
		}
		errBody := body["error"].(map[string]any)
		message := errBody["message"].(string)
		if firstMessage == "" {
			firstMessage = message
		} else if firstMessage != message {
			t.Fatalf("missing and foreign messages differ: %q vs %q", firstMessage, message)
		}
	}
}

func TestAgentSuppressionsCreateIsNormalizedIdempotentAndManual(t *testing.T) {
	var gotSources []string
	var fixture *agentSuppressionFixture
	srv := newAgentSuppressionsServer(t, func(d *Deps, f *agentSuppressionFixture) {
		fixture = f
		base := d.AddAgentSuppression
		d.AddAgentSuppression = func(ctx context.Context, userID, agentID, address, reason, source string, hook identity.AgentSuppressionTxHook) (identity.AgentSuppression, bool, error) {
			gotSources = append(gotSources, source)
			return base(ctx, userID, agentID, address, reason, source, hook)
		}
	})
	body := map[string]any{"address": " Person@Example.NET ", "reason": "recipient asked"}
	for i := 0; i < 2; i++ {
		code, response := sendJSON(t, http.MethodPost, srv.URL+"/v1/agents/SUPPORT%40EXAMPLE.COM/suppressions", "account", body)
		if code != http.StatusOK {
			t.Fatalf("POST attempt %d = %d %v; want 200", i+1, code, response)
		}
		if response["agent_email"] != "support@example.com" || response["address"] != "person@example.net" || response["reason"] != "recipient asked" || response["source"] != "manual" {
			t.Fatalf("unexpected resource: %v", response)
		}
		if response["created_at"] == nil {
			t.Fatalf("resource missing created_at: %v", response)
		}
	}
	if !reflect.DeepEqual(gotSources, []string{"manual", "manual"}) {
		t.Fatalf("sources = %v; want manual for every management create", gotSources)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	wantScope := identity.AgentSuppressionHookScope{UserID: "u_1", AgentID: "support@example.com", Address: "person@example.net", Source: "manual"}
	if !reflect.DeepEqual(fixture.hookScopes, []identity.AgentSuppressionHookScope{wantScope}) {
		t.Fatalf("hook scopes = %+v, want one exact normalized scope %+v", fixture.hookScopes, wantScope)
	}
}

func TestAgentSuppressionsCreateHookFailureDoesNotPersist(t *testing.T) {
	var fixture *agentSuppressionFixture
	srv := newAgentSuppressionsServer(t, func(_ *Deps, f *agentSuppressionFixture) {
		fixture = f
		f.hookErr = errors.New("outbox unavailable")
	})
	code, body := sendJSON(t, http.MethodPost, srv.URL+"/v1/agents/support%40example.com/suppressions", "account", map[string]any{"address": "person@example.net"})
	if code != http.StatusInternalServerError || errCode(body) != "internal_error" {
		t.Fatalf("POST with failed hook = %d %v, want 500 internal_error", code, body)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.rows) != 0 || len(fixture.hookScopes) != 1 {
		t.Fatalf("failed hook persisted state: rows=%d hooks=%d", len(fixture.rows), len(fixture.hookScopes))
	}
}

func TestAgentSuppressionsListPaginationCursorIsBoundToAgent(t *testing.T) {
	var fixture *agentSuppressionFixture
	srv := newAgentSuppressionsServer(t, func(_ *Deps, f *agentSuppressionFixture) { fixture = f })
	code, first := sendJSON(t, http.MethodGet, srv.URL+"/v1/agents/support%40example.com/suppressions?limit=1", "account", nil)
	if code != http.StatusOK {
		t.Fatalf("page 1 = %d %v", code, first)
	}
	items, _ := first["items"].([]any)
	if len(items) != 1 || first["next_cursor"] == nil {
		t.Fatalf("page 1 = %v; want one item and cursor", first)
	}
	cursor := first["next_cursor"].(string)
	code, second := sendJSON(t, http.MethodGet, srv.URL+"/v1/agents/support%40example.com/suppressions?limit=1&cursor="+cursor, "account", nil)
	if code != http.StatusOK || second["next_cursor"] != nil {
		t.Fatalf("page 2 = %d %v; want last page", code, second)
	}
	code, wrong := sendJSON(t, http.MethodGet, srv.URL+"/v1/agents/other%40example.com/suppressions?limit=1&cursor="+cursor, "account", nil)
	if code != http.StatusBadRequest || errCode(wrong) != "invalid_cursor" {
		t.Fatalf("cross-agent cursor = %d %v; want 400 invalid_cursor", code, wrong)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !reflect.DeepEqual(fixture.listFor, []string{"support@example.com", "support@example.com"}) {
		t.Fatalf("list calls = %v; cross-agent cursor should fail before storage", fixture.listFor)
	}
}

func TestAgentSuppressionsDeleteRequiresConfirmationAndRemovesExactRow(t *testing.T) {
	srv := newAgentSuppressionsServer(t, nil)
	create := map[string]any{"address": "Person@Example.NET"}
	if code, body := sendJSON(t, http.MethodPost, srv.URL+"/v1/agents/support%40example.com/suppressions", "account", create); code != http.StatusOK {
		t.Fatalf("setup POST = %d %v", code, body)
	}
	if code, _ := sendJSON(t, http.MethodDelete, srv.URL+"/v1/agents/support%40example.com/suppressions/person%40example.net", "account", nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("DELETE without confirm = %d; want 422", code)
	}
	code, body := sendJSON(t, http.MethodDelete, srv.URL+"/v1/agents/support%40example.com/suppressions/Person%40Example.NET?confirm=DELETE", "account", nil)
	if code != http.StatusOK || body["deleted"] != true || body["address"] != "person@example.net" {
		t.Fatalf("confirmed DELETE = %d %v", code, body)
	}
	code, body = sendJSON(t, http.MethodDelete, srv.URL+"/v1/agents/support%40example.com/suppressions/person%40example.net?confirm=DELETE", "account", nil)
	if code != http.StatusNotFound || errCode(body) != "not_found" {
		t.Fatalf("repeat DELETE = %d %v; want 404", code, body)
	}
}

func TestAgentSuppressionsAccountEndpointsDoNotReadOrRemoveAgentRows(t *testing.T) {
	var agentListCalls, agentRemoveCalls int
	srv := newAgentSuppressionsServer(t, func(d *Deps, _ *agentSuppressionFixture) {
		d.ListSuppressions = func(context.Context, string, int, time.Time, string) ([]identity.Suppression, error) {
			return []identity.Suppression{{Address: "account@example.net", Source: "bounce", CreatedAt: time.Unix(1, 0).UTC()}}, nil
		}
		d.RemoveSuppression = func(context.Context, string, string) (bool, error) { return true, nil }
		d.ListAgentSuppressions = func(context.Context, string, string, int, time.Time, string) ([]identity.AgentSuppression, error) {
			agentListCalls++
			return nil, nil
		}
		d.RemoveAgentSuppression = func(context.Context, string, string, string) (bool, error) {
			agentRemoveCalls++
			return true, nil
		}
	})
	code, body := sendJSON(t, http.MethodGet, srv.URL+"/v1/account/suppressions", "account", nil)
	if code != http.StatusOK {
		t.Fatalf("account list = %d %v", code, body)
	}
	items := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["address"] != "account@example.net" {
		t.Fatalf("account list leaked agent rows: %v", body)
	}
	code, body = sendJSON(t, http.MethodDelete, srv.URL+"/v1/account/suppressions/account%40example.net?confirm=DELETE", "account", nil)
	if code != http.StatusOK {
		t.Fatalf("account delete = %d %v", code, body)
	}
	if agentListCalls != 0 || agentRemoveCalls != 0 {
		t.Fatalf("account endpoints called agent store: list=%d remove=%d", agentListCalls, agentRemoveCalls)
	}
}
