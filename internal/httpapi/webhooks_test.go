package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCreateWebhookReturnsSecret(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/webhooks", "good", map[string]any{
		"url": "https://example.com/hook", "events": []string{"email.received"},
	})
	if code != 201 {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["signing_secret"] != "whsec_xyz" {
		t.Fatalf("create must return signing_secret, got %v", body)
	}
}

func TestCreateWebhookRejectsSSRF(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/webhooks", "good", map[string]any{
		"url": "http://example.com/hook", "events": []string{"email.received"},
	})
	if code != 400 || errCode(body) != "invalid_webhook_url" {
		t.Fatalf("want 400 invalid_webhook_url, got %d %v", code, body)
	}
}

func TestCreateWebhookNoEvents(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/webhooks", "good", map[string]any{
		"url": "https://example.com/hook", "events": []string{},
	})
	if code != 400 {
		t.Fatalf("want 400, got %d %v", code, body)
	}
}

func TestCreateWebhookInvalidEventType(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/webhooks", "good", map[string]any{
		"url": "https://example.com/hook", "events": []string{"email.invented"},
	})
	// Unknown event now rejected by the schema enum (WH-2) → 422 before the handler.
	if code != 422 || errCode(body) != "invalid_request" {
		t.Fatalf("want 422 invalid_request, got %d %v", code, body)
	}
}

func TestCreateWebhookUnownedAgentFilter(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/webhooks", "good", map[string]any{
		"url": "https://example.com/hook", "events": []string{"email.received"},
		"filters": map[string]any{"agent_emails": []string{"someone-else@x.com"}},
	})
	if code != 400 {
		t.Fatalf("want 400 for unowned agent filter, got %d %v", code, body)
	}
}

func TestCreateWebhookOwnedAgentFilter(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/webhooks", "good", map[string]any{
		"url": "https://example.com/hook", "events": []string{"email.received"},
		"filters": map[string]any{"agent_emails": []string{"support@acme.com"}},
	})
	if code != 201 {
		t.Fatalf("owned agent filter should be accepted, got %d %v", code, body)
	}
}

func TestCreateWebhookCapReached(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/webhooks", "good", map[string]any{
		"url": "https://example.com/capped", "events": []string{"email.received"},
	})
	if code != 400 || errCode(body) != "webhook_limit_reached" {
		t.Fatalf("want 400 webhook_limit_reached, got %d %v", code, body)
	}
}

func TestListWebhooksHidesSecret(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/webhooks", "good")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	hooks, _ := body["items"].([]any)
	if len(hooks) != 1 {
		t.Fatalf("want 1 webhook, got %d", len(hooks))
	}
	if _, present := hooks[0].(map[string]any)["signing_secret"]; present {
		t.Fatal("list must NOT expose signing_secret")
	}
	// Single-page at GA (no server-side cursoring yet): Page envelope present,
	// next_cursor always null. Locks the contract — see TestListDomains.
	if body["next_cursor"] != nil {
		t.Fatalf("expected null next_cursor on single page, got %v", body["next_cursor"])
	}
}

func TestGetWebhookHidesSecret(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/webhooks/wh_1", "good")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if _, present := body["signing_secret"]; present {
		t.Fatal("get must NOT expose signing_secret")
	}
}

func TestGetWebhookNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/webhooks/wh_missing", "good")
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestDeleteWebhook(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("DELETE", srv.URL+"/v1/webhooks/wh_1?confirm=DELETE", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["deleted"] != true || body["id"] != "wh_1" {
		t.Fatalf("want {deleted:true, id:wh_1}, got %v", body)
	}
}

func TestDeleteWebhookNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/webhooks/wh_missing?confirm=DELETE", "good", nil)
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestUpdateWebhookDescription(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/webhooks/wh_1", "good", map[string]any{"description": "new desc"})
	if code != 200 || body["description"] != "new desc" {
		t.Fatalf("want 200 updated, got %d %v", code, body)
	}
	if _, present := body["signing_secret"]; present {
		t.Fatal("update must NOT expose signing_secret")
	}
}

func TestUpdateWebhookEmptyEventsRejected(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/webhooks/wh_1", "good", map[string]any{"events": []string{}})
	if code != 400 {
		t.Fatalf("want 400, got %d %v", code, body)
	}
}

func TestUpdateWebhookCooldown(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/webhooks/wh_cooldown", "good", map[string]any{"enabled": true})
	if code != 409 || errCode(body) != "webhook_cooldown" {
		t.Fatalf("want 409 webhook_cooldown, got %d %v", code, body)
	}
}

func TestUpdateWebhookNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "PATCH", srv.URL+"/v1/webhooks/wh_missing", "good", map[string]any{"description": "x"})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestRotateWebhookSecret(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/webhooks/wh_1/rotate-secret", "good", nil)
	if code != 200 || body["signing_secret"] != "whsec_rotated" {
		t.Fatalf("want 200 rotated secret, got %d %v", code, body)
	}
	if body["previous_secret_expires_at"] == "" {
		t.Fatal("rotate must return previous_secret_expires_at")
	}
}

func TestRotateWebhookSecretNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/webhooks/wh_missing/rotate-secret", "good", nil)
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestTestWebhook(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/webhooks/wh_1/test", "good", map[string]any{"type": "email.received"})
	if code != 200 || body["delivery_id"] != "whd_test_1" {
		t.Fatalf("want 200 delivery_id, got %d %v", code, body)
	}
}

func TestTestWebhookInvalidEvent(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/webhooks/wh_1/test", "good", map[string]any{"type": "email.invented"})
	// Unknown event now rejected by the schema enum (WH-2) → 422 before the handler.
	if code != 422 || errCode(body) != "invalid_request" {
		t.Fatalf("want 422 invalid_request, got %d %v", code, body)
	}
}

func TestTestWebhookNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/webhooks/wh_missing/test", "good", map[string]any{})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestListWebhookDeliveries(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/webhooks/wh_1/deliveries", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "whd_1" {
		t.Fatalf("unexpected deliveries: %v", body)
	}
	if body["next_cursor"] != nil {
		t.Fatalf("expected null next_cursor, got %v", body["next_cursor"])
	}
}

func TestListWebhookDeliveriesNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/webhooks/wh_missing/deliveries", "good")
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}
