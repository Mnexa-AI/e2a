package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"

	"github.com/gorilla/mux"
	"net/http/httptest"
)

// setupWebhooksAPI wires the API the same way setupAPI does but also
// attaches a SubscriberStore — slice-2's /test and /deliveries handlers
// 404 if the subscriber store is not wired.
func setupWebhooksAPI(t *testing.T) (server *httptest.Server, store *identity.Store, sub *webhook.SubscriberStore) {
	t.Helper()
	pool := testutil.TestDB(t)
	store = identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotency.NewStore(pool))
	sub = webhook.NewSubscriberStore(pool)
	api.SetSubscriberStore(sub)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server = httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, store, sub
}

// whFixture provisions one user + api key for webhook tests.
type whFixture struct {
	server *httptest.Server
	store  *identity.Store
	apiKey string
	userID string
}

func setupWHFixture(t *testing.T, prefix string) whFixture {
	t.Helper()
	server, store, _ := setupWebhooksAPI(t)
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "owner-"+prefix+"@example.com", "Owner", "google-"+prefix)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	key, err := store.CreateAPIKey(ctx, user.ID, prefix+"-key", nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	return whFixture{server: server, store: store, apiKey: key.PlaintextKey, userID: user.ID}
}

func (f whFixture) do(t *testing.T, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	var rdr *bytes.Buffer
	if body != "" {
		rdr = bytes.NewBufferString(body)
	} else {
		rdr = bytes.NewBuffer(nil)
	}
	req, _ := http.NewRequest(method, f.server.URL+path, rdr)
	req.Header.Set("Authorization", "Bearer "+f.apiKey)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	resp.Body.Close()
	return resp, buf.Bytes()
}

// --- Auth ---

