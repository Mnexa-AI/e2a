package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/headers"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/relay"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/ws"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
)

const TestHMACSecret = "test-hmac-secret-for-testing"

type E2ATestServer struct {
	HTTPServer *httptest.Server
	SMTPAddr   string
	Store      *identity.Store
	Signer     *headers.Signer
	WSHub      *ws.Hub
	smtpServer *relay.Server
}

func TestServer(t *testing.T, pool *pgxpool.Pool) *E2ATestServer {
	t.Helper()

	store := identity.NewStore(pool)
	signer := headers.NewSigner(TestHMACSecret)
	deliverer := webhook.NewDeliverer(false) // no HTTPS requirement in tests
	deliveryStore := webhook.NewDeliveryStore(pool)
	persistentDeliverer := webhook.NewPersistentDeliverer(deliverer, deliveryStore)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	// HTTP server
	router := mux.NewRouter()
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotency.NewStore(pool))
	api.RegisterRoutes(router)

	// WebSocket route for local-mode agents
	wsHub := ws.NewHub()
	wsHandler := ws.NewHandler(wsHub, store)
	api.RegisterWSRoute(router, wsHandler.Handle)

	httpServer := httptest.NewServer(router)

	// SMTP server on random port
	smtpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for SMTP: %v", err)
	}
	smtpAddr := smtpListener.Addr().String()
	smtpListener.Close()

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			ListenAddr: smtpAddr,
			Domain:     "test.e2a.dev",
		},
		Env: "development",
	}
	smtpServer := relay.NewServer(cfg, store, signer, persistentDeliverer, noopUsage, wsHub)

	go func() {
		if err := smtpServer.ListenAndServe(); err != nil {
			// Server closed is expected during cleanup
		}
	}()

	// Wait for SMTP server to be ready
	for i := 0; i < 50; i++ {
		conn, err := net.DialTimeout("tcp", smtpAddr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	ts := &E2ATestServer{
		HTTPServer: httpServer,
		SMTPAddr:   smtpAddr,
		Store:      store,
		Signer:     signer,
		WSHub:      wsHub,
		smtpServer: smtpServer,
	}

	t.Cleanup(func() {
		httpServer.Close()
		smtpServer.Close()
		wsHub.Close()
	})

	return ts
}

type ReceivedPayload struct {
	Body    webhook.Payload
	Headers http.Header
	RawBody []byte
}

type WebhookReceiverResult struct {
	Server   *httptest.Server
	mu       sync.Mutex
	payloads []ReceivedPayload
}

func (w *WebhookReceiverResult) Payloads() []ReceivedPayload {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make([]ReceivedPayload, len(w.payloads))
	copy(result, w.payloads)
	return result
}

func (w *WebhookReceiverResult) WaitForPayloads(t *testing.T, count int, timeout time.Duration) []ReceivedPayload {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		payloads := w.Payloads()
		if len(payloads) >= count {
			return payloads
		}
		time.Sleep(50 * time.Millisecond)
	}
	payloads := w.Payloads()
	if len(payloads) < count {
		t.Fatalf("expected %d webhook payloads, got %d after %v", count, len(payloads), timeout)
	}
	return payloads
}

func WebhookReceiver(t *testing.T) *WebhookReceiverResult {
	t.Helper()

	result := &WebhookReceiverResult{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", 500)
			return
		}
		var payload webhook.Payload
		if err := json.Unmarshal(rawBody, &payload); err != nil {
			http.Error(w, fmt.Sprintf("unmarshal error: %v", err), 400)
			return
		}

		result.mu.Lock()
		result.payloads = append(result.payloads, ReceivedPayload{
			Body:    payload,
			Headers: r.Header.Clone(),
			RawBody: rawBody,
		})
		result.mu.Unlock()

		w.WriteHeader(200)
	}))

	result.Server = server
	t.Cleanup(server.Close)

	return result
}
