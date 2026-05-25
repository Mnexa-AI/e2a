package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// idempotencyHeaders builds the Authorization + Idempotency-Key header
// set used across these tests. Returns a request ready for http.Do.
func idempotencyRequest(t *testing.T, method, url, body, apiKey, idemKey string) *http.Request {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	return req
}

// setupVerifiedSendingAgent provisions a fully-onboarded sending agent
// keyed off the test name. Returns the server URL, the SMTP capture
// closure, and the bearer API key.
func setupVerifiedSendingAgent(t *testing.T, namePrefix string) (serverURL string, smtpDone func() []testutil.SMTPMessage, apiKey string) {
	t.Helper()
	srv, store, _, done := setupAPIWithSMTP(t)
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, namePrefix+"-owner@example.com", "Owner", "google-"+namePrefix)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	keyObj, err := store.CreateAPIKey(ctx, user.ID, namePrefix+"-key", nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	domain := namePrefix + ".example.com"
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	if _, err := store.CreateAgent(ctx, "agent@"+domain, domain, "", "https://example.com/webhook", "", user.ID); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return srv.URL, done, keyObj.PlaintextKey
}

func TestSendEmail_IdempotencyKey_DuplicateReplaysAndSkipsResend(t *testing.T) {
	serverURL, smtpDone, apiKey := setupVerifiedSendingAgent(t, "idem-replay")

	payload := `{"to":["alice@example.com"],"subject":"Hi","body":"first body"}`
	idemKey := "key-replay-001"

	// First call: should send once and return 200.
	resp1, err := http.DefaultClient.Do(idempotencyRequest(t, "POST", serverURL+"/api/v1/send", payload, apiKey, idemKey))
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if resp1.StatusCode != 200 {
		t.Fatalf("first status = %d, body = %s", resp1.StatusCode, body1)
	}
	if resp1.Header.Get("Idempotent-Replayed") != "" {
		t.Errorf("first call should not be marked replayed, got %q", resp1.Header.Get("Idempotent-Replayed"))
	}

	// Second call: same key + same body must NOT re-send; response must be byte-identical.
	resp2, err := http.DefaultClient.Do(idempotencyRequest(t, "POST", serverURL+"/api/v1/send", payload, apiKey, idemKey))
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("second status = %d, body = %s", resp2.StatusCode, body2)
	}
	if resp2.Header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("Idempotent-Replayed = %q, want \"true\"", resp2.Header.Get("Idempotent-Replayed"))
	}
	if !bytes.Equal(body1, body2) {
		t.Errorf("replay body diverged:\nfirst:  %s\nsecond: %s", body1, body2)
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Errorf("SMTP messages sent = %d, want exactly 1 (replay must not re-hit SMTP)", len(msgs))
	}
}

func TestSendEmail_IdempotencyKey_DifferentBodyReturns422(t *testing.T) {
	serverURL, smtpDone, apiKey := setupVerifiedSendingAgent(t, "idem-mismatch")

	idemKey := "key-mismatch-001"
	payloadA := `{"to":["alice@example.com"],"subject":"A","body":"first"}`
	payloadB := `{"to":["alice@example.com"],"subject":"B","body":"different"}`

	resp1, _ := http.DefaultClient.Do(idempotencyRequest(t, "POST", serverURL+"/api/v1/send", payloadA, apiKey, idemKey))
	if resp1.StatusCode != 200 {
		t.Fatalf("first status = %d", resp1.StatusCode)
	}
	resp1.Body.Close()

	resp2, _ := http.DefaultClient.Do(idempotencyRequest(t, "POST", serverURL+"/api/v1/send", payloadB, apiKey, idemKey))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(resp2.Body)
		t.Errorf("second status = %d, want 422; body=%s", resp2.StatusCode, body)
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Errorf("SMTP messages sent = %d, want 1 (mismatch must not trigger second send)", len(msgs))
	}
}

func TestSendEmail_IdempotencyKey_TooLongReturns400(t *testing.T) {
	serverURL, _, apiKey := setupVerifiedSendingAgent(t, "idem-long")

	tooLong := strings.Repeat("x", 300)
	payload := `{"to":["alice@example.com"],"subject":"x","body":"y"}`

	resp, err := http.DefaultClient.Do(idempotencyRequest(t, "POST", serverURL+"/api/v1/send", payload, apiKey, tooLong))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 400; body=%s", resp.StatusCode, body)
	}
}

func TestSendEmail_NoIdempotencyKey_BehavesAsBefore(t *testing.T) {
	// Existing /send tests cover the no-header path implicitly; this
	// pins down that wiring the idempotency store has not introduced
	// a regression for plain POSTs that omit the header.
	serverURL, smtpDone, apiKey := setupVerifiedSendingAgent(t, "idem-none")

	payload := `{"to":["alice@example.com"],"subject":"x","body":"y"}`

	for i := 0; i < 2; i++ {
		resp, err := http.DefaultClient.Do(idempotencyRequest(t, "POST", serverURL+"/api/v1/send", payload, apiKey, ""))
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("send %d status = %d", i, resp.StatusCode)
		}
		var sendResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&sendResp)
		resp.Body.Close()
		if sendResp["message_id"] == "" {
			t.Errorf("send %d: missing message_id", i)
		}
	}

	msgs := smtpDone()
	if len(msgs) != 2 {
		t.Errorf("SMTP messages sent = %d, want 2 (no header => no dedup)", len(msgs))
	}
}

