package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/go-chi/chi/v5"
	"nhooyr.io/websocket"
)

// ── Mock store ──────────────────────────────────────────────────

type mockStore struct {
	user     *identity.User
	userErr  error
	scope    string // "" → unscoped (legacy/account-equivalent); ScopeAgent pins to agentID
	agentID  string // bound agent id when scope == ScopeAgent
	agent    *identity.AgentIdentity
	agentErr error
	messages []identity.Message
	msgErr   error
}

func (m *mockStore) GetPrincipalByAPIKey(_ context.Context, apiKey string) (*identity.Principal, error) {
	if m.userErr != nil {
		return nil, m.userErr
	}
	if m.user == nil {
		return nil, nil
	}
	return &identity.Principal{User: m.user, Scope: m.scope, AgentID: m.agentID}, nil
}

func (m *mockStore) GetAgentByEmail(_ context.Context, email string) (*identity.AgentIdentity, error) {
	return m.agent, m.agentErr
}

func (m *mockStore) GetMessagesByAgent(_ context.Context, _ identity.MessageListFilter) ([]identity.Message, error) {
	return m.messages, m.msgErr
}

// ── Helpers ─────────────────────────────────────────────────────

func newTestAgent(userID string) *identity.AgentIdentity {
	return &identity.AgentIdentity{
		ID:     "agent_test",
		Email:  "bot@agents.e2a.dev",
		UserID: userID,
	}
}

func newTestUser() *identity.User {
	return &identity.User{ID: "user_1", Email: "alice@example.com"}
}

// startServer mounts the WS handler EXACTLY the way production does
// (internal/httpapi: chi + PathUnescape + ServeWithEmail). The old test mount
// used gorilla/mux, which decodes route vars — the opposite of chi — and that
// mismatch masked the percent-encoded-address 404 (#372): the handler passed
// its tests on a router with different semantics than the one serving it.
func startServer(t *testing.T, handler *Handler) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/api/v1/agents/{email}/ws", func(w http.ResponseWriter, req *http.Request) {
		address := chi.URLParam(req, "email")
		if decoded, err := url.PathUnescape(address); err == nil {
			address = decoded
		}
		handler.ServeWithEmail(w, req, address)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// dialWSPath connects to a specific WS path on the test server, authenticating
// with the Authorization: Bearer header (the credential is never in the URL).
func dialWSPath(t *testing.T, srv *httptest.Server, path, token string) (*websocket.Conn, *http.Response) {
	t.Helper()
	url := fmt.Sprintf("ws%s%s", strings.TrimPrefix(srv.URL, "http"), path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	opts := &websocket.DialOptions{HTTPHeader: http.Header{}}
	if token != "" {
		opts.HTTPHeader.Set("Authorization", "Bearer "+token)
	}
	conn, resp, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		// Return nil conn for error-path tests
		return nil, resp
	}
	return conn, resp
}

// dialWS connects to the versioned WS endpoint.
func dialWS(t *testing.T, srv *httptest.Server, email, token string) (*websocket.Conn, *http.Response) {
	t.Helper()
	// Percent-encode the address like every real client does
	// (encodeURIComponent in ws.ts, quote() in the Python SDK) — realistic
	// input is the whole point of mounting the test router like production.
	return dialWSPath(t, srv, fmt.Sprintf("/api/v1/agents/%s/ws", url.PathEscape(email)), token)
}

// waitConnected polls the hub until the agent is registered or timeout.
func waitConnected(t *testing.T, hub *Hub, agentID string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !hub.IsConnected(agentID) {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s to register in hub", agentID)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// doHTTP does a plain HTTP GET (no upgrade) to the WS endpoint, sending the
// credential (when non-empty) as an Authorization: Bearer header.
func doHTTP(t *testing.T, srv *httptest.Server, email, token string) *http.Response {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/agents/%s/ws", srv.URL, email)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP GET failed: %v", err)
	}
	return resp
}

// ── Tests ───────────────────────────────────────────────────────

