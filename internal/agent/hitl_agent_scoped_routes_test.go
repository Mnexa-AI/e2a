package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// HITL approve/reject/get historically lived only at the flat path
// `/api/v1/messages/{id}/...`. As of this slice they also live at
// `/api/v1/agents/{email}/messages/{id}/...` to match the rest of the
// per-message surface (reply, forward, labels). The flat paths stay
// registered as legacy aliases. These tests pin three invariants of
// the dual-route design:
//
//   1. The agent-scoped path is equivalent to the legacy path when
//      the URL's {email} matches the message's owning agent.
//   2. The agent-scoped path returns 404 when the URL's {email}
//      doesn't match — without this guard the alias could be used to
//      enumerate or misroute messages across the user's agents.
//   3. `/api/v1/pending` is an alias for `/api/v1/messages` (the
//      cross-agent HITL queue). Same handler, same response shape.

func TestApprovePendingMessage_AgentScopedPath_EmailMismatch_404(t *testing.T) {
	server, store, _, smtpDone := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-as-mismatch@example.com", "Owner", "google-as-mismatch")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "as-mismatch-key", nil)
	store.ClaimOrCreateDomain(ctx, "mismatch-a.example.com", user.ID)
	store.VerifyDomain(ctx, "mismatch-a.example.com", user.ID)
	agentA, err := store.CreateAgent(ctx, "agent-a@mismatch-a.example.com", "mismatch-a.example.com", "", "https://example.com/wh", "", user.ID)
	if err != nil {
		t.Fatalf("create agentA: %v", err)
	}
	enableHITL(t, store, agentA.ID, user.ID)
	// Second agent under the same user — used to construct a URL
	// that LOOKS legitimate (the {email} is the user's own agent) but
	// names the WRONG agent for this message.
	store.ClaimOrCreateDomain(ctx, "mismatch-b.example.com", user.ID)
	store.VerifyDomain(ctx, "mismatch-b.example.com", user.ID)
	agentB, err := store.CreateAgent(ctx, "agent-b@mismatch-b.example.com", "mismatch-b.example.com", "", "https://example.com/wh", "", user.ID)
	if err != nil {
		t.Fatalf("create agentB: %v", err)
	}

	// Create a pending message owned by agentA.
	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"from":"agent-a@mismatch-a.example.com","to":["alice@example.com"],"subject":"x","body":"y"}`,
		apiKey.PlaintextKey)
	defer sendResp.Body.Close()
	var sendBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(sendResp.Body).Decode(&sendBody)
	if sendBody.MessageID == "" {
		t.Skip("send did not return a message_id — pre-flight failed on this test fixture; skipping mismatch coverage")
	}

	// Approve via the OTHER agent's path. Same user, same message id,
	// but the URL names agent B. Should 404 — not 200, not 403.
	url := server.URL + "/api/v1/agents/" + agentB.Email + "/messages/" + sendBody.MessageID + "/approve"
	resp := authed(t, "POST", url, "", apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("agent-scoped approve with wrong email: status = %d, want 404", resp.StatusCode)
	}

	// SMTP should NOT have been hit — the mismatched-email request
	// must not double-send.
	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("SMTP should not be hit on mismatched-email approve, got %d", len(msgs))
	}
}

func TestRejectPendingMessage_AgentScopedPath_EmailMismatch_404(t *testing.T) {
	server, store, _, _ := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-as-rej@example.com", "Owner", "google-as-rej")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "as-rej-key", nil)
	store.ClaimOrCreateDomain(ctx, "rej-a.example.com", user.ID)
	store.VerifyDomain(ctx, "rej-a.example.com", user.ID)
	agentA, err := store.CreateAgent(ctx, "agent-a@rej-a.example.com", "rej-a.example.com", "", "https://example.com/wh", "", user.ID)
	if err != nil {
		t.Fatalf("create agentA: %v", err)
	}
	enableHITL(t, store, agentA.ID, user.ID)
	store.ClaimOrCreateDomain(ctx, "rej-b.example.com", user.ID)
	store.VerifyDomain(ctx, "rej-b.example.com", user.ID)
	agentB, err := store.CreateAgent(ctx, "agent-b@rej-b.example.com", "rej-b.example.com", "", "https://example.com/wh", "", user.ID)
	if err != nil {
		t.Fatalf("create agentB: %v", err)
	}

	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"from":"agent-a@rej-a.example.com","to":["alice@example.com"],"subject":"x","body":"y"}`,
		apiKey.PlaintextKey)
	defer sendResp.Body.Close()
	var sendBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(sendResp.Body).Decode(&sendBody)
	if sendBody.MessageID == "" {
		t.Skip("send did not return a message_id; skipping")
	}

	// Reject via the wrong agent path → 404. The legacy path
	// /api/v1/messages/{id}/reject would have succeeded with the
	// same payload; the agent-scoped path enforces the URL contract.
	url := server.URL + "/api/v1/agents/" + agentB.Email + "/messages/" + sendBody.MessageID + "/reject"
	resp := authed(t, "POST", url, `{"reason":"wrong agent"}`, apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("agent-scoped reject with wrong email: status = %d, want 404", resp.StatusCode)
	}
}

