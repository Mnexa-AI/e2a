//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/webhookdelivery"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// e2e tests covering slices 1-9 work end-to-end against a real DB +
// HTTP server, exercising the outbox path with WEBHOOKS_OUTBOX_ENABLED.
//
// Run with:
//   make docker-up   # postgres on :5433
//   E2A_TEST_DATABASE_URL=postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable \
//     go test -tags integration -v ./internal/e2e/...
//
// Coverage:
//   * Outbox writer (PublishTx) → worker fan-out → River DeliverWorker
//     POST → mock receiver (full round-trip)
//   * REST API: GET /events with pagination + filters
//   * REST API: GET /events/{id} happy path + 404 + 410
//   * REST API: POST /events/{id}/redeliver (targeted + fan-out)
//   * Concurrent publishes (deterministic ID dedup)
//   * Concurrent reads (handler thread safety)
//   * Auth boundaries (cross-user isolation)

// eventsE2EFixture is a self-contained DB + HTTP harness for the e2e
// tests in this file. Each test creates a fresh one; teardown wipes
// every seeded row.
type eventsE2EFixture struct {
	t              *testing.T
	pool           *pgxpool.Pool
	store          *identity.Store
	outbox         webhookpub.Outbox
	worker         *webhookpub.OutboxWorker
	deliverWorker  *webhookdelivery.DeliverWorker
	httpSrv        *httptest.Server
	cleanupUserIDs []string
}

// deliverPending runs the River DeliverWorker over every pending Layer 2 row —
// the test-side stand-in for the queued delivery jobs. Constructs a job the same
// way River would (attempt 1) and ignores per-row retryable failures so one
// failing subscriber doesn't stop the rest.
func (f *eventsE2EFixture) deliverPending(ctx context.Context) {
	rows, err := f.pool.Query(ctx,
		`SELECT id FROM webhook_subscriber_deliveries WHERE status = 'pending'`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		job := &river.Job[webhookdelivery.WebhookDeliverArgs]{
			JobRow: &rivertype.JobRow{
				Attempt:     1,
				MaxAttempts: webhookdelivery.MaxDeliveryAttempts,
				Kind:        webhookdelivery.WebhookDeliverArgs{}.Kind(),
			},
			Args: webhookdelivery.WebhookDeliverArgs{DeliveryID: id},
		}
		_ = f.deliverWorker.Work(ctx, job)
	}
}

func newEventsFixture(t *testing.T) *eventsE2EFixture {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	outbox := webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true))
	worker := webhookpub.NewOutboxWorker(pool, store)
	subStore := webhook.NewSubscriberStore(pool)
	subDeliverer := webhook.NewSubscriberDeliverer(false, "")
	deliverWorker := webhookdelivery.NewDeliverWorker(subStore, subDeliverer, store)

	// HTTP server wired with the agent API for the events endpoints.
	srv := testutil.TestServer(t, pool)
	srv.HTTPServer.Close() // we'll wrap with our own server below
	_ = srv

	// Build a minimal agent.API directly so we can wire the outbox and
	// eventsPool for the slice 6/7 handlers. The existing TestServer
	// doesn't wire those today.
	fix := &eventsE2EFixture{t: t, pool: pool, store: store, outbox: outbox, worker: worker, deliverWorker: deliverWorker}
	fix.httpSrv = testutil.NewEventsAPIHarness(t, pool, store, outbox)
	return fix
}

func (f *eventsE2EFixture) Close() {
	ctx := context.Background()
	for _, uid := range f.cleanupUserIDs {
		// Cascade cleans webhooks, webhook_events, deliveries.
		f.pool.Exec(ctx, `DELETE FROM webhook_events WHERE user_id = $1`, uid)
		f.pool.Exec(ctx, `DELETE FROM webhook_subscriber_deliveries
		                  WHERE webhook_id IN (SELECT id FROM webhooks WHERE user_id = $1)`, uid)
		f.pool.Exec(ctx, `DELETE FROM webhooks WHERE user_id = $1`, uid)
		f.pool.Exec(ctx, `DELETE FROM api_keys WHERE user_id = $1`, uid)
		f.pool.Exec(ctx, `DELETE FROM agent_identities WHERE user_id = $1`, uid)
		f.pool.Exec(ctx, `DELETE FROM domains WHERE user_id = $1`, uid)
		f.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, uid)
	}
	if f.httpSrv != nil {
		f.httpSrv.Close()
	}
}