func TestWebhooksAPI_RequiresAuth(t *testing.T) {
	server, _, _ := setupWebhooksAPI(t)
	for _, p := range []struct{ method, path string }{
		{"POST", "/api/v1/webhooks"},
		{"GET", "/api/v1/webhooks"},
		{"GET", "/api/v1/webhooks/wh_abc"},
		{"PATCH", "/api/v1/webhooks/wh_abc"},
		{"DELETE", "/api/v1/webhooks/wh_abc"},
		{"POST", "/api/v1/webhooks/wh_abc/rotate-secret"},
		{"POST", "/api/v1/webhooks/wh_abc/test"},
		{"GET", "/api/v1/webhooks/wh_abc/deliveries"},
	} {
		req, _ := http.NewRequest(p.method, server.URL+p.path, strings.NewReader("{}"))
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != 401 {
			t.Errorf("%s %s: status = %d, want 401", p.method, p.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// --- Create ---

func TestWebhooksAPI_Create_Success(t *testing.T) {
	f := setupWHFixture(t, "wh-create-ok")
	body := `{"url": "https://example.com/wh", "events": ["email.received"], "description": "test"}`
	resp, raw := f.do(t, "POST", "/api/v1/webhooks", body)
	if resp.StatusCode != 201 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(raw))
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if id, _ := got["id"].(string); !strings.HasPrefix(id, "wh_") {
		t.Errorf("id = %v, want wh_ prefix", got["id"])
	}
	if secret, _ := got["signing_secret"].(string); !strings.HasPrefix(secret, "whsec_") {
		t.Errorf("signing_secret missing or wrong format: %v", got["signing_secret"])
	}
	if got["enabled"] != true {
		t.Errorf("enabled = %v, want true", got["enabled"])
	}
}

func TestWebhooksAPI_Create_RejectsHTTP(t *testing.T) {
	f := setupWHFixture(t, "wh-create-http")
	body := `{"url": "http://example.com/wh", "events": ["email.received"]}`
	resp, _ := f.do(t, "POST", "/api/v1/webhooks", body)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestWebhooksAPI_Create_RejectsUnknownEvent(t *testing.T) {
	f := setupWHFixture(t, "wh-create-evt")
	body := `{"url": "https://example.com/wh", "events": ["email.bogus"]}`
	resp, raw := f.do(t, "POST", "/api/v1/webhooks", body)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d body=%s, want 400", resp.StatusCode, string(raw))
	}
}

func TestWebhooksAPI_Create_RejectsEmptyEvents(t *testing.T) {
	f := setupWHFixture(t, "wh-create-empty-evt")
	body := `{"url": "https://example.com/wh", "events": []}`
	resp, _ := f.do(t, "POST", "/api/v1/webhooks", body)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestWebhooksAPI_Create_RejectsCrossUserAgentID(t *testing.T) {
	f := setupWHFixture(t, "wh-create-xuser-agent")
	// Create a SECOND user and their agent — the fixture user must
	// not be allowed to reference it in filters.agent_ids.
	ctx := context.Background()
	otherUser, _ := f.store.CreateOrGetUser(ctx, "other-wh-create-xuser@example.com", "Other", "google-other-xuser")
	f.store.ClaimOrCreateDomain(ctx, "other-xuser.example.com", otherUser.ID)
	f.store.VerifyDomain(ctx, "other-xuser.example.com", otherUser.ID)
	otherAgent, _ := f.store.CreateAgent(ctx, "bot@other-xuser.example.com", "other-xuser.example.com", "", "https://example.com/webhook", "", otherUser.ID)

	body := fmt.Sprintf(`{"url": "https://example.com/wh", "events": ["email.received"], "filters": {"agent_ids": [%q]}}`, otherAgent.ID)
	resp, raw := f.do(t, "POST", "/api/v1/webhooks", body)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d body=%s, want 400 (cross-user agent)", resp.StatusCode, string(raw))
	}
}

// --- List / Get ---

func TestWebhooksAPI_ListAndGet_OmitSecret(t *testing.T) {
	f := setupWHFixture(t, "wh-list")
	create := `{"url": "https://example.com/wh", "events": ["email.received"]}`
	resp, raw := f.do(t, "POST", "/api/v1/webhooks", create)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, string(raw))
	}
	var created map[string]interface{}
	json.Unmarshal(raw, &created)
	whID := created["id"].(string)

	// LIST
	resp, raw = f.do(t, "GET", "/api/v1/webhooks", "")
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	var listed map[string]interface{}
	json.Unmarshal(raw, &listed)
	hooks, _ := listed["webhooks"].([]interface{})
	if len(hooks) != 1 {
		t.Fatalf("want 1 webhook, got %d", len(hooks))
	}
	if first, _ := hooks[0].(map[string]interface{}); first["signing_secret"] != nil {
		t.Errorf("LIST should scrub signing_secret, got %v", first["signing_secret"])
	}

	// GET
	resp, raw = f.do(t, "GET", "/api/v1/webhooks/"+whID, "")
	if resp.StatusCode != 200 {
		t.Fatalf("get: %d", resp.StatusCode)
	}
	var fetched map[string]interface{}
	json.Unmarshal(raw, &fetched)
	if fetched["signing_secret"] != nil {
		t.Errorf("GET should scrub signing_secret, got %v", fetched["signing_secret"])
	}
}

func TestWebhooksAPI_Get_CrossUser404(t *testing.T) {
	f := setupWHFixture(t, "wh-get-xuser")
	// Create as user A.
	create := `{"url": "https://example.com/wh", "events": ["email.received"]}`
	_, raw := f.do(t, "POST", "/api/v1/webhooks", create)
	var created map[string]interface{}
	json.Unmarshal(raw, &created)
	whID := created["id"].(string)

	// Make a second user fixture sharing the same server.
	ctx := context.Background()
	otherUser, _ := f.store.CreateOrGetUser(ctx, "other-wh-get@example.com", "Other", "google-other-wh-get")
	otherKey, _ := f.store.CreateAPIKey(ctx, otherUser.ID, "other-key", nil)
	req, _ := http.NewRequest("GET", f.server.URL+"/api/v1/webhooks/"+whID, nil)
	req.Header.Set("Authorization", "Bearer "+otherKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("cross-user GET status = %d, want 404", resp.StatusCode)
	}
}

// --- Patch ---

func TestWebhooksAPI_Patch_PartialUpdate(t *testing.T) {
	f := setupWHFixture(t, "wh-patch")
	create := `{"url": "https://example.com/wh", "events": ["email.received"], "description": "old"}`
	_, raw := f.do(t, "POST", "/api/v1/webhooks", create)
	var created map[string]interface{}
	json.Unmarshal(raw, &created)
	whID := created["id"].(string)

	patch := `{"description": "new desc", "enabled": false}`
	resp, raw := f.do(t, "PATCH", "/api/v1/webhooks/"+whID, patch)
	if resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, string(raw))
	}
	var updated map[string]interface{}
	json.Unmarshal(raw, &updated)
	if updated["description"] != "new desc" {
		t.Errorf("description = %v, want 'new desc'", updated["description"])
	}
	if updated["enabled"] != false {
		t.Errorf("enabled = %v, want false", updated["enabled"])
	}
}