func TestHandler_NoToken(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	store := &mockStore{}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	resp := doHTTP(t, srv, "bot@agents.e2a.dev", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Authorization: Bearer") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// TestHandler_QueryTokenRejected pins the cutover: the legacy `?token=<key>`
// query parameter is no longer accepted — only the Authorization: Bearer header.
func TestHandler_QueryTokenRejected(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	store := &mockStore{user: newTestUser()}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	// GET with the credential ONLY in the query string, no Authorization header.
	url := fmt.Sprintf("%s/api/v1/agents/%s/ws?token=%s", srv.URL, "bot@agents.e2a.dev", "valid_key")
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a ?token= query must be rejected (header-only auth), got %d", resp.StatusCode)
	}
}

func TestHandler_InvalidToken(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	store := &mockStore{user: nil, userErr: fmt.Errorf("not found")}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	resp := doHTTP(t, srv, "bot@agents.e2a.dev", "bad_key")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestHandler_AgentNotFound(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	store := &mockStore{
		user:     newTestUser(),
		agentErr: fmt.Errorf("not found"),
	}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	resp := doHTTP(t, srv, "bot@agents.e2a.dev", "valid_key")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHandler_NotOwner(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	store := &mockStore{
		user:  newTestUser(),
		agent: newTestAgent("other_user"),
	}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	resp := doHTTP(t, srv, "bot@agents.e2a.dev", "valid_key")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// HIGH-1 regression: an agent-scoped credential pinned to agent A must NOT be
// able to open agent B's stream, even when both agents share the same owner.
func TestHandler_AgentScoped_WrongAgent_Forbidden(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	store := &mockStore{
		user:    newTestUser(),          // user_1
		scope:   identity.ScopeAgent,    // agent-scoped credential…
		agentID: "agent_OTHER",          // …bound to a different agent
		agent:   newTestAgent("user_1"), // target agent (id agent_test), same owner
	}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	resp := doHTTP(t, srv, "bot@agents.e2a.dev", "agent_a_key")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent-scoped key for a different agent must be 403, got %d", resp.StatusCode)
	}
}

// HIGH-1: an agent-scoped credential pinned to the SAME agent it targets connects.
func TestHandler_AgentScoped_BoundAgent_Connects(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	store := &mockStore{
		user:    newTestUser(),
		scope:   identity.ScopeAgent,
		agentID: "agent_test", // matches newTestAgent's ID
		agent:   newTestAgent("user_1"),
	}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	conn, _ := dialWS(t, srv, "bot@agents.e2a.dev", "agent_test_key")
	if conn == nil {
		t.Fatal("agent-scoped key for its own agent should connect")
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitConnected(t, hub, "agent_test")
}

func TestHandler_SuccessfulConnect(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	store := &mockStore{
		user:  newTestUser(),
		agent: newTestAgent("user_1"),
	}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	conn, _ := dialWS(t, srv, "bot@agents.e2a.dev", "valid_key")
	if conn == nil {
		t.Fatal("expected successful WS connection")
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	waitConnected(t, hub, "agent_test")
}

func TestHandler_DrainUnreadOnConnect(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	now := time.Now()
	store := &mockStore{
		user:  newTestUser(),
		agent: newTestAgent("user_1"),
		messages: []identity.Message{
			{
				ID:             "msg_1",
				AgentID:        "agent_test",
				Sender:         "alice@example.com",
				Recipient:      "bot@agents.e2a.dev",
				Subject:        "Hello",
				ConversationID: "conv_1",
				CreatedAt:      now,
			},
			{
				ID:        "msg_2",
				AgentID:   "agent_test",
				Sender:    "bob@example.com",
				Recipient: "bot@agents.e2a.dev",
				Subject:   "Hi there",
				CreatedAt: now,
			},
		},
	}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	conn, _ := dialWS(t, srv, "bot@agents.e2a.dev", "valid_key")
	if conn == nil {
		t.Fatal("expected successful WS connection")
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read two drained notifications
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read notification %d: %v", i+1, err)
		}

		var notif struct {
			MessageID      string `json:"message_id"`
			ConversationID string `json:"conversation_id,omitempty"`
			From           string `json:"from"`
			Recipient      string `json:"recipient"`
			Subject        string `json:"subject"`
		}
		if err := json.Unmarshal(data, &notif); err != nil {
			t.Fatalf("unmarshal notification %d: %v", i+1, err)
		}

		if i == 0 {
			if notif.MessageID != "msg_1" {
				t.Fatalf("expected msg_1, got %s", notif.MessageID)
			}
			if notif.ConversationID != "conv_1" {
				t.Fatalf("expected conv_1, got %s", notif.ConversationID)
			}
			if notif.From != "alice@example.com" {
				t.Fatalf("expected alice@example.com, got %s", notif.From)
			}
			if notif.Subject != "Hello" {
				t.Fatalf("expected Hello, got %s", notif.Subject)
			}
		} else {
			if notif.MessageID != "msg_2" {
				t.Fatalf("expected msg_2, got %s", notif.MessageID)
			}
			if notif.ConversationID != "" {
				t.Fatalf("expected empty conversation_id, got %s", notif.ConversationID)
			}
		}
	}

	// Client frames are ignored by the server; fetching the message over REST is
	// what marks it read.
	clientMsg, _ := json.Marshal(map[string]string{"type": "ack", "message_id": "msg_1"})
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := conn.Write(writeCtx, websocket.MessageText, clientMsg)
	writeCancel()
	if err != nil {
		t.Fatalf("failed to send client message: %v", err)
	}
}

func TestHandler_DisconnectUnregisters(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	store := &mockStore{
		user:  newTestUser(),
		agent: newTestAgent("user_1"),
	}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	conn, _ := dialWS(t, srv, "bot@agents.e2a.dev", "valid_key")
	if conn == nil {
		t.Fatal("expected successful WS connection")
	}

	waitConnected(t, hub, "agent_test")

	// Close the connection
	conn.Close(websocket.StatusNormalClosure, "bye")

	// Wait for unregister to propagate
	deadline := time.After(2 * time.Second)
	for hub.IsConnected("agent_test") {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for unregister")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestHandler_SendAfterConnect(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	store := &mockStore{
		user:  newTestUser(),
		agent: newTestAgent("user_1"),
	}
	handler := NewHandler(hub, store)
	srv := startServer(t, handler)

	conn, _ := dialWS(t, srv, "bot@agents.e2a.dev", "valid_key")
	if conn == nil {
		t.Fatal("expected successful WS connection")
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	waitConnected(t, hub, "agent_test")

	// Send a message through the hub after connection
	msg := `{"message_id":"msg_live","from":"charlie@example.com","subject":"Live"}`
	if !hub.Send("agent_test", []byte(msg)) {
		t.Fatal("expected send to succeed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != msg {
		t.Fatalf("got %q, want %q", data, msg)
	}
}

func TestBuildNotification(t *testing.T) {
	now := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	msg := &identity.Message{
		ID:             "msg_bn",
		Sender:         "alice@example.com",
		Recipient:      "bot@agents.e2a.dev",
		Subject:        "Test Subject",
		ConversationID: "conv_abc",
		CreatedAt:      now,
	}

	data := BuildNotification(msg)
	var notif map[string]interface{}
	if err := json.Unmarshal(data, &notif); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if notif["message_id"] != "msg_bn" {
		t.Fatalf("expected msg_bn, got %v", notif["message_id"])
	}
	if notif["conversation_id"] != "conv_abc" {
		t.Fatalf("expected conv_abc, got %v", notif["conversation_id"])
	}
	if notif["from"] != "alice@example.com" {
		t.Fatalf("expected alice@example.com, got %v", notif["from"])
	}
	if notif["recipient"] != "bot@agents.e2a.dev" {
		t.Fatalf("expected bot@agents.e2a.dev, got %v", notif["recipient"])
	}
	// Notification stays lightweight — full To/Cc lists are fetched via REST.
	if _, hasTo := notif["to"]; hasTo {
		t.Fatalf("notification should not include 'to'; clients fetch via REST")
	}
	if notif["subject"] != "Test Subject" {
		t.Fatalf("expected Test Subject, got %v", notif["subject"])
	}
}

func TestBuildNotification_OmitsEmptyConversationID(t *testing.T) {
	msg := &identity.Message{
		ID:        "msg_no_conv",
		Sender:    "alice@example.com",
		Recipient: "bot@agents.e2a.dev",
		Subject:   "No Conv",
		CreatedAt: time.Now(),
	}

	data := BuildNotification(msg)
	var notif map[string]interface{}
	json.Unmarshal(data, &notif)

	if _, exists := notif["conversation_id"]; exists {
		t.Fatal("expected conversation_id to be omitted when empty")
	}
}