func (f *eventsE2EFixture) seedUser(slug string) string {
	f.t.Helper()
	uid := "u_" + slug
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, name, google_subject, created_at)
		 VALUES ($1, $2, $3, $1, now())
		 ON CONFLICT (id) DO NOTHING`,
		uid, uid+"@example.com", slug)
	if err != nil {
		f.t.Fatalf("seed user: %v", err)
	}
	f.cleanupUserIDs = append(f.cleanupUserIDs, uid)
	return uid
}

func (f *eventsE2EFixture) seedAgent(userID, slug string) string {
	f.t.Helper()
	domain := slug + ".example.com"
	agentEmail := "agent@" + domain
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO domains (domain, user_id, verified, verification_token, created_at)
		 VALUES ($1, $2, true, 'tkn', now())
		 ON CONFLICT (domain) DO NOTHING`,
		domain, userID)
	if err != nil {
		f.t.Fatalf("seed domain: %v", err)
	}
	if _, err := f.store.CreateAgent(context.Background(), agentEmail, domain, "E2E Agent",
		"https://test.example.com/wh", "cloud", userID); err != nil {
		f.t.Fatalf("seed agent: %v", err)
	}
	return agentEmail
}

func (f *eventsE2EFixture) issueAPIKey(userID string) string {
	f.t.Helper()
	key, err := f.store.CreateAPIKey(context.Background(), userID, "e2e test", nil)
	if err != nil {
		f.t.Fatalf("create api key: %v", err)
	}
	return key.PlaintextKey
}

func (f *eventsE2EFixture) seedWebhook(userID, url string, events []string) string {
	f.t.Helper()
	wh, err := f.store.CreateWebhook(context.Background(), userID, url, "e2e", events, identity.WebhookFilters{})
	if err != nil {
		f.t.Fatalf("create webhook: %v", err)
	}
	return wh.ID
}

// seedMessage inserts a minimal messages row so the FK on
// webhook_events.message_id is satisfied. Production triggers always
// run messages INSERT + outbox INSERT in the same tx; tests do them
// separately for fixture simplicity.
func (f *eventsE2EFixture) seedMessage(messageID, agentID, direction string) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO messages (id, agent_id, direction, sender, recipient, subject, created_at, expires_at)
		 VALUES ($1, $2, $3, 'sender@e2e.example', 'agent@e2e.example', 'e2e', now(), now() + interval '30 days')
		 ON CONFLICT (id) DO NOTHING`,
		messageID, agentID, direction); err != nil {
		f.t.Fatalf("seed message: %v", err)
	}
}

// publishEvent writes the outbox row + drives the worker to fan it out.
// In production these are async; tests drive them synchronously so we
// don't have to wait on the 1s poll. Auto-seeds a messages row when
// the event has a non-empty MessageID so the FK is satisfied.
func (f *eventsE2EFixture) publishEvent(ctx context.Context, e webhookpub.Event) error {
	if e.MessageID != "" && e.AgentID != "" {
		f.seedMessage(e.MessageID, e.AgentID, "inbound")
	}
	err := f.store.WithTx(ctx, func(tx pgx.Tx) error {
		return f.outbox.PublishTx(ctx, tx, e)
	})
	if err != nil {
		return err
	}
	f.worker.Tick(ctx)
	f.deliverPending(ctx)
	return nil
}

func (f *eventsE2EFixture) seedExpiredEvent(userID, eventID, eventType string) {
	f.t.Helper()
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO webhook_events
		     (id, user_id, type, envelope, status, expires_at)
		 VALUES ($1, $2, $3, '{}'::jsonb, 'pending', now() - interval '1 day')`,
		eventID, userID, eventType)
	if err != nil {
		f.t.Fatalf("seed expired event: %v", err)
	}
}

