package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
)

// Integration tests for the side-effect-committed caching policy on
// /api/v1/send and /api/v1/agents/{email}/messages/{id}/reply.
//
// The standard handlers don't have a natural code path to "5xx after
// SES already accepted" — that scenario is a panic recovery, late
// context cancel, or a follow-up DB write that returned 5xx. To
// exercise the guard's caching decision in this branch without
// fragile timing tricks, these tests register their own synthetic
// handlers using the test-only shims in export_test.go.

// newAPIWithIdempotency mirrors setupAPI from api_test.go but returns
// the API value directly (rather than the bound httptest.Server) so a
// test can register its own routes against the same store + guard.
func newAPIWithIdempotency(t *testing.T) (*agent.API, *identity.Store, string) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotency.NewStore(pool))

	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "guard-sideeffect-owner@example.com", "Owner", "google-guard-sideeffect-"+t.Name())
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	key, err := store.CreateAPIKey(ctx, user.ID, "guard-sideeffect-key", nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	return api, store, key.PlaintextKey
}

func TestIdempotencyGuard_5xxAfterSideEffect_CachesResponse(t *testing.T) {
	api, _, apiKey := newAPIWithIdempotency(t)

	var handlerCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/test/late-5xx", func(w http.ResponseWriter, r *http.Request) {
		uid, err := api.AuthenticateUserForTest(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		replayed, captureW, finalize := api.IdempotencyGuardForTest(w, r, uid, bodyBytes)
		if replayed {
			return
		}
		defer finalize()
		w = captureW

		// Simulate "SES accepted the message" / "loopback rows
		// written" — the irreversible step.
		handlerCalls++
		agent.MarkSideEffectCommittedForTest(w)

		// Simulate a late failure post-side-effect.
		http.Error(w, "simulated late failure", http.StatusInternalServerError)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	payload := `{"hello":"world"}`
	idemKey := "key-5xx-cache-001"

	send := func(label string) (status int, body []byte, replayedHdr string) {
		t.Helper()
		req, _ := http.NewRequest("POST", server.URL+"/test/late-5xx", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Idempotency-Key", idemKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b, resp.Header.Get("Idempotent-Replayed")
	}

	status1, body1, replayed1 := send("first")
	if status1 != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", status1)
	}
	if replayed1 != "" {
		t.Errorf("first response unexpectedly marked replayed: %q", replayed1)
	}

	// Second call with same key + same body MUST replay the cached
	// 500 — the handler body MUST NOT run again. This is the
	// double-send protection for the immediate /send and /reply
	// paths: a 5xx coming from anywhere AFTER the upstream send
	// accepted must lock the caller into the cached error rather
	// than letting them retry blindly.
	status2, body2, replayed2 := send("second")
	if status2 != http.StatusInternalServerError {
		t.Fatalf("second status = %d, want 500 (cached)", status2)
	}
	if replayed2 != "true" {
		t.Errorf("second response Idempotent-Replayed = %q, want \"true\"", replayed2)
	}
	if !bytes.Equal(body1, body2) {
		t.Errorf("cached response diverged:\nfirst:  %q\nsecond: %q", body1, body2)
	}
	if handlerCalls != 1 {
		t.Errorf("handler ran %d time(s), want exactly 1 (replay must not re-invoke)", handlerCalls)
	}
}

// Negative case: when the handler hits a 5xx BEFORE the side effect
// committed (e.g. upstream send error, early validation), the key
// must be Released so a retry with the same key can succeed.
// This is the existing Release-on-error behavior — verifying it
// still holds when the side-effect flag is NOT flipped.
func TestIdempotencyGuard_5xxWithoutSideEffect_ReleasesKey(t *testing.T) {
	api, _, apiKey := newAPIWithIdempotency(t)

	var handlerCalls int
	failFirstCall := true

	mux := http.NewServeMux()
	mux.HandleFunc("/test/early-5xx", func(w http.ResponseWriter, r *http.Request) {
		uid, err := api.AuthenticateUserForTest(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		bodyBytes, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		replayed, captureW, finalize := api.IdempotencyGuardForTest(w, r, uid, bodyBytes)
		if replayed {
			return
		}
		defer finalize()
		w = captureW

		handlerCalls++
		if failFirstCall {
			// Simulate an EARLY failure — upstream send rejected, or
			// a validation error after the body decode. NO side
			// effect committed yet.
			http.Error(w, "simulated upstream failure", http.StatusInternalServerError)
			return
		}
		agent.MarkSideEffectCommittedForTest(w)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	send := func(label string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", server.URL+"/test/early-5xx", strings.NewReader(`{"hello":"world"}`))
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Idempotency-Key", "key-5xx-release-001")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if got := send("first"); got != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", got)
	}

	// Now allow the handler to succeed. Retry with the SAME key —
	// must be accepted because the first 5xx released the slot.
	failFirstCall = false
	if got := send("second"); got != http.StatusOK {
		t.Fatalf("second status = %d, want 200 (key should have been released after first 5xx)", got)
	}
	if handlerCalls != 2 {
		t.Errorf("handler ran %d time(s), want exactly 2 (retry must be allowed)", handlerCalls)
	}
}

// End-to-end on the real /api/v1/send path: SES error → handler
// returns 5xx → key released → retry allowed. Exercises the real
// handleSendEmail wiring rather than a synthetic harness, to verify
// markSideEffectCommitted is positioned correctly (it must NOT
// already have fired by the time sender.Send errors).
func TestSendEmail_IdempotencyKey_SenderErrorReleasesKey(t *testing.T) {
	// Point the outbound relay at an unreachable address so
	// sender.Send returns an error without ever accepting the
	// message at the SMTP layer.
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{
		Host: "127.0.0.1",
		Port: 1, // reserved; will fail to connect
	})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotency.NewStore(pool))

	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "send-err-owner@example.com", "Owner", "google-send-err")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "send-err-key", nil)
	_, _ = store.ClaimOrCreateDomain(ctx, "send-err.example.com", user.ID)
	_ = store.VerifyDomain(ctx, "send-err.example.com", user.ID)
	_, _ = store.CreateAgent(ctx, "agent@send-err.example.com", "send-err.example.com", "", "https://example.com/webhook", "", user.ID)

	router := mux.NewRouter()
	api.RegisterRoutes(router)
	srv := httptest.NewServer(router)
	defer srv.Close()

	payload := `{"to":["alice@example.com"],"subject":"x","body":"y"}`
	idemKey := "key-sender-err-001"

	post := func(label string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/send", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
		req.Header.Set("Idempotency-Key", idemKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// First call: SMTP relay unreachable → handleSendEmail returns 500.
	if got := post("first"); got != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", got)
	}

	// Second call with same key — must be allowed (released, not
	// cached) because no SES side effect committed. Will also return
	// 500 because the relay is still unreachable, but the handler
	// MUST be invoked rather than serving a cached 500.
	if got := post("second"); got != http.StatusInternalServerError {
		t.Fatalf("second status = %d, want 500 (relay still unreachable)", got)
	}
	// The Idempotent-Replayed header should NOT be set on the
	// second call — that would indicate cache rather than re-run.
	req3, _ := http.NewRequest("POST", srv.URL+"/api/v1/send", strings.NewReader(payload))
	req3.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	req3.Header.Set("Idempotency-Key", idemKey)
	req3.Header.Set("Content-Type", "application/json")
	resp3, _ := http.DefaultClient.Do(req3)
	defer resp3.Body.Close()
	if resp3.Header.Get("Idempotent-Replayed") == "true" {
		t.Error("Idempotent-Replayed=true on 5xx retry — key should have been released, not cached")
	}
}

