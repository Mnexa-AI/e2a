package ws

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"nhooyr.io/websocket"
)

// HandlerStore is the subset of identity.Store that the WS handler needs.
type HandlerStore interface {
	GetUserByAPIKey(ctx context.Context, apiKey string) (*identity.User, error)
	GetAgentByEmail(ctx context.Context, email string) (*identity.AgentIdentity, error)
	GetMessagesByAgent(ctx context.Context, agentID, status, direction string, descending bool, limit int, afterTime time.Time, afterID string) ([]identity.Message, error)
}

// Handler upgrades HTTP connections to WebSocket for local-mode agents.
type Handler struct {
	hub   *Hub
	store HandlerStore
}

func NewHandler(hub *Hub, store HandlerStore) *Handler {
	return &Handler{hub: hub, store: store}
}

// Handle is the HTTP handler for WebSocket upgrade requests.
// Route: GET /api/v1/agents/{email}/ws?token={api_key}
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	// Authenticate via query param (WebSocket clients can't set headers during upgrade)
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "token query parameter required", http.StatusUnauthorized)
		return
	}

	user, err := h.store.GetUserByAPIKey(r.Context(), token)
	if err != nil || user == nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// Resolve agent and verify ownership
	email := mux.Vars(r)["email"]
	agent, err := h.store.GetAgentByEmail(r.Context(), email)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	if agent.UserID != user.ID {
		http.Error(w, "not authorized for this agent", http.StatusForbidden)
		return
	}
	if agent.AgentMode != "local" {
		http.Error(w, "WebSocket is only available for local-mode agents", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket. Origin checks are intentionally disabled
	// because authentication uses the `token=` query parameter (the
	// agent owner's API key), not cookies. Cross-origin WebSockets from
	// a malicious site would still need to know that token, and an
	// attacker who already has the token has full REST API access too.
	// CLI/SDK clients also have no Origin to check.
	//
	// The known tradeoff: tokens in URLs leak to access logs, browser
	// history, and Referer. Operators should scrub access logs and
	// avoid linking to WS URLs from any HTML page.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[ws] upgrade failed for %s: %v", email, err)
		return
	}

	log.Printf("[ws] connected: %s (user=%s)", email, user.ID)

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

// drainUnread sends notifications for all unread messages. Notifications are
// best-effort; messages are marked read only when fetched via the REST API.
func (h *Handler) drainUnread(agent *identity.AgentIdentity) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	messages, err := h.store.GetMessagesByAgent(ctx, agent.ID, "unread", "inbound", false, 100, time.Time{}, "")
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