func TestWebhooksAPI_Patch_RejectsEmptyEvents(t *testing.T) {
	f := setupWHFixture(t, "wh-patch-emptyev")
	_, raw := f.do(t, "POST", "/api/v1/webhooks", `{"url":"https://example.com/wh","events":["email.received"]}`)
	var created map[string]interface{}
	json.Unmarshal(raw, &created)
	whID := created["id"].(string)
	resp, _ := f.do(t, "PATCH", "/api/v1/webhooks/"+whID, `{"events": []}`)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// --- Delete ---

func TestWebhooksAPI_Delete(t *testing.T) {
	f := setupWHFixture(t, "wh-del")
	_, raw := f.do(t, "POST", "/api/v1/webhooks", `{"url":"https://example.com/wh","events":["email.received"]}`)
	var created map[string]interface{}
	json.Unmarshal(raw, &created)
	whID := created["id"].(string)
	resp, _ := f.do(t, "DELETE", "/api/v1/webhooks/"+whID, "")
	if resp.StatusCode != 204 {
		t.Errorf("delete status = %d, want 204", resp.StatusCode)
	}
	// Second delete -> 404
	resp, _ = f.do(t, "DELETE", "/api/v1/webhooks/"+whID, "")
	if resp.StatusCode != 404 {
		t.Errorf("idempotent delete status = %d, want 404", resp.StatusCode)
	}
}

// --- Rotate secret ---

func TestWebhooksAPI_RotateSecret_ReturnsNewPlaintext(t *testing.T) {
	f := setupWHFixture(t, "wh-rotate")
	_, raw := f.do(t, "POST", "/api/v1/webhooks", `{"url":"https://example.com/wh","events":["email.received"]}`)
	var created map[string]interface{}
	json.Unmarshal(raw, &created)
	whID := created["id"].(string)
	origSecret := created["signing_secret"].(string)

	resp, raw := f.do(t, "POST", "/api/v1/webhooks/"+whID+"/rotate-secret", "")
	if resp.StatusCode != 200 {
		t.Fatalf("rotate: %d %s", resp.StatusCode, string(raw))
	}
	var got map[string]interface{}
	json.Unmarshal(raw, &got)
	newSecret, _ := got["signing_secret"].(string)
	if !strings.HasPrefix(newSecret, "whsec_") {
		t.Errorf("new secret missing whsec_ prefix: %s", newSecret)
	}
	if newSecret == origSecret {
		t.Errorf("rotate returned the same secret")
	}
	if _, ok := got["previous_secret_expires_at"].(string); !ok {
		t.Errorf("previous_secret_expires_at missing or not string")
	}
}

// --- Test endpoint ---

func TestWebhooksAPI_Test_SchedulesDelivery(t *testing.T) {
	f := setupWHFixture(t, "wh-test")
	_, raw := f.do(t, "POST", "/api/v1/webhooks", `{"url":"https://example.com/wh","events":["email.received"]}`)
	var created map[string]interface{}
	json.Unmarshal(raw, &created)
	whID := created["id"].(string)

	resp, raw := f.do(t, "POST", "/api/v1/webhooks/"+whID+"/test",
		`{"event": "email.received", "data": {"foo": "bar"}}`)
	if resp.StatusCode != 200 {
		t.Fatalf("test: %d %s", resp.StatusCode, string(raw))
	}
	var got map[string]interface{}
	json.Unmarshal(raw, &got)
	deliveryID, _ := got["delivery_id"].(string)
	if !strings.HasPrefix(deliveryID, "whd_") {
		t.Errorf("delivery_id = %v, want whd_ prefix", deliveryID)
	}
}

func TestWebhooksAPI_Test_DisabledWebhookReturns409(t *testing.T) {
	f := setupWHFixture(t, "wh-test-disabled")
	_, raw := f.do(t, "POST", "/api/v1/webhooks", `{"url":"https://example.com/wh","events":["email.received"]}`)
	var created map[string]interface{}
	json.Unmarshal(raw, &created)
	whID := created["id"].(string)
	// Disable.
	f.do(t, "PATCH", "/api/v1/webhooks/"+whID, `{"enabled": false}`)
	resp, _ := f.do(t, "POST", "/api/v1/webhooks/"+whID+"/test", `{}`)
	if resp.StatusCode != 409 {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

// --- Deliveries list ---

func TestWebhooksAPI_Deliveries_Lists(t *testing.T) {
	f := setupWHFixture(t, "wh-deliveries")
	_, raw := f.do(t, "POST", "/api/v1/webhooks", `{"url":"https://example.com/wh","events":["email.received"]}`)
	var created map[string]interface{}
	json.Unmarshal(raw, &created)
	whID := created["id"].(string)

	// Trigger /test to seed a row.
	f.do(t, "POST", "/api/v1/webhooks/"+whID+"/test", `{}`)
	resp, raw := f.do(t, "GET", "/api/v1/webhooks/"+whID+"/deliveries", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(raw))
	}
	var got map[string]interface{}
	json.Unmarshal(raw, &got)
	dels, _ := got["deliveries"].([]interface{})
	if len(dels) < 1 {
		t.Errorf("expected at least 1 delivery, got %d", len(dels))
	}
}

func TestWebhooksAPI_Deliveries_RejectsBadStatus(t *testing.T) {
	f := setupWHFixture(t, "wh-deliveries-badstatus")
	_, raw := f.do(t, "POST", "/api/v1/webhooks", `{"url":"https://example.com/wh","events":["email.received"]}`)
	var created map[string]interface{}
	json.Unmarshal(raw, &created)
	whID := created["id"].(string)
	resp, _ := f.do(t, "GET", "/api/v1/webhooks/"+whID+"/deliveries?status=bogus", "")
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
