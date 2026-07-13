package ws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
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

// TestHandler_HandshakeError_JSONEnvelope pins the fix: a handshake rejection
// (before the WebSocket upgrade) returns the SAME canonical error envelope the
// REST /v1 surface does — Content-Type: application/json, a body that unmarshals
// to {error:{code,message,request_id}} with non-empty fields, and an
// X-Request-Id header that matches the body's request_id — so a client's shared
// envelope-based error handling works identically on a failed WS handshake.
func TestHandler_HandshakeError_JSONEnvelope(t *testing.T) {
	type envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}

	cases := []struct {
		name        string
		store       *mockStore
		token       string
		wantStatus  int
		wantCode    string
		wantWWWAuth bool // 401s must advertise the RFC 6750 Bearer challenge
	}{
		{
			name:        "missing bearer",
			store:       &mockStore{},
			token:       "",
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantWWWAuth: true,
		},
		{
			name:        "invalid token",
			store:       &mockStore{user: nil, userErr: fmt.Errorf("not found")},
			token:       "bad_key",
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantWWWAuth: true,
		},
		{
			// Cross-tenant: the agent exists but belongs to another account.
			// Must be INDISTINGUISHABLE from a nonexistent agent (both 404
			// not_found) so the handshake isn't an existence-enumeration oracle.
			name:       "not owner (cross-tenant)",
			store:      &mockStore{user: newTestUser(), agent: newTestAgent("other_user")},
			token:      "valid_key",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "agent not found",
			store:      &mockStore{user: newTestUser(), agentErr: fmt.Errorf("not found")},
			token:      "valid_key",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewHub()
			defer hub.Close()
			srv := startServer(t, NewHandler(hub, tc.store))

			resp := doHTTP(t, srv, "bot@agents.e2a.dev", tc.token)
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status: got %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type: got %q, want application/json", ct)
			}

			body, _ := io.ReadAll(resp.Body)
			var env envelope
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatalf("body is not the JSON envelope: %v (body=%s)", err, body)
			}
			if env.Error.Code != tc.wantCode {
				t.Fatalf("code: got %q, want %q", env.Error.Code, tc.wantCode)
			}
			if env.Error.Message == "" {
				t.Fatal("envelope message is empty")
			}
			if env.Error.RequestID == "" {
				t.Fatal("envelope request_id is empty")
			}

			// Header must echo the same request id that's in the body.
			if h := resp.Header.Get("X-Request-Id"); h != env.Error.RequestID {
				t.Fatalf("X-Request-Id header %q != body request_id %q", h, env.Error.RequestID)
			}

			if tc.wantWWWAuth {
				if wa := resp.Header.Get("WWW-Authenticate"); wa != `Bearer realm="e2a"` {
					t.Fatalf("401 must carry WWW-Authenticate Bearer challenge, got %q", wa)
				}
			}
		})
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