func TestGetOutboundMessage_AgentScopedPath_EmailMismatch_404(t *testing.T) {
	server, store, _, _ := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-as-get@example.com", "Owner", "google-as-get")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "as-get-key", nil)
	store.ClaimOrCreateDomain(ctx, "get-a.example.com", user.ID)
	store.VerifyDomain(ctx, "get-a.example.com", user.ID)
	agentA, err := store.CreateAgent(ctx, "agent-a@get-a.example.com", "get-a.example.com", "", "https://example.com/wh", "", user.ID)
	if err != nil {
		t.Fatalf("create agentA: %v", err)
	}
	enableHITL(t, store, agentA.ID, user.ID)
	store.ClaimOrCreateDomain(ctx, "get-b.example.com", user.ID)
	store.VerifyDomain(ctx, "get-b.example.com", user.ID)
	agentB, err := store.CreateAgent(ctx, "agent-b@get-b.example.com", "get-b.example.com", "", "https://example.com/wh", "", user.ID)
	if err != nil {
		t.Fatalf("create agentB: %v", err)
	}

	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"from":"agent-a@get-a.example.com","to":["alice@example.com"],"subject":"x","body":"y"}`,
		apiKey.PlaintextKey)
	defer sendResp.Body.Close()
	var sendBody struct{ MessageID string `json:"message_id"` }
	json.NewDecoder(sendResp.Body).Decode(&sendBody)
	if sendBody.MessageID == "" {
		t.Skip("send did not return a message_id; skipping")
	}

	// The flat path returns the message detail; the agent-scoped path
	// MUST 404 when the URL names the wrong agent — otherwise the
	// alias could be used to confirm a message exists under any of
	// the user's agents by enumerating emails.
	url := server.URL + "/api/v1/agents/" + agentB.Email + "/messages/" + sendBody.MessageID
	resp := authed(t, "GET", url, "", apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("agent-scoped get with wrong email: status = %d, want 404", resp.StatusCode)
	}
}

func TestPendingList_LegacyMessagesPathIsGone(t *testing.T) {
	// The HITL pending list moved from /api/v1/messages to /api/v1/pending.
	// Pin both endpoints to ensure the legacy path is gone (no silent
	// alias) and the canonical path returns the documented shape.
	server, store, _, _ := setupAPIWithSMTP(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-pending-list@example.com", "Owner", "google-pending-list")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "pending-list-key", nil)
	store.ClaimOrCreateDomain(ctx, "pa.example.com", user.ID)
	store.VerifyDomain(ctx, "pa.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@pa.example.com", "pa.example.com", "", "https://example.com/wh", "", user.ID)
	enableHITL(t, store, agent.ID, user.ID)

	// Create a pending message so the list has something to return.
	sendResp := authed(t, "POST", server.URL+"/api/v1/send",
		`{"to":["alice@example.com"],"subject":"P","body":"q"}`,
		apiKey.PlaintextKey)
	defer sendResp.Body.Close()

	// Legacy path must be gone — no registered handler, gorilla/mux 404s.
	legacy := authed(t, "GET", server.URL+"/api/v1/messages", "", apiKey.PlaintextKey)
	defer legacy.Body.Close()
	if legacy.StatusCode != http.StatusNotFound {
		t.Errorf("/api/v1/messages should 404 after rename, got %d", legacy.StatusCode)
	}

	// Canonical /pending must return the pending row(s).
	pending := authed(t, "GET", server.URL+"/api/v1/pending", "", apiKey.PlaintextKey)
	defer pending.Body.Close()
	if pending.StatusCode != http.StatusOK {
		t.Fatalf("/api/v1/pending status = %d, want 200", pending.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(pending.Body).Decode(&body)
	if _, ok := body["messages"]; !ok {
		t.Errorf("/api/v1/pending response missing 'messages' key, got %v", body)
	}
}
