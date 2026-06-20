package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func getJSON(t *testing.T, url, bearer string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestListMessagesPageEnvelopeAndCursor(t *testing.T) {
	srv := testServer(t)
	// limit=1 against 2 messages -> first page has 1 item + a next_cursor.
	code, body := getJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages?direction=inbound&status=all&limit=1", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d (%v)", len(items), body)
	}
	first := items[0].(map[string]any)
	if first["message_id"] != "msg_b" {
		t.Fatalf("want newest msg_b first, got %v", first["message_id"])
	}
	cursor, ok := body["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("expected non-null next_cursor, got %v", body["next_cursor"])
	}

	// Follow the cursor -> second page has msg_a and a null next_cursor.
	code, body = getJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages?direction=inbound&status=all&limit=1&cursor="+cursor, "good")
	if code != 200 {
		t.Fatalf("page2 status %d body %v", code, body)
	}
	items, _ = body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["message_id"] != "msg_a" {
		t.Fatalf("unexpected page 2: %v", body)
	}
	if body["next_cursor"] != nil {
		t.Fatalf("expected null next_cursor on last page, got %v", body["next_cursor"])
	}
}

func TestListMessagesOutboundStatusConflict(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages?direction=outbound&read_status=unread", "good")
	if code != 400 {
		t.Fatalf("want 400, got %d", code)
	}
	if errObj, _ := body["error"].(map[string]any); errObj["code"] != "invalid_filter" {
		t.Fatalf("want invalid_filter, got %v", body)
	}
}

func TestListMessagesBadSince(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages?direction=inbound&status=all&since=not-a-date", "good")
	if code != 400 {
		t.Fatalf("want 400, got %d", code)
	}
	if errObj, _ := body["error"].(map[string]any); errObj["code"] != "invalid_filter" {
		t.Fatalf("want invalid_filter, got %v", body)
	}
}

func TestListMessagesCursorFilterMismatch(t *testing.T) {
	srv := testServer(t)
	// Get a valid cursor under status=all...
	_, body := getJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages?direction=inbound&status=all&limit=1", "good")
	cursor := body["next_cursor"].(string)
	// ...then reuse it under a different filter (subject_contains) -> 400.
	code, body := getJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages?direction=inbound&status=all&limit=1&subject_contains=changed&cursor="+cursor, "good")
	if code != 400 {
		t.Fatalf("want 400 on filter mismatch, got %d", code)
	}
	if errObj, _ := body["error"].(map[string]any); errObj["code"] != "invalid_cursor" {
		t.Fatalf("want invalid_cursor, got %v", body)
	}
}