func TestSendEmail_IdempotencyKey_ConcurrentSameKey_OnlyOneSends(t *testing.T) {
	serverURL, smtpDone, apiKey := setupVerifiedSendingAgent(t, "idem-conc")

	payload := `{"to":["alice@example.com"],"subject":"x","body":"y"}`
	const idemKey = "key-conc-001"
	const N = 6

	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		twoXX       int
		conflicts   int
		others      int
	)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			resp, err := http.DefaultClient.Do(idempotencyRequest(t, "POST", serverURL+"/api/v1/send", payload, apiKey, idemKey))
			if err != nil {
				t.Errorf("request error: %v", err)
				return
			}
			defer resp.Body.Close()
			mu.Lock()
			defer mu.Unlock()
			switch {
			case resp.StatusCode >= 200 && resp.StatusCode < 300:
				twoXX++
			case resp.StatusCode == http.StatusConflict:
				conflicts++
			default:
				others++
			}
		}()
	}
	wg.Wait()

	// All N callers should either succeed (the original + any replays
	// after Complete) or get 409 while the original is in-flight; no
	// 5xx, no 422.
	if others != 0 {
		t.Errorf("unexpected non-2xx/non-409 responses = %d", others)
	}
	if twoXX+conflicts != N {
		t.Errorf("twoXX(%d) + conflicts(%d) != N(%d)", twoXX, conflicts, N)
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Errorf("SMTP messages sent = %d, want exactly 1 across %d concurrent same-key requests", len(msgs), N)
	}
}

func TestReplyToMessage_IdempotencyKey_DuplicateReplays(t *testing.T) {
	srv, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "idem-reply-owner@example.com", "Owner", "google-idem-reply")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "idem-reply-key", nil)
	_, _ = store.ClaimOrCreateDomain(ctx, "idem-reply.example.com", user.ID)
	_ = store.VerifyDomain(ctx, "idem-reply.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@idem-reply.example.com", "idem-reply.example.com", "", "https://example.com/webhook", "", user.ID)

	// Synthesize an inbound to reply to.
	inboundRaw := []byte("From: sender@example.com\r\nTo: agent@idem-reply.example.com\r\nMessage-ID: <m1@example.com>\r\nSubject: Hello\r\n\r\nHello body\r\n")
	inMsg, err := store.CreateInboundMessage(ctx, "", a.ID, "sender@example.com", "agent@idem-reply.example.com", "<m1@example.com>", "Hello", "", "", inboundRaw, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}

	url := srv.URL + "/api/v1/agents/agent@idem-reply.example.com/messages/" + inMsg.ID + "/reply"
	payload := `{"body":"my reply"}`
	idemKey := "key-reply-001"

	resp1, _ := http.DefaultClient.Do(idempotencyRequest(t, "POST", url, payload, apiKeyObj.PlaintextKey, idemKey))
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if resp1.StatusCode != 200 {
		t.Fatalf("first status = %d, body=%s", resp1.StatusCode, body1)
	}

	resp2, _ := http.DefaultClient.Do(idempotencyRequest(t, "POST", url, payload, apiKeyObj.PlaintextKey, idemKey))
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("second status = %d, body=%s", resp2.StatusCode, body2)
	}
	if resp2.Header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("Idempotent-Replayed = %q, want \"true\"", resp2.Header.Get("Idempotent-Replayed"))
	}
	if !bytes.Equal(body1, body2) {
		t.Errorf("replay body diverged")
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Errorf("SMTP messages sent = %d, want 1 (reply replay must not re-send)", len(msgs))
	}
}

func TestSendEmail_IdempotencyKey_ClientErrorReleasesKey(t *testing.T) {
	// 4xx response must release the key so the caller can retry with a
	// corrected payload using the SAME key (rather than being locked
	// into the bad-response cache for 24h).
	serverURL, smtpDone, apiKey := setupVerifiedSendingAgent(t, "idem-4xx")

	idemKey := "key-4xx-001"
	// Missing subject + body — handler returns 400.
	bad := `{"to":["alice@example.com"]}`
	good := `{"to":["alice@example.com"],"subject":"hi","body":"ok"}`

	resp1, _ := http.DefaultClient.Do(idempotencyRequest(t, "POST", serverURL+"/api/v1/send", bad, apiKey, idemKey))
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusBadRequest {
		t.Fatalf("first status = %d, want 400", resp1.StatusCode)
	}

	// Same key, valid payload now — should succeed (key was Released).
	resp2, _ := http.DefaultClient.Do(idempotencyRequest(t, "POST", serverURL+"/api/v1/send", good, apiKey, idemKey))
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("second status = %d, want 200 (key should have been released after 400)", resp2.StatusCode)
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Errorf("SMTP messages sent = %d, want 1 (only the second call should send)", len(msgs))
	}
}
