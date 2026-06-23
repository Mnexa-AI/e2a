package ws

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/gorilla/mux"
	"nhooyr.io/websocket"
)

// HandlerStore is the subset of identity.Store that the WS handler needs.
type HandlerStore interface {
	// GetPrincipalByAPIKey returns the scoped principal (User + Scope + AgentID)
	// so the WS transport can enforce the SAME agent-scope confinement the REST
	// API does (HIGH-1): an agent-scoped key must not open another agent's stream.
	GetPrincipalByAPIKey(ctx context.Context, apiKey string) (*identity.Principal, error)
	GetAgentByEmail(ctx context.Context, email string) (*identity.AgentIdentity, error)
	GetMessagesByAgent(ctx context.Context, f identity.MessageListFilter) ([]identity.Message, error)
}

// Handler upgrades HTTP connections to WebSocket. Open to any agent the
// caller owns — the legacy local-mode gate was removed (migration 029).
type Handler struct {
	hub   *Hub
	store HandlerStore
}

func NewHandler(hub *Hub, store HandlerStore) *Handler {
	return &Handler{hub: hub, store: store}
}

// Handle is the HTTP handler for WebSocket upgrade requests.
// Route: GET /v1/agents/{email}/ws — authenticated by `Authorization: Bearer <api_key>`.
// Handle reads the agent email from the gorilla/mux route var.
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, mux.Vars(r)["email"])
}

// ServeWithEmail handles the WebSocket upgrade for an explicitly-provided
// agent email, so non-mux routers (chi at /v1/agents/{address}/ws) can host
// the same transport.
func (h *Handler) ServeWithEmail(w http.ResponseWriter, r *http.Request, rawEmail string) {
	h.serve(w, r, rawEmail)
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, rawEmail string) {
	// Authenticate via the `Authorization: Bearer <api_key>` handshake header —
	// the standard shape for non-browser WebSocket clients (matches OpenAI
	// Realtime server-side, Slack Socket Mode, Kubernetes). The credential never
	// touches the URL, so it can't leak to access logs / browser history /
	// Referer the way a `?token=` query param does. All e2a WS clients (the TS +
	// Python SDKs and the CLI) are non-browser and set the header on the
	// handshake. (A short-lived connect ticket is the path to add later if an
	// in-browser client ever needs to connect — browsers can't set headers.)
	token := bearerToken(r)
	if token == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="e2a"`)
		http.Error(w, "missing credential — send Authorization: Bearer <api_key>", http.StatusUnauthorized)
		return
	}

	principal, err := h.store.GetPrincipalByAPIKey(r.Context(), token)
	if err != nil || principal == nil || principal.User == nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// Resolve agent and verify ownership. Canonicalize the email so that
	// `ws://.../UPPER@x.dev/ws?token=…` resolves identically to the
	// lower-case form — matches the REST API's `normalizeEmail` policy.
	email := identity.NormalizeEmail(rawEmail)
	agent, err := h.store.GetAgentByEmail(r.Context(), email)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	// Tenant ownership: the agent must belong to the credential's user.
	if agent.UserID != principal.User.ID {
		http.Error(w, "not authorized for this agent", http.StatusForbidden)
		return
	}
	// Agent-scope confinement (HIGH-1): an agent-scoped credential is pinned to
	// its one bound agent — it may not open a different agent's stream even
	// within the same account. Mirrors the REST resolveOwnedAgent pin.
	if principal.Scope == identity.ScopeAgent && principal.AgentID != agent.ID {
		http.Error(w, "not authorized for this agent", http.StatusForbidden)
		return
	}

	// Upgrade to WebSocket. Origin checks are intentionally disabled because
	// authentication is the `Authorization: Bearer` header, not cookies — and a
	// browser cannot set that header on a cross-site WebSocket, so there is no
	// cross-site-WebSocket-hijacking (CSWSH) vector to defend against here.
	// CLI/SDK clients have no Origin to check. (Header auth is strictly safer
	// than the old `?token=` query param it replaced, which leaked the key to
	// access logs / history / Referer.)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[ws] upgrade failed for %s: %v", email, err)
		return
	}

	log.Printf("[ws] connected: %s (user=%s)", email, principal.User.ID)

	// Register connection; close old one if exists
	if old := h.hub.Register(agent.ID, conn); old != nil {
		log.Printf("[ws] replacing existing connection for %s", email)
		old.Close(websocket.StatusPolicyViolation, "replaced by new connection")
	}

	// Drain unread messages as notifications
	h.drainUnread(agent)

	// Read loop: keepalive + disconnect detection
	h.readLoop(conn, agent)

	// Cleanup on disconnect
	h.hub.Unregister(agent.ID, conn)
	conn.Close(websocket.StatusNormalClosure, "")
	log.Printf("[ws] disconnected: %s", email)
}

// bearerToken extracts the API key from the `Authorization: Bearer <key>`
// handshake header (scheme match is case-insensitive per RFC 7235). Returns ""
// when the header is absent or not a Bearer credential.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// drainUnread sends notifications for all unread messages. Notifications are
// best-effort; messages are marked read only when fetched via the REST API.
func (h *Handler) drainUnread(agent *identity.AgentIdentity) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	messages, err := h.store.GetMessagesByAgent(ctx, identity.MessageListFilter{
		AgentID:   agent.ID,
		Status:    "unread",
		Direction: "inbound",
		Limit:     100,
	})
	if err != nil {
		log.Printf("[ws] drain error for %s: %v", agent.ID, err)
		return
	}

	for _, msg := range messages {
		notification := BuildNotification(&msg)
		h.hub.Send(agent.ID, notification)
	}

	if len(messages) > 0 {
		log.Printf("[ws] drained %d unread messages for %s", len(messages), agent.ID)
	}
}

// Notification is the lightweight JSON payload sent over WebSocket when a new
// message arrives. It contains only metadata — the full message (including
// the To/Cc lists) is fetched via REST.
type Notification struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id,omitempty"`
	From           string    `json:"from"`
	Recipient      string    `json:"recipient"`
	Subject        string    `json:"subject"`
	ReceivedAt     time.Time `json:"received_at"`
}

// BuildNotification creates a lightweight JSON notification from a message.
func BuildNotification(msg *identity.Message) []byte {
	n := Notification{
		MessageID:      msg.ID,
		ConversationID: msg.ConversationID,
		From:           msg.Sender,
		Recipient:      msg.Recipient,
		Subject:        msg.Subject,
		ReceivedAt:     msg.CreatedAt,
	}
	data, _ := json.Marshal(n)
	return data
}

// readLoop blocks until the client disconnects or sends a close frame.
// It ignores client messages and sends periodic pings.
func (h *Handler) readLoop(conn *websocket.Conn, agent *identity.AgentIdentity) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ping goroutine: send a ping every 30 seconds, fail if no pong within 10 seconds.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					log.Printf("[ws] ping failed for %s: %v", agent.Email, err)
					// Close the connection to unblock the Read below
					conn.Close(websocket.StatusGoingAway, "ping timeout")
					return
				}
			}
		}
	}()

	// Read loop: consume client messages and control frames until disconnect.
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			return
		}
	}
}