// TestHandler_NotOwner pins the anti-enumeration behavior: an agent that exists
// but belongs to a DIFFERENT account returns 404 not_found — the same response
// as a nonexistent agent (TestHandler_AgentNotFound) — so an authenticated
// caller can't probe which agent addresses exist across tenants. This mirrors
// the REST resolveOwnedAgent, which refuses to distinguish the two. The
// same-account agent-scope ceiling is a genuine authorization error and stays
// 403 (TestHandler_AgentScoped_WrongAgent_Forbidden).
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
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant not-owned must be 404 (anti-enumeration), got %d", resp.StatusCode)
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

		// Frames are the versioned event envelope (same shape as a webhook
		// delivery): {type, id, schema_version, created_at, data}.
		var frame struct {
			Type          string `json:"type"`
			ID            string `json:"id"`
			SchemaVersion string `json:"schema_version"`
			Data          struct {
				MessageID      string `json:"message_id"`
				ConversationID string `json:"conversation_id,omitempty"`
				From           string `json:"from"`
				Recipient      string `json:"delivered_to"`
				Subject        string `json:"subject"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("unmarshal notification %d: %v", i+1, err)
		}
		if frame.Type != "email.received" {
			t.Fatalf("type = %q, want email.received", frame.Type)
		}
		if frame.SchemaVersion != "1" {
			t.Fatalf("schema_version = %q, want 1", frame.SchemaVersion)
		}
		notif := frame.Data

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

// TestBuildNotification pins the WS frame contract: the SAME versioned event
// envelope the webhook channel delivers — {type:"email.received", id,
// schema_version, created_at, data: EmailReceivedData} — with the event id
// derived deterministically from the message id (identical to the outbox
// derivation), so a consumer can dedup WS-vs-webhook on it.
func TestBuildNotification(t *testing.T) {
	now := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	msg := &identity.Message{
		ID:             "msg_bn",
		Sender:         "alice@example.com",
		Recipient:      "bot@agents.e2a.dev",
		Subject:        "Test Subject",
		ConversationID: "conv_abc",
		ToRecipients:   []string{"bot@agents.e2a.dev"},
		CreatedAt:      now,
	}

	raw := BuildNotification(msg)
	var env struct {
		Type          string         `json:"type"`
		ID            string         `json:"id"`
		SchemaVersion string         `json:"schema_version"`
		CreatedAt     time.Time      `json:"created_at"`
		Data          map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if env.Type != webhookpub.EventEmailReceived {
		t.Fatalf("type = %q, want email.received", env.Type)
	}
	if want := webhookpub.DeterministicEventID(msg.ID, webhookpub.EventEmailReceived); env.ID != want {
		t.Fatalf("id = %q, want deterministic %q (same derivation as the webhook outbox)", env.ID, want)
	}
	if env.SchemaVersion != "1" {
		t.Fatalf("schema_version = %q, want 1", env.SchemaVersion)
	}
	if !env.CreatedAt.Equal(now) {
		t.Fatalf("created_at = %v, want %v", env.CreatedAt, now)
	}

	notif := env.Data
	if notif["message_id"] != "msg_bn" {
		t.Fatalf("expected msg_bn, got %v", notif["message_id"])
	}
	if notif["conversation_id"] != "conv_abc" {
		t.Fatalf("expected conv_abc, got %v", notif["conversation_id"])
	}
	if notif["from"] != "alice@example.com" {
		t.Fatalf("expected alice@example.com, got %v", notif["from"])
	}
	if notif["delivered_to"] != "bot@agents.e2a.dev" {
		t.Fatalf("expected bot@agents.e2a.dev, got %v", notif["delivered_to"])
	}
	if notif["agent_email"] != "bot@agents.e2a.dev" {
		t.Fatalf("expected agent_email, got %v", notif["agent_email"])
	}
	if notif["direction"] != "inbound" {
		t.Fatalf("direction = %v, want inbound", notif["direction"])
	}
	if notif["subject"] != "Test Subject" {
		t.Fatalf("expected Test Subject, got %v", notif["subject"])
	}
	// The frame stays a metadata notification — never the message body.
	if _, hasRaw := notif["raw_message"]; hasRaw {
		t.Fatal("notification must not include raw_message; clients fetch via REST")
	}
	// Required fields stay present-but-empty (never absent) when the row
	// genuinely recorded no auth attestation.
	if v, ok := notif["authenticated_from"]; !ok || v != "" {
		t.Fatalf("authenticated_from should be present-but-empty when the row has no auth headers, got %v (present=%v)", v, ok)
	}
	if _, ok := notif["auth_headers"]; !ok {
		t.Fatal("auth_headers should be present (empty object) when the row has none")
	}
}

// TestBuildNotification_AuthFieldsFromRow pins the drain-path auth contract:
// messages.auth_headers IS persisted at intake and selected by the drain's
// list query, so the drain frame must carry the row's auth_headers and derive
// authenticated_from from its X-E2A-Auth-Sender value — the same gated
// identity a live delivery carries. Consumers are documented to GATE on
// authenticated_from, and the drain frame shares its deterministic event id
// with the webhook delivery, so a dedup-by-id consumer that saw an empty
// authenticated_from here first would permanently mistrust a verified message.
func TestBuildNotification_AuthFieldsFromRow(t *testing.T) {
	msg := &identity.Message{
		ID:           "msg_auth",
		Sender:       "reply@customer.example.com",
		Recipient:    "bot@agents.e2a.dev",
		Subject:      "Auth",
		ToRecipients: []string{"bot@agents.e2a.dev"},
		AuthHeaders: map[string]string{
			"X-E2A-Auth-Sender":   "alice@customer.example.com",
			"X-E2A-Auth-Verified": "true",
		},
		CreatedAt: time.Now(),
	}

	var env struct {
		Data struct {
			AuthenticatedFrom string            `json:"authenticated_from"`
			AuthHeaders       map[string]string `json:"auth_headers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(BuildNotification(msg), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.AuthenticatedFrom != "alice@customer.example.com" {
		t.Fatalf("authenticated_from = %q, want the row's X-E2A-Auth-Sender value", env.Data.AuthenticatedFrom)
	}
	if env.Data.AuthHeaders["X-E2A-Auth-Verified"] != "true" {
		t.Fatalf("auth_headers = %v, want the row's persisted attestation", env.Data.AuthHeaders)
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

	raw := BuildNotification(msg)
	var env struct {
		Data map[string]any `json:"data"`
	}
	json.Unmarshal(raw, &env)

	if _, exists := env.Data["conversation_id"]; exists {
		t.Fatal("expected conversation_id to be omitted when empty")
	}
}

// TestBuildNotification_GoldenParity locks the WS drain frame to the same
// golden fixture the webhook channel asserts against: given a message row
// carrying everything the fixture's payload needs, the frame's data must be
// byte-identical to the fixture's data — including authenticated_from, which
// is derived from the row's persisted auth_headers (X-E2A-Auth-Sender). The
// only genuine drain divergences (attachments omitted without raw_message,
// row-time timestamps) don't apply here because the test row carries the
// fixture's raw message and created_at.
func TestBuildNotification_GoldenParity(t *testing.T) {
	pdf := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 12345)))
	rawMsg := []byte("From: alice@customer.example.com\r\n" +
		"To: support@agents.example.com\r\n" +
		"Subject: Order #1234 delayed\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b1\"\r\n" +
		"\r\n" +
		"--b1\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"hello\r\n" +
		"--b1\r\n" +
		"Content-Type: application/pdf; name=\"invoice.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		pdf + "\r\n" +
		"--b1--\r\n")

	msg := &identity.Message{
		ID:             "msg_01h2xcejqtf2nbrexx3vqjhp41",
		Sender:         "reply@customer.example.com",
		Recipient:      "support@agents.example.com",
		Subject:        "Order #1234 delayed",
		ConversationID: "conv_9f8e7d6c",
		ToRecipients:   []string{"support@agents.example.com"},
		CC:             []string{"ops@customer.example.com"},
		ReplyTo:        []string{"reply@customer.example.com"},
		AuthHeaders: map[string]string{
			"X-E2A-Auth-Sender":   "alice@customer.example.com",
			"X-E2A-Auth-Verified": "true",
		},
		RawMessage: rawMsg,
		CreatedAt:  time.Date(2026, 7, 1, 10, 30, 0, 123456789, time.UTC),
	}

	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(BuildNotification(msg), &env); err != nil {
		t.Fatal(err)
	}

	want, err := os.ReadFile("../eventpayload/testdata/email.received.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(want, &fixture); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(env.Data)
	wantJSON, _ := json.Marshal(fixture.Data)
	if string(got) != string(wantJSON) {
		t.Errorf("WS drain frame drifted from the golden fixture\n got: %s\nwant: %s", got, wantJSON)
	}
}

