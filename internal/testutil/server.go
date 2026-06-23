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
	"github.com/Mnexa-AI/e2a/internal/apiserver"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/headers"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/relay"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/Mnexa-AI/e2a/internal/ws"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
)

const TestHMACSecret = "test-hmac-secret-for-testing"

// testServerOpts collects optional knobs callers can flip without
// growing the TestServer call site. Add fields here rather than new
// constructor parameters — every existing caller stays untouched.
type testServerOpts struct {
	outboundSMTPHost       string
	outboundSMTPPort       int
	outboundSMTPFromDomain string
}

type TestServerOption func(*testServerOpts)

// WithOutboundSMTP wires an upstream relay for /send + HITL approve
// paths so they don't error with "outbound SMTP relay not configured".
// Tests that don't trigger outbound omit this. Pointing at Mailpit on
// localhost:1025 (started via `make docker-up`) is the typical pattern.
func WithOutboundSMTP(host string, port int, fromDomain string) TestServerOption {
	return func(o *testServerOpts) {
		o.outboundSMTPHost = host
		o.outboundSMTPPort = port
		o.outboundSMTPFromDomain = fromDomain
	}
}

type E2ATestServer struct {
	HTTPServer *httptest.Server
	SMTPAddr   string
	Store      *identity.Store
	Signer     *headers.Signer
	WSHub      *ws.Hub
	smtpServer *relay.Server

	// Webhooks-as-a-resource wiring (post-PR-180). Tests that exercise
	// the new path can read these directly without re-deriving them.
	// Publisher is wired into both the agent API (so /send etc. fire
	// email.sent) and the SMTP server (so inbound mail fires
	// email.received). SubscriberStore + Worker let tests insert /
	// inspect delivery rows and force a drain without waiting on the
	// 30s production tick.
	Publisher        webhookpub.Publisher
	SubscriberStore  *webhook.SubscriberStore
	SubscriberWorker *webhook.SubscriberRetryWorker
}

func TestServer(t *testing.T, pool *pgxpool.Pool, opts ...TestServerOption) *E2ATestServer {
	t.Helper()
	o := testServerOpts{}
	for _, opt := range opts {
		opt(&o)
	}

	store := identity.NewStore(pool)
	signer := headers.NewSigner(TestHMACSecret)
	outboundCfg := &config.OutboundSMTPConfig{
		Host: o.outboundSMTPHost,
		Port: o.outboundSMTPPort,
	}
	fromDomain := "test.e2a.dev"
	if o.outboundSMTPFromDomain != "" {
		fromDomain = o.outboundSMTPFromDomain
	}
	smtpRelay := outbound.NewSMTPRelay(outboundCfg)
	sender := outbound.NewSender(smtpRelay, fromDomain)

	// Webhooks-resource (PR-180) wiring. The publisher fans events
	// to enabled subscribers; the subscriber store is what the /test
	// + /deliveries handlers read. We wire them into both the API
	// and the relay so trigger sites fire events. The retry worker
	// is constructed but NOT started — tests call Tick(ctx) directly
	// for deterministic delivery without the 30s tick.
	subscriberStore := webhook.NewSubscriberStore(pool)
	subscriberDeliverer := webhook.NewSubscriberDeliverer(false)
	subscriberWorker := webhook.NewSubscriberRetryWorker(subscriberStore, subscriberDeliverer, store)
	publisher := webhookpub.New(store, webhookpub.NewDBInserter(pool), webhookpub.StaticFlag(true))

	// HTTP server
	router := mux.NewRouter()
	noopUsage := usage.NewNoopUsageTracker()
	usageStore := usage.NewStore(pool)
	// Generous caps — e2e exercises behavior, not quota enforcement.
	enforcer := limits.NewEnforcer(limits.NewStore(pool), usageStore, limits.Defaults{
		PlanCode: "test", MaxAgents: 100000, MaxDomains: 100000,
		MaxMessagesMonth: 100000, MaxStorageBytes: 1 << 40,
	}, time.Minute)
	idempotencyStore := idempotency.NewStore(pool)
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotencyStore)
	api.SetSubscriberStore(subscriberStore)
	api.SetPublisher(publisher)
	api.SetEnforcer(enforcer)
	api.SetUsageStore(usageStore)
	api.RegisterRoutes(router)

	// WebSocket live-tail transport — wired as a /v1 route via WSHandle below.
	wsHub := ws.NewHub()
	wsHandler := ws.NewHandler(wsHub, store)

	// Wrap the legacy mux with the typed /v1 surface (the same apiserver
	// builder prod + StartContractServer use) so e2e exercises the real /v1
	// handler; the remaining non-v1 routes (oauth/auth/health) fall through to the mux.
	v1 := apiserver.New(apiserver.Params{
		API: api, Store: store, Enforcer: enforcer, UsageStore: usageStore,
		SubscriberStore: subscriberStore, Idempotency: idempotencyStore, Pool: pool,
		SMTPDomain: "test.e2a.dev", SharedDomain: "agents.e2a.dev",
		PublicURL: "http://127.0.0.1", Production: false,
		Legacy: router, WSHandle: wsHandler.ServeWithEmail,
	})

	httpServer := httptest.NewServer(v1)

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
	smtpServer := relay.NewServer(cfg, store, signer, noopUsage, wsHub)
	smtpServer.SetPublisher(publisher)

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
		HTTPServer:       httpServer,
		SMTPAddr:         smtpAddr,
		Store:            store,
		Signer:           signer,
		WSHub:            wsHub,
		smtpServer:       smtpServer,
		Publisher:        publisher,
		SubscriberStore:  subscriberStore,
		SubscriberWorker: subscriberWorker,
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

