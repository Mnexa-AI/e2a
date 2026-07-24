package selftest

// websocket_round_trip scenario tests, mock-driven like the rest of the
// internal failure-path suite: an httptest server stands in for the e2a WS
// endpoint + self-send API so connect, push, auth-reject, and no-frame
// timeout are each exercised without a DB.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// wsStub serves GET …/ws (upgrade, Bearer-checked) and POST …/messages
// (responds method=loopback and pushes an email.received envelope carrying
// the posted subject over the accepted socket).
func wsStub(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	var conn *websocket.Conn

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ws"):
			if r.Header.Get("Authorization") != "Bearer k" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			mu.Lock()
			conn = c
			mu.Unlock()
		case strings.HasSuffix(r.URL.Path, "/messages") && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			var in struct {
				Subject string `json:"subject"`
			}
			_ = json.Unmarshal(raw, &in)
			mu.Lock()
			c := conn
			mu.Unlock()
			if c != nil {
				env, _ := json.Marshal(map[string]any{
					"type": "email.received",
					"data": map[string]any{"subject": in.Subject},
				})
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = c.Write(ctx, websocket.MessageText, env)
				cancel()
			}
			w.Write([]byte(`{"method":"loopback"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return httptest.NewServer(mux)
}

func TestScenarioWebSocketRoundTrip(t *testing.T) {
	srv := wsStub(t)
	defer srv.Close()
	p := failProbe(srv.URL, "", nil)
	if r := scenarioWebSocketRoundTrip(context.Background(), p); r.Status != StatusPass {
		t.Errorf("happy path: status = %s (%q), want pass", r.Status, r.Detail)
	}
}

func TestScenarioWebSocketRoundTrip_Fail(t *testing.T) {
	// Handshake rejected (bad credential) → fail.
	srv := wsStub(t)
	defer srv.Close()
	p := failProbe(srv.URL, "", nil)
	p.APIKey = "wrong"
	mustFail(t, "ws 401", scenarioWebSocketRoundTrip(context.Background(), p))

	// Connects but no frame ever arrives (self-send accepted, push lost) →
	// fail on the round-trip timeout, not hang.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ws") {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err == nil {
				_ = c // hold open, push nothing
			}
			return
		}
		w.Write([]byte(`{"method":"loopback"}`))
	})
	silent := httptest.NewServer(mux)
	defer silent.Close()
	p2 := failProbe(silent.URL, "", nil)
	start := time.Now()
	mustFail(t, "no frame", scenarioWebSocketRoundTrip(context.Background(), p2))
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("no-frame case took %s, want bounded by the probe timeout", elapsed)
	}

	// Self-send rejected → fail.
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ws") {
			_, _ = websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})
	deny := httptest.NewServer(mux2)
	defer deny.Close()
	mustFail(t, "self-send 403", scenarioWebSocketRoundTrip(context.Background(), failProbe(deny.URL, "", nil)))
}

func TestScenarioMCPHTTPRoundTrip_RequiredNotConfigured(t *testing.T) {
	// E2A_PROBE_REQUIRE_MCP: an unset MCP URL must FAIL instead of
	// skip-as-pass, so a misconfigured prod prober can't stay silently green
	// while never probing MCP.
	p := failProbe("http://127.0.0.1:1", "", nil)
	p.RequireMCP = true
	mustFail(t, "require-mcp unset URL", scenarioMCPHTTPRoundTrip(context.Background(), p))
}
