package ws

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/eventpayload"
	"github.com/Mnexa-AI/e2a/internal/headers"
	"github.com/Mnexa-AI/e2a/internal/httpapi"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
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

// ServeWithEmail handles the WebSocket upgrade for an explicitly-provided
// agent email — the router owns path extraction (and, for routers like chi
// that return params still percent-encoded, DECODING; see the /v1 mount in
// internal/httpapi). The old gorilla/mux `Handle` variant was removed with
// the retired /api/v1 surface: this is the transport's only entry point.
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
	// Handshake rejections happen BEFORE the WebSocket upgrade (the connection
	// is still a plain HTTP request, not yet hijacked), so they return the same
	// canonical error envelope every /v1 REST endpoint does — httpapi.WriteError
	// emits {error:{code,message,request_id}} + the X-Request-Id header, reusing
	// the request id the chi-root middleware already stamped. A client's shared
	// envelope-based error handling then works identically on a failed WS
	// handshake and a failed REST call. Status codes are UNCHANGED from the prior
	// behavior. WWW-Authenticate (RFC 6750 §3) is set before WriteError writes the
	// status line, since headers can't be added once the status is flushed; on the
	// prod stack the authChallenge middleware also (re)asserts it on any 401.
	token := bearerToken(r)
	if token == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="e2a"`)
		httpapi.WriteError(w, r, http.StatusUnauthorized, "unauthorized",
			"missing credential — send Authorization: Bearer <api_key>")
		return
	}

	principal, err := h.store.GetPrincipalByAPIKey(r.Context(), token)
	if err != nil || principal == nil || principal.User == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="e2a"`)
		httpapi.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}

	// Resolve agent and verify ownership. Canonicalize the email so that
	// `ws://.../UPPER@x.dev/ws?token=…` resolves identically to the
	// lower-case form — matches the REST API's `normalizeEmail` policy.
	email := identity.NormalizeEmail(rawEmail)
	agent, err := h.store.GetAgentByEmail(r.Context(), email)
	if err != nil {
		httpapi.WriteError(w, r, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	// Tenant ownership: the agent must belong to the credential's user. A
	// cross-tenant miss returns 404 not_found — the SAME response as a
	// nonexistent agent above — so an authenticated caller can't tell "this
	// address doesn't exist" from "it exists but isn't yours". Emitting 403
	// here would make the WS handshake an agent-existence enumeration oracle
	// across tenants; collapsing both into 404 closes it (mirrors the REST
	// resolveOwnedAgent, which likewise refuses to distinguish the two).
	if agent.UserID != principal.User.ID {
		httpapi.WriteError(w, r, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	// Agent-scope confinement (HIGH-1): an agent-scoped credential is pinned to
	// its one bound agent — it may not open a different agent's stream even
	// within the same account. Mirrors the REST resolveOwnedAgent pin.
	if principal.Scope == identity.ScopeAgent && principal.AgentID != agent.ID {
		httpapi.WriteError(w, r, http.StatusForbidden, "forbidden", "not authorized for this agent")
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

// BuildNotification renders the WS frame for one inbound message: the SAME
// versioned event envelope the webhook channel delivers —
// {type:"email.received", id, schema_version, created_at, data} with the
// canonical typed eventpayload.EmailReceivedData — so a consumer can share one
// parser across both channels. The event id is the same deterministic
// derivation the outbox uses (sha256(message_id|type)), so a subscriber that
// receives the message on both channels can dedup on it, and the drain path
// re-emits a byte-stable id across reconnects.
//
// This drain-path rebuild populates what the message ROW provides, including
// the persisted signed auth attestation: auth_headers is stored at intake
// (messages.auth_headers) and selected by the drain's list query, and
// authenticated_from is derived from its X-E2A-Auth-Sender value — the same
// identity the live path carries. A consumer that gates on authenticated_from
// therefore gets the SAME verdict whether the frame arrives live or on a
// drain (critical for dedup-by-id consumers: the drain frame shares the
// deterministic event id with the webhook delivery, so a mistrusted drain
// frame would permanently mistrust a verified message).
//
// The only genuine drain divergences from the live event are:
//   - attachments: raw_message is not selected by the drain's list query →
//     omitted (fetch the message for attachment metadata + bytes).
//   - timestamps: created_at / received_at are the message ROW's created_at,
//     not the original event's publish time.
//
// The live relay path (internal/relay) marshals the actual outbox event: the
// same event envelope — identical fields and event id — as the webhook
// delivery (byte layout may differ across channels; JSON key order/escaping
// is not contractual).
func BuildNotification(msg *identity.Message) []byte {
	authHeaders := msg.AuthHeaders
	if authHeaders == nil {
		authHeaders = map[string]string{}
	}
	to := msg.ToRecipients
	if to == nil {
		to = []string{}
	}
	ev := webhookpub.Event{
		ID:        webhookpub.DeterministicEventID(msg.ID, webhookpub.EventEmailReceived),
		Type:      webhookpub.EventEmailReceived,
		CreatedAt: msg.CreatedAt.UTC(),
		Data: eventpayload.EmailReceivedData{
			MessageID:         msg.ID,
			AgentEmail:        msg.Recipient,
			Direction:         "inbound",
			ConversationID:    msg.ConversationID,
			From:              msg.Sender,
			AuthenticatedFrom: authHeaders[headers.HeaderSender],
			To:                to,
			CC:                msg.CC,
			ReplyTo:           msg.ReplyTo,
			DeliveredTo:       msg.Recipient,
			Subject:           msg.Subject,
			AuthHeaders:       authHeaders,
			ReceivedAt:        msg.CreatedAt.UTC(),
			Attachments:       eventpayload.AttachmentMetadata(msg.RawMessage),
		},
	}
	data, _ := json.Marshal(ev.AsEnvelope())
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