// SubscriberCaptured is one POST the SubscriberReceiver caught. The
// envelope is the parsed JSON body ({event, id, created_at, data})
// and RawBody is the verbatim bytes — useful for HMAC verification,
// which signs `t.body` and must use the exact bytes the worker POSTed.
type SubscriberCaptured struct {
	URL      string
	Envelope map[string]any
	RawBody  []byte
	Headers  http.Header
}

// SubscriberReceiverResult is a multi-path receiver for the new
// webhooks-as-a-resource path. Distinct from WebhookReceiverResult
// (which decodes the legacy webhook.Payload shape) because the new
// envelope is {event, id, created_at, data} and we need raw bytes
// for signature verification.
type SubscriberReceiverResult struct {
	Server   *httptest.Server
	mu       sync.Mutex
	captured []SubscriberCaptured
	// status is per-path: if absent, 200. Used by the auto-disable
	// test to force 503 on one route.
	statusByPath map[string]int
}

func (s *SubscriberReceiverResult) Captured() []SubscriberCaptured {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SubscriberCaptured, len(s.captured))
	copy(out, s.captured)
	return out
}

// SetStatus pins a non-200 response for the given path. Used by the
// auto-disable test to force the worker into the failure path.
func (s *SubscriberReceiverResult) SetStatus(path string, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statusByPath == nil {
		s.statusByPath = map[string]int{}
	}
	s.statusByPath[path] = code
}

// Reset clears captured payloads. Useful between phases of a long
// test so per-phase assertions don't see prior posts.
func (s *SubscriberReceiverResult) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captured = nil
}

// WaitFor polls until predicate returns true or the timeout expires.
// Returns the captured list at the moment predicate first matched (or
// the last seen list on timeout). Tests typically call Tick(ctx) on
// the worker once then WaitFor(..., 0) for an immediate check; the
// timeout exists for cases where the publisher may still be in flight.
func (s *SubscriberReceiverResult) WaitFor(t *testing.T, timeout time.Duration, predicate func([]SubscriberCaptured) bool) []SubscriberCaptured {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := s.Captured()
		if predicate(got) {
			return got
		}
		if time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// SubscriberReceiver returns a multi-path HTTP receiver wired for the
// new webhook-resource envelope. Routes work as plain paths under the
// receiver's base URL — e.g. receiver.Server.URL + "/sent" + ".../fail".
func SubscriberReceiver(t *testing.T) *SubscriberReceiverResult {
	t.Helper()
	result := &SubscriberReceiverResult{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read err", 500)
			return
		}
		var env map[string]any
		_ = json.Unmarshal(raw, &env) // tolerate non-JSON for negative cases

		result.mu.Lock()
		result.captured = append(result.captured, SubscriberCaptured{
			URL:      r.URL.Path,
			Envelope: env,
			RawBody:  raw,
			Headers:  r.Header.Clone(),
		})
		status := 200
		if s, ok := result.statusByPath[r.URL.Path]; ok {
			status = s
		}
		result.mu.Unlock()
		w.WriteHeader(status)
	}))
	result.Server = server
	t.Cleanup(server.Close)
	return result
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
