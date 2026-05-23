package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// authedPut is a sibling to authedPost for the new PUT endpoint.
func authedPut(t *testing.T, url, payload, apiKey string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("PUT", url, bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestUpdateAgentHITLViaV1Endpoint(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-v1-up@example.com", "Owner", "google-v1-up")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "v1-up-key", nil)
	store.ClaimOrCreateDomain(ctx, "v1up.example.com", user.ID)
	store.VerifyDomain(ctx, "v1up.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@v1up.example.com", "v1up.example.com", "", "https://example.com/webhook", "", user.ID)

	payload := `{"hitl_enabled":true,"hitl_ttl_seconds":3600,"hitl_expiration_action":"approve"}`
	resp := authedPut(t, server.URL+"/api/v1/agents/"+agent.ID, payload, apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["hitl_enabled"] != true {
		t.Errorf("hitl_enabled = %v", body["hitl_enabled"])
	}
	if body["hitl_ttl_seconds"].(float64) != 3600 {
		t.Errorf("hitl_ttl_seconds = %v", body["hitl_ttl_seconds"])
	}
	if body["hitl_expiration_action"] != "approve" {
		t.Errorf("hitl_expiration_action = %v", body["hitl_expiration_action"])
	}

	// DB state reflects the update
	got, _ := store.GetAgentByID(ctx, agent.ID)
	if !got.HITLEnabled || got.HITLTTLSeconds != 3600 || got.HITLExpirationAction != "approve" {
		t.Errorf("DB state mismatch: %+v", got)
	}
}

func TestUpdateAgentPartialHITLPreservesOthers(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-v1-part@example.com", "Owner", "google-v1-part")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "v1-part-key", nil)
	store.ClaimOrCreateDomain(ctx, "v1part.example.com", user.ID)
	store.VerifyDomain(ctx, "v1part.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@v1part.example.com", "v1part.example.com", "", "https://example.com/webhook", "", user.ID)
	if err := store.UpdateAgentHITL(ctx, agent.ID, user.ID, true, 7200, identity.HITLExpirationApprove); err != nil {
		t.Fatal(err)
	}

	// Only change the action; ttl and enabled should stay.
	resp := authedPut(t, server.URL+"/api/v1/agents/"+agent.ID,
		`{"hitl_expiration_action":"reject"}`, apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	got, _ := store.GetAgentByID(ctx, agent.ID)
	if !got.HITLEnabled {
		t.Error("HITLEnabled should remain true")
	}
	if got.HITLTTLSeconds != 7200 {
		t.Errorf("HITLTTLSeconds = %d, want 7200 (unchanged)", got.HITLTTLSeconds)
	}
	if got.HITLExpirationAction != identity.HITLExpirationReject {
		t.Errorf("HITLExpirationAction = %q, want reject", got.HITLExpirationAction)
	}
}

func TestUpdateAgentRejectsEmptyBody(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-v1-empty@example.com", "Owner", "google-v1-empty")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "v1-empty-key", nil)
	store.ClaimOrCreateDomain(ctx, "v1empty.example.com", user.ID)
	store.VerifyDomain(ctx, "v1empty.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@v1empty.example.com", "v1empty.example.com", "", "https://example.com/webhook", "", user.ID)

	resp := authedPut(t, server.URL+"/api/v1/agents/"+agent.ID, `{}`, apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty body should 400, got %d", resp.StatusCode)
	}
}

func TestUpdateAgentRejectsInvalidExpirationAction(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-v1-bad@example.com", "Owner", "google-v1-bad")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "v1-bad-key", nil)
	store.ClaimOrCreateDomain(ctx, "v1bad.example.com", user.ID)
	store.VerifyDomain(ctx, "v1bad.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@v1bad.example.com", "v1bad.example.com", "", "https://example.com/webhook", "", user.ID)

	resp := authedPut(t, server.URL+"/api/v1/agents/"+agent.ID,
		`{"hitl_expiration_action":"maybe"}`, apiKey.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid action should 400, got %d", resp.StatusCode)
	}
}

func TestUpdateAgentCrossUserForbidden(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	userA, _ := store.CreateOrGetUser(ctx, "a-cross-up@example.com", "A", "google-a-cross-up")
	store.ClaimOrCreateDomain(ctx, "acrossup.example.com", userA.ID)
	store.VerifyDomain(ctx, "acrossup.example.com", userA.ID)
	agentA, _ := store.CreateAgent(ctx, "bot@acrossup.example.com", "acrossup.example.com", "", "https://example.com/webhook", "", userA.ID)

	userB, _ := store.CreateOrGetUser(ctx, "b-cross-up@example.com", "B", "google-b-cross-up")
	keyB, _ := store.CreateAPIKey(ctx, userB.ID, "b-cross-up-key", nil)

	resp := authedPut(t, server.URL+"/api/v1/agents/"+agentA.ID,
		`{"hitl_enabled":true}`, keyB.PlaintextKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-user PUT should 403, got %d", resp.StatusCode)
	}
}
