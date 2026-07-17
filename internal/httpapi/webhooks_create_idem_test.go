package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tokencanopy/e2a/internal/identity"
)

// webhookCreateFake is the deps.CreateWebhook signature (kept as a named type
// so the fakes below stay readable).
type webhookCreateFake func(ctx context.Context, userID, url, description string, events []string, filters identity.WebhookFilters, idemCompleteTx identity.WebhookIdemCompleter) (*identity.Webhook, error)

// webhookCreateIdemServer wires the body-aware idempotency store (the same
// fake the createApiKey idempotency tests use — it honors the body hash AND
// mirrors the real store's completed-row guard) plus a caller-supplied
// CreateWebhook fake. Two bearer tokens map to two distinct accounts so the
// cross-account key-scoping test is expressible.
func webhookCreateIdemServer(t *testing.T, create webhookCreateFake) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			switch r.Header.Get("Authorization") {
			case "Bearer good":
				return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
			case "Bearer other":
				return &identity.User{ID: "u_2", Email: "other@beta.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		Idempotency:   newBodyAwareIdem(),
		CreateWebhook: create,
	}))
	t.Cleanup(srv.Close)
	return srv
}

// countingWebhookCreate is a production-faithful CreateWebhook fake: each call
// mints a DISTINCT id + one-time signing secret (so a double-create is
// observable) and — mirroring identity.Store.CreateWebhookIdem — invokes the
// idempotency completer with the new webhook before returning, aborting the
// create if the completer fails. URLs containing "capped" simulate the
// per-user webhook limit.
func countingWebhookCreate(creates *int) webhookCreateFake {
	return func(ctx context.Context, userID, url, description string, events []string, filters identity.WebhookFilters, idemCompleteTx identity.WebhookIdemCompleter) (*identity.Webhook, error) {
		if strings.Contains(url, "capped") {
			return nil, identity.ErrWebhookCapReached
		}
		*creates++
		wh := &identity.Webhook{
			ID: fmt.Sprintf("wh_%d", *creates), UserID: userID, URL: url, Description: description,
			Events: events, Filters: filters, SigningSecret: fmt.Sprintf("whsec_create_%d", *creates),
			Enabled: true, CreatedAt: time.Unix(1700000000, 0).UTC(),
		}
		if idemCompleteTx != nil {
			if err := idemCompleteTx(ctx, nil, wh); err != nil {
				return nil, err
			}
		}
		return wh, nil
	}
}

