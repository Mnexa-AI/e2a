package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/auth"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func setupUserAuth(t *testing.T) (*auth.UserAuth, *identity.Store, string) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	cfg := &config.OAuthConfig{
		GoogleClientID:     "test",
		GoogleClientSecret: "test",
		RedirectURL:        "http://localhost/api/auth/callback",
	}
	ua := auth.NewUserAuth(cfg, store, false)

	// Create a user and session
	user, err := store.CreateOrGetUser(ctx, "josh@test.com", "Josh", "google-sub-123")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	token, err := store.CreateUserSession(ctx, user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	return ua, store, token
}

func authedRequest(method, url, sessionToken string) *http.Request {
	req := httptest.NewRequest(method, url, nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionToken})
	return req
}

func TestHandleAgentActivity_Unauthenticated(t *testing.T) {
	ua, _, _ := setupUserAuth(t)

	req := httptest.NewRequest("GET", "/api/dashboard/agents/agent%40test.example.com/activity", nil)
	w := httptest.NewRecorder()
	ua.HandleAgentActivity(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleAgentActivity_AgentNotFound(t *testing.T) {
	ua, _, token := setupUserAuth(t)

	req := authedRequest("GET", "/api/dashboard/agents/agent%40nonexistent.example.com/activity", token)
	w := httptest.NewRecorder()
	ua.HandleAgentActivity(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleAgentActivity_NotOwned(t *testing.T) {
	ua, store, token := setupUserAuth(t)
	ctx := context.Background()

	// Create agent owned by a different user (not the authenticated user)
	otherUser, _ := store.CreateOrGetUser(ctx, "other@example.com", "Other", "google-other")
	store.ClaimOrCreateDomain(ctx, "unowned.example.com", otherUser.ID)
	store.CreateAgent(ctx, "agent@unowned.example.com", "unowned.example.com", "", "https://example.com/webhook", "", otherUser.ID)

	req := authedRequest("GET", "/api/dashboard/agents/agent%40unowned.example.com/activity", token)
	w := httptest.NewRecorder()
	ua.HandleAgentActivity(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for unowned agent", w.Code)
	}
}

func TestHandleAgentActivity_EmptyActivity(t *testing.T) {
	ua, store, token := setupUserAuth(t)
	ctx := context.Background()

	user, _ := store.GetUserSession(ctx, token)
	store.ClaimOrCreateDomain(ctx, "empty-activity.example.com", user.ID)
	store.CreateAgent(ctx, "agent@empty-activity.example.com", "empty-activity.example.com", "", "https://example.com/webhook", "", user.ID)

	req := authedRequest("GET", "/api/dashboard/agents/agent%40empty-activity.example.com/activity", token)
	w := httptest.NewRecorder()
	ua.HandleAgentActivity(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var activity []identity.Message
	json.NewDecoder(w.Body).Decode(&activity)
	if len(activity) != 0 {
		t.Errorf("expected empty activity, got %d", len(activity))
	}
}

func TestHandleAgentActivity_WithMessages(t *testing.T) {
	ua, store, token := setupUserAuth(t)
	ctx := context.Background()

	user, _ := store.GetUserSession(ctx, token)
	store.ClaimOrCreateDomain(ctx, "active.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "agent@active.example.com", "active.example.com", "", "https://example.com/webhook", "", user.ID)

	// Create some activity ("" id lets the store generate one).
	store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", "bot@active.example.com", "", "Hello", "", "", nil, nil)
	store.CreateOutboundMessage(ctx, agent.ID, []string{"alice@gmail.com"}, nil, nil, "Re: Hello", "reply", "smtp", "", "")
	store.CreateInboundMessage(ctx, "", agent.ID, "bob@gmail.com", "bot@active.example.com", "", "Hi", "", "", nil, nil)

	req := authedRequest("GET", "/api/dashboard/agents/agent%40active.example.com/activity", token)
	w := httptest.NewRecorder()
	ua.HandleAgentActivity(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var activity []identity.Message
	json.NewDecoder(w.Body).Decode(&activity)
	if len(activity) != 3 {
		t.Fatalf("expected 3 activities, got %d", len(activity))
	}

	// Most recent first
	if activity[0].Direction != "inbound" || activity[0].Subject != "Hi" {
		t.Errorf("first entry: direction=%q subject=%q", activity[0].Direction, activity[0].Subject)
	}
	if activity[1].Direction != "outbound" || activity[1].Method != "smtp" {
		t.Errorf("second entry: direction=%q method=%q", activity[1].Direction, activity[1].Method)
	}
	if activity[2].Direction != "inbound" || activity[2].Subject != "Hello" {
		t.Errorf("third entry: direction=%q subject=%q", activity[2].Direction, activity[2].Subject)
	}
}