// TestBuildNotification_GoldenParityMinimal locks the drain frame for a
// DRAIN-REALISTIC minimal row — no conversation, no cc/reply_to, no persisted
// auth attestation, and (as on the real drain query) no raw_message — to the
// committed required-fields-only fixture. This pins the omitempty presence
// semantics on the drain path: optional fields ABSENT, required fields
// present-but-empty (authenticated_from: "", auth_headers: {}).
func TestBuildNotification_GoldenParityMinimal(t *testing.T) {
	msg := &identity.Message{
		ID:           "msg_01h2xcejqtf2nbrexx3vqjhp41",
		Sender:       "reply@customer.example.com",
		Recipient:    "support@agents.example.com",
		Subject:      "Order #1234 delayed",
		ToRecipients: []string{"support@agents.example.com"},
		CreatedAt:    time.Date(2026, 7, 1, 10, 30, 0, 123456789, time.UTC),
	}

	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(BuildNotification(msg), &env); err != nil {
		t.Fatal(err)
	}

	want, err := os.ReadFile("../eventpayload/testdata/email.received.min.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(want, &fixture); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(env.Data)
	wantJSON, _ := json.Marshal(fixture.Data)
	if string(got) != string(wantJSON) {
		t.Errorf("WS drain frame drifted from the minimal golden fixture\n got: %s\nwant: %s", got, wantJSON)
	}
}
