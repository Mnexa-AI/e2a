package httpapi

import "testing"

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
	// limit=1: page1 evt_b + cursor; follow -> page2 evt_a (+ boundary
	// cursor, matching the legacy len==limit heuristic); follow -> empty.
	code, body := getJSON(t, srv.URL+"/v1/events?limit=1", "good")
	if code != 200 || body["items"].([]any)[0].(map[string]any)["id"] != "evt_b" {
		t.Fatalf("page1: %d %v", code, body)
	}
	cur := body["next_cursor"].(string)
	_, body = getJSON(t, srv.URL+"/v1/events?limit=1&cursor="+cur, "good")
	if body["items"].([]any)[0].(map[string]any)["id"] != "evt_a" {
		t.Fatalf("page2: %v", body)
	}
	cur2 := body["next_cursor"].(string)
	_, body = getJSON(t, srv.URL+"/v1/events?limit=1&cursor="+cur2, "good")
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Fatalf("page3 should be empty, got %v", body)
	}
	if body["next_cursor"] != nil {
		t.Fatalf("page3 next_cursor should be null, got %v", body["next_cursor"])
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
	if code != 200 || body["delivery_id"] != "whd_wh_1" || body["status"] != "pending" {
		t.Fatalf("targeted redeliver: %d %v", code, body)
	}
}

func TestRedeliverEventFanout(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/events/evt_a/redeliver", "good", map[string]any{})
	if code != 200 || body["status"] != "scheduled" {
		t.Fatalf("fanout redeliver: %d %v", code, body)
	}
	dels, _ := body["deliveries"].([]any)
	if len(dels) != 2 {
		t.Fatalf("want 2 fanout deliveries, got %d", len(dels))
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
