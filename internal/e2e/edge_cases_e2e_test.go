//go:build integration

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// Edge-case tests covering design corners that the main e2e suite
// glides past:
//   * 7-day cap boundary on /redeliver-since (design §4.6)
//   * empty matched_webhook_ids replay returns sensible response
//   * cursor pagination across filter mismatch
//   * 410 boundary as expires_at crosses now()
//   * Replay-since on a webhook that originally matched 0 events
//   * Long event payload survives JSON round-trip in envelope

func TestEdge_RedeliverSince_7DayCapBoundary(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	user := fix.seedUser("edge_7day")
	apiKey := fix.issueAPIKey(user)
	whID := fix.seedWebhook(user, "http://example.com/wh", []string{webhookpub.EventEmailReceived})

	// 8 days ago — should 400.
	tooOld := time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"since":"%s"}`, tooOld)
	resp := fix.httpPost("/api/v1/webhooks/"+whID+"/redeliver-since", apiKey, []byte(body))
	if resp.StatusCode != 400 {
		t.Errorf("8 days ago → %d; want 400 (7-day cap)", resp.StatusCode)
	}
	resp.Body.Close()

	// Exactly 7 days minus 1 hour — should accept.
	justWithin := time.Now().Add(-7*24*time.Hour + time.Hour).UTC().Format(time.RFC3339)
	body2 := fmt.Sprintf(`{"since":"%s"}`, justWithin)
	resp2 := fix.httpPost("/api/v1/webhooks/"+whID+"/redeliver-since", apiKey, []byte(body2))
	if resp2.StatusCode != 200 {
		b, _ := io.ReadAll(resp2.Body)
		t.Errorf("just-within-window → %d (%s); want 200", resp2.StatusCode, b)
	}
	resp2.Body.Close()
}

func TestEdge_RedeliverEmptyMatched_NoCrash(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	ctx := context.Background()
	user := fix.seedUser("edge_empty")
	agent := fix.seedAgent(user, "empty")
	apiKey := fix.issueAPIKey(user)

	// Publish an event with NO matched webhooks (no webhooks
	// registered for this user). status becomes no_match.
	mid := "msg_empty_replay"
	eventID := webhookpub.DeterministicEventID(mid, webhookpub.EventEmailReceived)
	fix.publishEvent(ctx, webhookpub.Event{
		ID: eventID, Type: webhookpub.EventEmailReceived,
		UserID: user, AgentID: agent, MessageID: mid, Data: map[string]any{},
	})

	// Replay with empty body: should return a deliveries array of
	// length 0 without crashing.
	resp := fix.httpPost("/api/v1/events/"+eventID+"/redeliver", apiKey, []byte(`{}`))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("empty-matched replay → %d (%s); want 200", resp.StatusCode, b)
	}
	var r struct {
		Deliveries []map[string]any `json:"deliveries"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	if len(r.Deliveries) != 0 {
		t.Errorf("empty-matched replay produced %d deliveries; want 0", len(r.Deliveries))
	}
}

func TestEdge_RedeliverSinceOnWebhookWithZeroEvents(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	user := fix.seedUser("edge_zero")
	apiKey := fix.issueAPIKey(user)
	whID := fix.seedWebhook(user, "http://example.com/wh", []string{webhookpub.EventEmailReceived})

	body := fmt.Sprintf(`{"since":"%s"}`, time.Now().Add(-1*time.Hour).UTC().Format(time.RFC3339))
	resp := fix.httpPost("/api/v1/webhooks/"+whID+"/redeliver-since", apiKey, []byte(body))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("zero-event window → %d", resp.StatusCode)
	}
	var r struct {
		Scheduled int `json:"scheduled"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	if r.Scheduled != 0 {
		t.Errorf("zero-event scheduled = %d; want 0", r.Scheduled)
	}
}

func TestEdge_410BoundaryExactlyAtExpiresAt(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	user := fix.seedUser("edge_410")
	apiKey := fix.issueAPIKey(user)

	// Seed an event that expires NOW — should immediately 410.
	expiringNow := webhookpub.DeterministicEventID("msg_just_expired", webhookpub.EventEmailReceived)
	_, err := fix.pool.Exec(context.Background(),
		`INSERT INTO webhook_events (id, user_id, type, envelope, status, expires_at)
		 VALUES ($1, $2, $3, '{}'::jsonb, 'pending', now() - interval '1 second')`,
		expiringNow, user, webhookpub.EventEmailReceived)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp := fix.httpGet("/api/v1/events/"+expiringNow, apiKey)
	if resp.StatusCode != 410 {
		t.Errorf("expired-just-now → %d; want 410", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestEdge_LargeEventPayloadRoundTrip(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	ctx := context.Background()
	user := fix.seedUser("edge_large")
	agent := fix.seedAgent(user, "large")
	apiKey := fix.issueAPIKey(user)

	// 50 KB payload — within JSONB limits, exercises the envelope
	// serialization + reverse parse on GET.
	bigString := strings.Repeat("x", 50000)
	mid := "msg_large_payload"
	eventID := webhookpub.DeterministicEventID(mid, webhookpub.EventEmailReceived)
	fix.publishEvent(ctx, webhookpub.Event{
		ID: eventID, Type: webhookpub.EventEmailReceived,
		UserID: user, AgentID: agent, MessageID: mid,
		Data: map[string]any{"body": bigString, "subject": "large"},
	})

	resp := fix.httpGet("/api/v1/events/"+eventID, apiKey)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("large get → %d", resp.StatusCode)
	}
	var ev struct {
		Data map[string]any `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&ev)
	body, ok := ev.Data["body"].(string)
	if !ok {
		t.Fatalf("body type = %T; want string", ev.Data["body"])
	}
	if len(body) != 50000 {
		t.Errorf("body length = %d; want 50000 (large payload corrupted)", len(body))
	}
}

func TestEdge_InvalidCursorReturns400(t *testing.T) {
	fix := newEventsFixture(t)
	defer fix.Close()
	user := fix.seedUser("edge_cursor")
	apiKey := fix.issueAPIKey(user)

	// Malformed base64.
	resp := fix.httpGet("/api/v1/events?token=NOT_VALID_BASE64!!!", apiKey)
	if resp.StatusCode != 400 {
		t.Errorf("bad token → %d; want 400", resp.StatusCode)
	}
	resp.Body.Close()
}