func (f *eventsE2EFixture) httpGet(path, apiKey string) *http.Response {
	f.t.Helper()
	req, _ := http.NewRequest("GET", f.httpSrv.URL+path, nil)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (f *eventsE2EFixture) httpPost(path, apiKey string, body []byte) *http.Response {
	f.t.Helper()
	req, _ := http.NewRequest("POST", f.httpSrv.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (f *eventsE2EFixture) countEvents(eventID string) int {
	f.t.Helper()
	var n int
	f.pool.QueryRow(context.Background(), `SELECT count(*) FROM webhook_events WHERE id = $1`, eventID).Scan(&n)
	return n
}

func (f *eventsE2EFixture) drainBoth(ctx context.Context) {
	f.worker.Tick(ctx)
	f.deliverPending(ctx)
}

// captureReceiver records every webhook POST.
type captureReceiver struct {
	mu       sync.Mutex
	requests []capturedRequest
	srv      *httptest.Server
}

type capturedRequest struct {
	Body      []byte
	Signature string
}

func newCaptureReceiver() *captureReceiver {
	cr := &captureReceiver{}
	cr.srv = httptest.NewServer(http.HandlerFunc(cr.handle))
	return cr
}

func (cr *captureReceiver) URL() string { return cr.srv.URL }
func (cr *captureReceiver) Close()      { cr.srv.Close() }
func (cr *captureReceiver) Count() int  { cr.mu.Lock(); defer cr.mu.Unlock(); return len(cr.requests) }
func (cr *captureReceiver) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.requests = append(cr.requests, capturedRequest{
		Body:      body,
		Signature: r.Header.Get("X-E2A-Signature"),
	})
	w.WriteHeader(200)
}

func (cr *captureReceiver) snapshot() []capturedRequest {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	out := make([]capturedRequest, len(cr.requests))
	copy(out, cr.requests)
	return out
}

// === TESTS ===

func TestEventsE2E_FullOutboxRoundTrip(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	ctx := context.Background()

	user := fix.seedUser("e2e_round")
	agent := fix.seedAgent(user, "round")
	receiver := newCaptureReceiver()
	defer receiver.Close()
	whID := fix.seedWebhook(user, receiver.URL(), []string{webhookpub.EventEmailReceived})

	messageID := "msg_round_1"
	eventID := webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailReceived)
	if err := fix.publishEvent(ctx, webhookpub.Event{
		ID:        eventID,
		Type:      webhookpub.EventEmailReceived,
		UserID:    user,
		AgentID:   agent,
		MessageID: messageID,
		Data:      map[string]any{"from": "sender@e2e.example", "subject": "round-trip"},
	}); err != nil {
		t.Fatalf("publishEvent: %v", err)
	}

	if receiver.Count() != 1 {
		t.Fatalf("receiver count = %d; want 1", receiver.Count())
	}
	captured := receiver.snapshot()[0]
	var env webhookpub.Envelope
	if err := json.Unmarshal(captured.Body, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.ID != eventID {
		t.Errorf("env.id = %s, want %s", env.ID, eventID)
	}
	if env.Type != webhookpub.EventEmailReceived {
		t.Errorf("env.type = %s, want email.received", env.Type)
	}
	if !strings.HasPrefix(captured.Signature, "t=") {
		t.Errorf("signature should start with t=; got %s", captured.Signature)
	}
	_ = whID
}

func TestEventsE2E_ListAPI_FilterAndCursor(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	ctx := context.Background()

	user := fix.seedUser("e2e_list")
	agent := fix.seedAgent(user, "list")
	apiKey := fix.issueAPIKey(user)

	for i := 0; i < 3; i++ {
		mid := fmt.Sprintf("msg_list_recv_%d", i)
		fix.publishEvent(ctx, webhookpub.Event{
			ID:        webhookpub.DeterministicEventID(mid, webhookpub.EventEmailReceived),
			Type:      webhookpub.EventEmailReceived,
			UserID:    user,
			AgentID:   agent,
			MessageID: mid,
			Data:      map[string]any{"i": i},
		})
	}
	for i := 0; i < 2; i++ {
		mid := fmt.Sprintf("msg_list_sent_%d", i)
		fix.publishEvent(ctx, webhookpub.Event{
			ID:        webhookpub.DeterministicEventID(mid, webhookpub.EventEmailSent),
			Type:      webhookpub.EventEmailSent,
			UserID:    user,
			AgentID:   agent,
			MessageID: mid,
			Data:      map[string]any{"i": i},
		})
	}

	// Unfiltered. /v1 cursor page: {items:[...], next_cursor:...}; page-size
	// param is `limit`.
	resp := fix.httpGet("/v1/events?limit=10", apiKey)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var listResp struct {
		Items      []map[string]interface{} `json:"items"`
		NextCursor *string                  `json:"next_cursor"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)
	if len(listResp.Items) != 5 {
		t.Errorf("got %d events; want 5", len(listResp.Items))
	}

	// type=email.sent filter
	resp2 := fix.httpGet("/v1/events?type=email.sent", apiKey)
	defer resp2.Body.Close()
	var typedResp struct {
		Items []map[string]interface{} `json:"items"`
	}
	json.NewDecoder(resp2.Body).Decode(&typedResp)
	if len(typedResp.Items) != 2 {
		t.Errorf("type filter returned %d; want 2", len(typedResp.Items))
	}
	for _, e := range typedResp.Items {
		if e["type"] != "email.sent" {
			t.Errorf("filter leaked: type=%v", e["type"])
		}
	}

	// Cursor pagination: limit=2, walk pages via next_cursor.
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		url := "/v1/events?limit=2"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		r := fix.httpGet(url, apiKey)
		var page struct {
			Items      []map[string]interface{} `json:"items"`
			NextCursor *string                  `json:"next_cursor"`
		}
		json.NewDecoder(r.Body).Decode(&page)
		r.Body.Close()
		pages++
		for _, e := range page.Items {
			id := e["id"].(string)
			if seen[id] {
				t.Errorf("cursor duplicated event %s", id)
			}
			seen[id] = true
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
		if pages > 10 {
			t.Fatal("cursor loop did not terminate")
		}
	}
	if len(seen) != 5 {
		t.Errorf("cursor walk saw %d unique events; want 5", len(seen))
	}
}

func TestEventsE2E_GetReturns404And410(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	user := fix.seedUser("e2e_get")
	apiKey := fix.issueAPIKey(user)

	resp := fix.httpGet("/v1/events/evt_does_not_exist", apiKey)
	if resp.StatusCode != 404 {
		t.Errorf("missing event → %d; want 404", resp.StatusCode)
	}
	resp.Body.Close()

	expiredID := webhookpub.DeterministicEventID("msg_expired_e2e", webhookpub.EventEmailReceived)
	fix.seedExpiredEvent(user, expiredID, webhookpub.EventEmailReceived)
	resp2 := fix.httpGet("/v1/events/"+expiredID, apiKey)
	if resp2.StatusCode != 410 {
		t.Errorf("expired event → %d; want 410", resp2.StatusCode)
	}
	resp2.Body.Close()
}

func TestEventsE2E_RedeliverFanOut(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	ctx := context.Background()
	user := fix.seedUser("e2e_replay")
	agent := fix.seedAgent(user, "replay")
	apiKey := fix.issueAPIKey(user)

	rcvA := newCaptureReceiver()
	defer rcvA.Close()
	rcvB := newCaptureReceiver()
	defer rcvB.Close()
	fix.seedWebhook(user, rcvA.URL(), []string{webhookpub.EventEmailReceived})
	fix.seedWebhook(user, rcvB.URL(), []string{webhookpub.EventEmailReceived})

	mid := "msg_replay_target"
	eventID := webhookpub.DeterministicEventID(mid, webhookpub.EventEmailReceived)
	fix.publishEvent(ctx, webhookpub.Event{
		ID:        eventID,
		Type:      webhookpub.EventEmailReceived,
		UserID:    user,
		AgentID:   agent,
		MessageID: mid,
		Data:      map[string]any{"orig": true},
	})
	if rcvA.Count() != 1 || rcvB.Count() != 1 {
		t.Fatalf("original delivery counts: A=%d B=%d", rcvA.Count(), rcvB.Count())
	}

	resp := fix.httpPost("/v1/events/"+eventID+"/redeliver", apiKey, []byte(`{}`))
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("redeliver status %d: %s", resp.StatusCode, body)
	}
	var rResp struct {
		EventID    string `json:"event_id"`
		Deliveries []struct {
			WebhookID  string `json:"webhook_id"`
			DeliveryID string `json:"delivery_id"`
			Status     string `json:"status"`
		} `json:"deliveries"`
	}
	json.NewDecoder(resp.Body).Decode(&rResp)
	resp.Body.Close()
	if len(rResp.Deliveries) != 2 {
		t.Errorf("replay fan-out: got %d deliveries; want 2", len(rResp.Deliveries))
	}

	fix.drainBoth(ctx)
	if rcvA.Count() != 2 {
		t.Errorf("receiver A after replay: %d; want 2", rcvA.Count())
	}
	if rcvB.Count() != 2 {
		t.Errorf("receiver B after replay: %d; want 2", rcvB.Count())
	}
}

func TestEventsE2E_AuthBoundaries(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	ctx := context.Background()

	userA := fix.seedUser("e2e_a")
	userB := fix.seedUser("e2e_b")
	apiKeyA := fix.issueAPIKey(userA)
	agentA := fix.seedAgent(userA, "auth_a")
	agentB := fix.seedAgent(userB, "auth_b")

	bEventID := webhookpub.DeterministicEventID("msg_b_only", webhookpub.EventEmailReceived)
	fix.publishEvent(ctx, webhookpub.Event{
		ID: bEventID, Type: webhookpub.EventEmailReceived,
		UserID: userB, AgentID: agentB, MessageID: "msg_b_only", Data: map[string]any{},
	})
	fix.publishEvent(ctx, webhookpub.Event{
		ID:     webhookpub.DeterministicEventID("msg_a_only", webhookpub.EventEmailReceived),
		Type:   webhookpub.EventEmailReceived,
		UserID: userA, AgentID: agentA, MessageID: "msg_a_only", Data: map[string]any{},
	})

	resp := fix.httpGet("/v1/events", apiKeyA)
	defer resp.Body.Close()
	var listResp struct {
		Items []map[string]interface{} `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)
	for _, e := range listResp.Items {
		if e["id"] == bEventID {
			t.Errorf("user A saw user B's event — auth boundary violated")
		}
	}

	directResp := fix.httpGet("/v1/events/"+bEventID, apiKeyA)
	if directResp.StatusCode != 404 {
		t.Errorf("cross-user GET → %d; want 404", directResp.StatusCode)
	}
	directResp.Body.Close()
}

func TestEventsE2E_MissingBearer(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	resp := fix.httpGet("/v1/events", "")
	if resp.StatusCode != 401 {
		t.Errorf("no bearer → %d; want 401", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestEventsE2E_ConcurrentPublishesDedup is the headline data-race +
// dedup test: 50 goroutines concurrently call PublishTx for the same
// deterministic event id. ON CONFLICT (id) DO NOTHING must produce
// exactly one row regardless of contention.
func TestEventsE2E_ConcurrentPublishesDedup(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	ctx := context.Background()
	user := fix.seedUser("e2e_concurrent")
	agent := fix.seedAgent(user, "concurrent")

	mid := "msg_concurrent_dedup"
	fix.seedMessage(mid, agent, "inbound")
	eventID := webhookpub.DeterministicEventID(mid, webhookpub.EventEmailReceived)

	var failures atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := fix.store.WithTx(ctx, func(tx pgx.Tx) error {
				return fix.outbox.PublishTx(ctx, tx, webhookpub.Event{
					ID: eventID, Type: webhookpub.EventEmailReceived,
					UserID: user, AgentID: agent, MessageID: mid, Data: map[string]any{},
				})
			})
			if err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() > 0 {
		t.Errorf("PublishTx failures: %d; want 0", failures.Load())
	}
	if n := fix.countEvents(eventID); n != 1 {
		t.Errorf("concurrent dedup produced %d rows; want 1", n)
	}
}

// TestEventsE2E_ConcurrentWorkers verifies multi-replica safety:
// two OutboxWorker instances race on the same pending row;
// FOR UPDATE SKIP LOCKED ensures exactly one wins. Run with -race
// to catch any in-process data races.
func TestEventsE2E_ConcurrentWorkers(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	ctx := context.Background()
	user := fix.seedUser("e2e_concworker")
	agent := fix.seedAgent(user, "concworker")
	receiver := newCaptureReceiver()
	defer receiver.Close()
	fix.seedWebhook(user, receiver.URL(), []string{webhookpub.EventEmailReceived})

	// Seed one event but don't drain.
	mid := "msg_concworker"
	fix.seedMessage(mid, agent, "inbound")
	if err := fix.store.WithTx(ctx, func(tx pgx.Tx) error {
		return fix.outbox.PublishTx(ctx, tx, webhookpub.Event{
			ID:        webhookpub.DeterministicEventID(mid, webhookpub.EventEmailReceived),
			Type:      webhookpub.EventEmailReceived,
			UserID:    user,
			AgentID:   agent,
			MessageID: mid,
			Data:      map[string]any{},
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	workerA := webhookpub.NewOutboxWorker(fix.pool, fix.store)
	workerB := webhookpub.NewOutboxWorker(fix.pool, fix.store)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); workerA.Tick(ctx) }()
	go func() { defer wg.Done(); workerB.Tick(ctx) }()
	wg.Wait()

	fix.deliverPending(ctx)

	// Exactly one subscriber delivery should have been written.
	var deliveryCount int
	fix.pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_subscriber_deliveries WHERE event_id = $1`,
		webhookpub.DeterministicEventID(mid, webhookpub.EventEmailReceived),
	).Scan(&deliveryCount)
	if deliveryCount != 1 {
		t.Errorf("concurrent workers produced %d delivery rows; want 1 (lease + partial index dedup)", deliveryCount)
	}
}

// TestEventsE2E_ConcurrentReadsConsistent fires 30 GET /events
// requests in parallel and verifies they all see the same complete set.
// Catches any handler thread-safety regression.
func TestEventsE2E_ConcurrentReadsConsistent(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	ctx := context.Background()
	user := fix.seedUser("e2e_concread")
	agent := fix.seedAgent(user, "concread")
	apiKey := fix.issueAPIKey(user)

	const expected = 20
	for i := 0; i < expected; i++ {
		mid := fmt.Sprintf("msg_concread_%d", i)
		fix.publishEvent(ctx, webhookpub.Event{
			ID:        webhookpub.DeterministicEventID(mid, webhookpub.EventEmailReceived),
			Type:      webhookpub.EventEmailReceived,
			UserID:    user,
			AgentID:   agent,
			MessageID: mid,
			Data:      map[string]any{"i": i},
		})
	}

	var wg sync.WaitGroup
	var mismatches atomic.Int32
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := fix.httpGet("/v1/events?limit=100", apiKey)
			defer r.Body.Close()
			var page struct {
				Items []map[string]interface{} `json:"items"`
			}
			json.NewDecoder(r.Body).Decode(&page)
			if len(page.Items) != expected {
				mismatches.Add(1)
			}
		}()
	}
	wg.Wait()
	if mismatches.Load() > 0 {
		t.Errorf("concurrent reads: %d mismatched counts", mismatches.Load())
	}
}