func createWebhookCall(t *testing.T, url, bearer, idemKey, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

const whCreateBody = `{"url":"https://example.com/hook","events":["email.received"]}`

// A retried createWebhook carrying the SAME Idempotency-Key must replay the
// first response — the SAME webhook id, 201, and one-time signing secret —
// and NOT create a second active subscription with a second secret.
func TestCreateWebhook_IdempotentReplay(t *testing.T) {
	var creates int
	srv := webhookCreateIdemServer(t, countingWebhookCreate(&creates))
	url := srv.URL + "/v1/webhooks"

	code1, b1 := createWebhookCall(t, url, "good", "whk-1", whCreateBody)
	code2, b2 := createWebhookCall(t, url, "good", "whk-1", whCreateBody) // network retry, byte-identical

	if code1 != 201 || code2 != 201 {
		t.Fatalf("want 201/201, got %d/%d (b1=%v b2=%v)", code1, code2, b1, b2)
	}
	if creates != 1 {
		t.Fatalf("retry created a SECOND subscription: creates=%d, want 1", creates)
	}
	if b1["id"] != b2["id"] || b1["signing_secret"] != b2["signing_secret"] {
		t.Fatalf("retry returned a different webhook: id %v vs %v, secret %v vs %v",
			b1["id"], b2["id"], b1["signing_secret"], b2["signing_secret"])
	}
}

// Same key + a DIFFERENT body is a caller bug, not a retry: 422, no replay,
// no second create.
func TestCreateWebhook_KeyReuseDifferentBody422(t *testing.T) {
	var creates int
	srv := webhookCreateIdemServer(t, countingWebhookCreate(&creates))
	url := srv.URL + "/v1/webhooks"

	code1, _ := createWebhookCall(t, url, "good", "whk-2", whCreateBody)
	code2, b2 := createWebhookCall(t, url, "good", "whk-2", `{"url":"https://example.com/DIFFERENT","events":["email.received"]}`)

	if code1 != 201 {
		t.Fatalf("first create: want 201, got %d", code1)
	}
	if code2 != 422 || errCode(b2) != "idempotency_key_reuse" {
		t.Fatalf("same key + different body: want 422 idempotency_key_reuse, got %d %v", code2, b2)
	}
	if creates != 1 {
		t.Fatalf("key reuse must not create: creates=%d, want 1", creates)
	}
}

// While the first keyed request is still executing, a concurrent duplicate is
// a 409 idempotency_in_flight — never a second create.
func TestCreateWebhook_InFlight409(t *testing.T) {
	var creates int
	inner := countingWebhookCreate(&creates)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	srv := webhookCreateIdemServer(t, func(ctx context.Context, userID, url, description string, events []string, filters identity.WebhookFilters, idemCompleteTx identity.WebhookIdemCompleter) (*identity.Webhook, error) {
		once.Do(func() { close(entered) })
		<-release
		return inner(ctx, userID, url, description, events, filters, idemCompleteTx)
	})
	url := srv.URL + "/v1/webhooks"

	first := make(chan int, 1)
	go func() {
		code, _ := createWebhookCall(t, url, "good", "whk-3", whCreateBody)
		first <- code
	}()
	<-entered // first request holds the claim inside CreateWebhook

	code2, b2 := createWebhookCall(t, url, "good", "whk-3", whCreateBody)
	if code2 != 409 || errCode(b2) != "idempotency_in_flight" {
		t.Fatalf("concurrent duplicate: want 409 idempotency_in_flight, got %d %v", code2, b2)
	}

	close(release)
	if code1 := <-first; code1 != 201 {
		t.Fatalf("original request: want 201, got %d", code1)
	}
	if creates != 1 {
		t.Fatalf("in-flight collision must not create twice: creates=%d, want 1", creates)
	}
}

// Idempotency keys are scoped per authenticated account: two accounts may use
// the same key without colliding (each gets its own webhook).
func TestCreateWebhook_DifferentAccountsSameKey(t *testing.T) {
	var creates int
	srv := webhookCreateIdemServer(t, countingWebhookCreate(&creates))
	url := srv.URL + "/v1/webhooks"

	code1, b1 := createWebhookCall(t, url, "good", "whk-shared", whCreateBody)
	code2, b2 := createWebhookCall(t, url, "other", "whk-shared", whCreateBody)

	if code1 != 201 || code2 != 201 {
		t.Fatalf("want 201/201, got %d/%d (b1=%v b2=%v)", code1, code2, b1, b2)
	}
	if creates != 2 {
		t.Fatalf("distinct accounts must each create: creates=%d, want 2", creates)
	}
	if b1["id"] == b2["id"] {
		t.Fatalf("distinct accounts must get distinct webhooks, both got %v", b1["id"])
	}
}

// Without a key, intentional duplicate subscriptions — including two
// subscriptions to the SAME URL — remain allowed (idempotency is opt-in;
// multiple webhooks per URL is a supported pattern, see the no-unique(url)
// decision).
func TestCreateWebhook_NoKeyDuplicatesAllowed(t *testing.T) {
	var creates int
	srv := webhookCreateIdemServer(t, countingWebhookCreate(&creates))
	url := srv.URL + "/v1/webhooks"

	code1, b1 := createWebhookCall(t, url, "good", "", whCreateBody)
	code2, b2 := createWebhookCall(t, url, "good", "", whCreateBody) // same URL, no key

	if code1 != 201 || code2 != 201 {
		t.Fatalf("want 201/201, got %d/%d", code1, code2)
	}
	if creates != 2 {
		t.Fatalf("unkeyed duplicates must both create: creates=%d, want 2", creates)
	}
	if b1["id"] == b2["id"] {
		t.Fatalf("unkeyed duplicates must be distinct subscriptions, both got %v", b1["id"])
	}
}

// A validation failure happens strictly before the side effect, so it must
// RELEASE the key (runIdempotent's fn-error contract) — a later request may
// reuse the key with a corrected (different) body and succeed.
func TestCreateWebhook_ValidationFailureReleasesKey(t *testing.T) {
	var creates int
	srv := webhookCreateIdemServer(t, countingWebhookCreate(&creates))
	url := srv.URL + "/v1/webhooks"

	// http:// fails the SSRF/scheme validation (agent.ValidateWebhookURL).
	code1, b1 := createWebhookCall(t, url, "good", "whk-4", `{"url":"http://example.com/hook","events":["email.received"]}`)
	if code1 != 400 || errCode(b1) != "invalid_webhook_url" {
		t.Fatalf("want 400 invalid_webhook_url, got %d %v", code1, b1)
	}
	if creates != 0 {
		t.Fatalf("validation failure must not create: creates=%d", creates)
	}

	// The corrected retry has a DIFFERENT body: only a released key allows it
	// (a consumed key would 422, an orphaned in-flight claim would 409).
	code2, b2 := createWebhookCall(t, url, "good", "whk-4", whCreateBody)
	if code2 != 201 {
		t.Fatalf("key must be reusable after a validation failure: want 201, got %d %v", code2, b2)
	}
	if creates != 1 {
		t.Fatalf("creates=%d, want 1", creates)
	}
}

// The webhook-limit error surfaces before any insert, so it follows the same
// release behavior: the key is not consumed.
func TestCreateWebhook_LimitErrorReleasesKey(t *testing.T) {
	var creates int
	srv := webhookCreateIdemServer(t, countingWebhookCreate(&creates))
	url := srv.URL + "/v1/webhooks"

	code1, b1 := createWebhookCall(t, url, "good", "whk-5", `{"url":"https://example.com/capped","events":["email.received"]}`)
	if code1 != 400 || errCode(b1) != "webhook_limit_reached" {
		t.Fatalf("want 400 webhook_limit_reached, got %d %v", code1, b1)
	}
	code2, b2 := createWebhookCall(t, url, "good", "whk-5", whCreateBody)
	if code2 != 201 {
		t.Fatalf("key must be reusable after a limit error: want 201, got %d %v", code2, b2)
	}
	if creates != 1 {
		t.Fatalf("creates=%d, want 1", creates)
	}
}

// A transient store failure (500) also releases the key: the retry re-executes
// (at-least-once, per the documented fn contract) instead of being locked out.
func TestCreateWebhook_TransientFailureReleasesKey(t *testing.T) {
	var creates int
	inner := countingWebhookCreate(&creates)
	failFirst := true
	srv := webhookCreateIdemServer(t, func(ctx context.Context, userID, url, description string, events []string, filters identity.WebhookFilters, idemCompleteTx identity.WebhookIdemCompleter) (*identity.Webhook, error) {
		if failFirst {
			failFirst = false
			return nil, errors.New("transient db error")
		}
		return inner(ctx, userID, url, description, events, filters, idemCompleteTx)
	})
	url := srv.URL + "/v1/webhooks"

	code1, b1 := createWebhookCall(t, url, "good", "whk-6", whCreateBody)
	if code1 != 500 || errCode(b1) != "internal_error" {
		t.Fatalf("want 500 internal_error, got %d %v", code1, b1)
	}
	code2, b2 := createWebhookCall(t, url, "good", "whk-6", whCreateBody)
	if code2 != 201 {
		t.Fatalf("retry after transient failure: want 201, got %d %v", code2, b2)
	}
	if creates != 1 {
		t.Fatalf("creates=%d, want 1", creates)
	}
}
