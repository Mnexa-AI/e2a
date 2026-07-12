package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

func TestListEventsNoOverflow(t *testing.T) {
	srv := testServer(t)
	// limit > result count -> all items, null next_cursor.
	code, body := getJSON(t, srv.URL+"/v1/events?limit=5", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if body["next_cursor"] != nil {
		t.Fatalf("expected null next_cursor when not full, got %v", body["next_cursor"])
	}
}

func TestListEventsCursorRoundTrip(t *testing.T) {
	srv := testServer(t)
	// limit=1 with limit+1 over-fetch: page1 = evt_b (+cursor); page2 = evt_a
	// with a NULL cursor (the over-fetch sees no further row, so no spurious
	// empty page).
	code, body := getJSON(t, srv.URL+"/v1/events?limit=1", "good")
	if code != 200 || body["items"].([]any)[0].(map[string]any)["id"] != "evt_b" {
		t.Fatalf("page1: %d %v", code, body)
	}
	cur := body["next_cursor"].(string)
	_, body = getJSON(t, srv.URL+"/v1/events?limit=1&cursor="+cur, "good")
	if body["items"].([]any)[0].(map[string]any)["id"] != "evt_a" {
		t.Fatalf("page2: %v", body)
	}
	if body["next_cursor"] != nil {
		t.Fatalf("page2 next_cursor should be null (last page), got %v", body["next_cursor"])
	}
}

// TestListEventsCursorRejectsChangedFilter pins the filter-binding: a cursor
// minted under one filter set must be rejected if a continuation changes the
// filters (else the keyset position is meaningless and the page silently wrong).
func TestListEventsCursorRejectsChangedFilter(t *testing.T) {
	srv := testServer(t)
	// Mint a cursor with NO type filter...
	_, body := getJSON(t, srv.URL+"/v1/events?limit=1", "good")
	cur, _ := body["next_cursor"].(string)
	if cur == "" {
		t.Fatal("expected a next_cursor on page 1")
	}
	// ...then reuse it WITH a type filter -> invalid_cursor.
	code, body := getJSON(t, srv.URL+"/v1/events?limit=1&type=email.sent&cursor="+cur, "good")
	if code != 400 || errCode(body) != "invalid_cursor" {
		t.Fatalf("want 400 invalid_cursor when filters change under a cursor, got %d %v", code, body)
	}
}

func TestGetEvent(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/events/evt_a", "good")
	if code != 200 || body["id"] != "evt_a" {
		t.Fatalf("want 200 evt_a, got %d %v", code, body)
	}
}

func TestGetEventNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/events/evt_missing", "good")
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestGetEventExpiredGone(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/events/evt_expired", "good")
	if code != 410 || errCode(body) != "gone" {
		t.Fatalf("want 410 gone, got %d %v", code, body)
	}
}

func TestRedeliverEventTargeted(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/events/evt_a/redeliver", "good", map[string]any{"webhook_id": "wh_1"})
	if code != 202 || body["delivery_id"] != "whd_wh_1" || body["status"] != "pending" {
		t.Fatalf("targeted redeliver: %d %v", code, body)
	}
}

func TestRedeliverEventFanout(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/events/evt_a/redeliver", "good", map[string]any{})
	if code != 202 || body["status"] != "scheduled" {
		t.Fatalf("fanout redeliver: %d %v", code, body)
	}
	dels, _ := body["deliveries"].([]any)
	if len(dels) != 2 {
		t.Fatalf("want 2 fanout deliveries, got %d", len(dels))
	}
}

// TestEnqueueError_ReportsPendingNotFailure pins the reconciler-backed contract:
// when the River enqueue seam errors, /test and redelivery must still report
// success/pending (the row is durable and the periodic reconciler will re-drive it),
// NOT 500 or 'skipped' — a failure response would spawn a duplicate row on retry.
func TestEnqueueError_ReportsPendingNotFailure(t *testing.T) {
	boom := func(_ context.Context, _ string) error { return errors.New("river down") }
	srv := testServer(t, func(d *Deps) { d.EnqueueDelivery = boom })

	// /test → 200 with a delivery_id (not 500).
	code, body := postJSON(t, srv.URL+"/v1/webhooks/wh_1/test", "good", map[string]any{})
	if code != 200 || body["delivery_id"] == nil || body["delivery_id"] == "" {
		t.Fatalf("/test on enqueue error: want 200 + delivery_id, got %d %v", code, body)
	}

	// Targeted redeliver → 202 pending (not 500).
	code, body = postJSON(t, srv.URL+"/v1/events/evt_a/redeliver", "good", map[string]any{"webhook_id": "wh_1"})
	if code != 202 || body["status"] != "pending" {
		t.Fatalf("targeted redeliver on enqueue error: want 202 pending, got %d %v", code, body)
	}

	// Fanout redeliver → 202, every delivery 'pending' (not 'skipped').
	code, body = postJSON(t, srv.URL+"/v1/events/evt_a/redeliver", "good", map[string]any{})
	if code != 202 {
		t.Fatalf("fanout redeliver on enqueue error: want 202, got %d %v", code, body)
	}
	dels, _ := body["deliveries"].([]any)
	if len(dels) == 0 {
		t.Fatalf("fanout: no deliveries in %v", body)
	}
	for _, d := range dels {
		if m, _ := d.(map[string]any); m["status"] != "pending" {
			t.Errorf("fanout delivery status = %v, want pending (reconciler-backed, not skipped): %v", m["status"], m)
		}
	}
}

func TestRedeliverEventUnmatchedWebhook(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/events/evt_a/redeliver", "good", map[string]any{"webhook_id": "wh_other"})
	if code != 409 || errCode(body) != "conflict" {
		t.Fatalf("want 409 conflict, got %d %v", code, body)
	}
}

func TestRedeliverEventNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/events/evt_missing/redeliver", "good", map[string]any{})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

// TestEventsDisabledReturns501 pins the gate: when the durable event log is
// off (EventsEnabled=false, i.e. WEBHOOKS_OUTBOX_ENABLED unset in prod), the
// list/get/redeliver endpoints must return 501 events_log_disabled rather than
// querying the empty webhook_events table and masquerading as "no events".
func TestEventsDisabledReturns501(t *testing.T) {
	srv := httptest.NewServer(New(Deps{
		EventsEnabled: false,
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
	}))
	defer srv.Close()

	for _, path := range []string{"/v1/events?limit=5", "/v1/events/evt_a"} {
		code, body := getJSON(t, srv.URL+path, "good")
		if code != 501 || errCode(body) != "events_log_disabled" {
			t.Fatalf("GET %s: want 501 events_log_disabled, got %d %v", path, code, body)
		}
	}
	code, body := postJSON(t, srv.URL+"/v1/events/evt_a/redeliver", "good", map[string]any{})
	if code != 501 || errCode(body) != "events_log_disabled" {
		t.Fatalf("redeliver: want 501 events_log_disabled, got %d %v", code, body)
	}
}