// TestApprovePending_IdempotencyKey_SenderErrorReleasesKey mirrors the
// /send sender-error test for the approve path: when the upstream
// SMTP relay is unreachable, ApproveAndSend's send callback returns
// an error BEFORE the row transitions to 'sent', so the side-effect
// flag is never set on the response writer. The guard must release
// the key — otherwise a retry would replay a stale 500 instead of
// actually getting the message out once the relay comes back.
//
// Pair this with TestApprovePending_IdempotencyKey_DuplicateReplaysAndSkipsResend
// (in idempotency_api_test.go, happy-path side) — together they
// pin both branches of the side-effect-committed decision for the
// approve handler.
func TestApprovePending_IdempotencyKey_SenderErrorReleasesKey(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	// Unreachable relay → sender.Send returns an error inside
	// ApproveAndSend's callback, so ApproveAndSend returns non-nil
	// err and the approve handler 500s BEFORE reaching the
	// markSideEffectCommitted call.
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{
		Host: "127.0.0.1",
		Port: 1, // reserved; will fail to connect
	})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotency.NewStore(pool))

	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "approve-relay-err@example.com", "Owner", "google-approve-relay-err")
	apiKeyObj, _ := store.CreateAPIKey(ctx, user.ID, "approve-relay-err-key", nil)
	_, _ = store.ClaimOrCreateDomain(ctx, "approve-relay-err.example.com", user.ID)
	_ = store.VerifyDomain(ctx, "approve-relay-err.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@approve-relay-err.example.com", "approve-relay-err.example.com", "", "https://example.com/webhook", "", user.ID)
	// Flip HITL on so /send produces a pending row instead of sending.
	enableHITL(t, store, a.ID, user.ID)

	router := mux.NewRouter()
	api.RegisterRoutes(router)
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Create a pending message — the relay isn't hit here (status 202
	// pending_approval), so this succeeds even though the relay is
	// unreachable.
	sendReq, _ := http.NewRequest("POST", srv.URL+"/api/v1/send",
		strings.NewReader(`{"to":["alice@example.com"],"subject":"draft","body":"draft body"}`))
	sendReq.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
	sendReq.Header.Set("Content-Type", "application/json")
	sendResp, err := http.DefaultClient.Do(sendReq)
	if err != nil {
		t.Fatalf("setup send: %v", err)
	}
	defer sendResp.Body.Close()
	if sendResp.StatusCode != http.StatusAccepted && sendResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sendResp.Body)
		t.Fatalf("setup send: status=%d body=%s", sendResp.StatusCode, body)
	}
	var sb struct{ MessageID string `json:"message_id"` }
	if err := json.NewDecoder(sendResp.Body).Decode(&sb); err != nil {
		t.Fatalf("setup send: decode body: %v", err)
	}
	if sb.MessageID == "" {
		t.Fatal("setup send: no message_id")
	}

	idemKey := "approve-relay-err-key-001"
	approveURL := srv.URL + "/api/v1/messages/" + sb.MessageID + "/approve"

	approve := func(label string) (status int, replayed string) {
		t.Helper()
		req, _ := http.NewRequest("POST", approveURL, strings.NewReader(""))
		req.Header.Set("Authorization", "Bearer "+apiKeyObj.PlaintextKey)
		req.Header.Set("Idempotency-Key", idemKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Idempotent-Replayed")
	}

	// First approve: the relay is unreachable, sender.Send errors,
	// ApproveAndSend returns an error path that yields 500 from the
	// handler. Critically, markSideEffectCommitted is NOT reached.
	status1, replayed1 := approve("first")
	if status1 != http.StatusInternalServerError {
		t.Fatalf("first approve status = %d, want 500 (relay unreachable)", status1)
	}
	if replayed1 != "" {
		t.Errorf("first call unexpectedly marked replayed: %q", replayed1)
	}

	// Second approve with the same key: handler MUST run again
	// (the guard released the key on the 500) — otherwise a
	// transient relay outage would lock the reviewer out of ever
	// retrying. Status is still 500 because the relay is still
	// down, but Idempotent-Replayed MUST NOT be "true".
	status2, replayed2 := approve("second")
	if status2 != http.StatusInternalServerError {
		t.Fatalf("second approve status = %d, want 500 (relay still down)", status2)
	}
	if replayed2 == "true" {
		t.Error("Idempotent-Replayed=true on retry after pre-side-effect 5xx — key must release, not cache")
	}
}

