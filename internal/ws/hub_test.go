package ws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// newTestWSPair creates a connected client/server websocket pair.
func newTestWSPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	var serverConn *websocket.Conn
	ready := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		serverConn, err = websocket.Accept(w, r, nil)
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		close(ready)
		// Keep open until test closes it
		select {}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("timeout waiting for server accept")
	}

	return clientConn, serverConn
}

func TestHubRegisterAndIsConnected(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	if hub.IsConnected("agent1") {
		t.Fatal("expected not connected before register")
	}

	_, serverConn := newTestWSPair(t)
	hub.Register("agent1", serverConn)

	if !hub.IsConnected("agent1") {
		t.Fatal("expected connected after register")
	}
}

func TestHubUnregisterMatchingConn(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	_, conn := newTestWSPair(t)
	hub.Register("agent1", conn)
	hub.Unregister("agent1", conn)

	if hub.IsConnected("agent1") {
		t.Fatal("expected disconnected after unregister")
	}
}

func TestHubUnregisterNonMatchingConn(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	_, conn1 := newTestWSPair(t)
	_, conn2 := newTestWSPair(t)

	hub.Register("agent1", conn1)
	// Unregister with wrong conn should not remove
	hub.Unregister("agent1", conn2)

	if !hub.IsConnected("agent1") {
		t.Fatal("expected still connected when unregister conn doesn't match")
	}
}

func TestHubRegisterReplacesOld(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	_, conn1 := newTestWSPair(t)
	_, conn2 := newTestWSPair(t)

	hub.Register("agent1", conn1)
	old := hub.Register("agent1", conn2)

	if old != conn1 {
		t.Fatal("expected old connection returned")
	}
	if !hub.IsConnected("agent1") {
		t.Fatal("expected connected with new conn")
	}
}

func TestHubSend(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	clientConn, serverConn := newTestWSPair(t)
	hub.Register("agent1", serverConn)

	msg := []byte(`{"message_id":"msg_1"}`)
	if !hub.Send("agent1", msg) {
		t.Fatal("expected send to succeed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(data) != string(msg) {
		t.Fatalf("got %q, want %q", data, msg)
	}
}

func TestHubSendNotConnected(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	if hub.Send("nobody", []byte("hello")) {
		t.Fatal("expected send to fail for unregistered agent")
	}
}

func TestHubClose(t *testing.T) {
	hub := NewHub()

	clientConn, conn := newTestWSPair(t)
	hub.Register("agent1", conn)
	hub.Close()

	if hub.IsConnected("agent1") {
		t.Fatal("expected no connections after close")
	}

	// Contract: shutdown closes with 1001 (going away) + the stable
	// "shutting_down" reason token — clients reconnect with backoff.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := clientConn.Read(ctx)
	if err == nil {
		t.Fatal("expected the client connection to be closed")
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
		t.Fatalf("close code = %d, want %d (going away) — err: %v", got, websocket.StatusGoingAway, err)
	}
	var ce websocket.CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("expected a websocket.CloseError, got %v", err)
	}
	if ce.Reason != ReasonShuttingDown {
		t.Fatalf("close reason = %q, want the stable token %q", ce.Reason, ReasonShuttingDown)
	}
}

func TestHubConcurrency(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, conn := newTestWSPair(t)
			hub.Register("agent1", conn)
			hub.IsConnected("agent1")
			hub.Send("agent1", []byte("test"))
		}(i)
	}
	wg.Wait()
}
