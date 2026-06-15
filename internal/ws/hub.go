package ws

import (
	"context"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

// Hub manages WebSocket connections for agents that live-tail inbound
// mail. Any agent may connect — WS is an opportunistic push on top of
// the durable pollable inbox + webhook subscriptions.
// One connection per agent; new connections replace old ones.
type Hub struct {
	mu    sync.RWMutex
	conns map[string]*websocket.Conn // agentID → conn
}

func NewHub() *Hub {
	return &Hub{
		conns: make(map[string]*websocket.Conn),
	}
}

// Register stores a connection for the given agent, returning any previous connection.
// The caller should close the old connection if non-nil.
func (h *Hub) Register(agentID string, conn *websocket.Conn) (old *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	old = h.conns[agentID]
	h.conns[agentID] = conn
	return old
}

// Unregister removes the connection for the given agent.
// Only removes if the current connection matches (prevents race with re-register).
func (h *Hub) Unregister(agentID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[agentID] == conn {
		delete(h.conns, agentID)
	}
}

// Send writes a message to the agent's WebSocket connection.
// Returns true if the message was sent successfully.
func (h *Hub) Send(agentID string, msg []byte) bool {
	h.mu.RLock()
	conn := h.conns[agentID]
	h.mu.RUnlock()

	if conn == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := conn.Write(ctx, websocket.MessageText, msg)
	return err == nil
}

// IsConnected returns whether an agent has an active WebSocket connection.
func (h *Hub) IsConnected(agentID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[agentID] != nil
}

// Close closes all active connections.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, conn := range h.conns {
		conn.Close(websocket.StatusGoingAway, "server shutting down")
		delete(h.conns, id)
	}
}
