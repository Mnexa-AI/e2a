package httpapi

import "testing"

func TestGetMyLimits(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/account", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["plan_code"] != "pro" {
		t.Fatalf("unexpected plan: %v", body)
	}
	caps, _ := body["limits"].(map[string]any)
	if caps["max_agents"].(float64) != 10 {
		t.Fatalf("unexpected caps: %v", caps)
	}
	usage, _ := body["usage"].(map[string]any)
	if usage["agents"].(float64) != 2 || usage["messages_month"].(float64) != 42 {
		t.Fatalf("unexpected usage: %v", usage)
	}
}

func TestGetMyLimitsUnauthorized(t *testing.T) {
	srv := testServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/account", "")
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
}

// TestListSuppressionsPagination exercises the A-5 cursor round-trip: page 1
// returns `limit` items + a next_cursor, and replaying that cursor returns the
// remaining page with a null cursor.
func TestListSuppressionsPagination(t *testing.T) {
	srv := testServer(t)

	// Page 1: limit=2 over a 3-item set → 2 items + a cursor.
	code, body := getJSON(t, srv.URL+"/v1/account/suppressions?limit=2", "good")
	if code != 200 {
		t.Fatalf("page1 status %d: %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("page1 want 2 items, got %d (%v)", len(items), body)
	}
	cursor, _ := body["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("page1 want a next_cursor, got null: %v", body)
	}
	first := items[0].(map[string]any)
	if first["address"] != "c@x.com" {
		t.Fatalf("page1[0] want c@x.com (newest), got %v", first["address"])
	}

	// Page 2: replay the cursor → the last item, no further cursor.
	code, body = getJSON(t, srv.URL+"/v1/account/suppressions?limit=2&cursor="+cursor, "good")
	if code != 200 {
		t.Fatalf("page2 status %d: %v", code, body)
	}
	items, _ = body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["address"] != "a@x.com" {
		t.Fatalf("page2 want [a@x.com], got %v", body)
	}
	if body["next_cursor"] != nil {
		t.Fatalf("page2 want null next_cursor, got %v", body["next_cursor"])
	}
}

// TestListSuppressionsBadCursor rejects a malformed cursor with 400.
func TestListSuppressionsBadCursor(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/account/suppressions?cursor=not-a-cursor", "good")
	if code != 400 || errCode(body) != "invalid_cursor" {
		t.Fatalf("want 400 invalid_cursor, got %d %v", code, body)
	}
}
