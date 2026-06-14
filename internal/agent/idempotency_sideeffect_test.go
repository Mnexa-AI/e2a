package agent_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
