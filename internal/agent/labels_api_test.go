package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// setupLabelsAgent provisions a verified domain + agent + an inbound
// message ready for label mutations. Returns the server, store, api
// key, agent email, and a single message id.
type labelsFixture struct {
	server    *http.Client
	serverURL string
	apiKey    string
	agentEmail string
	msgID     string
}

func setupLabelsFixture(t *testing.T, prefix string) labelsFixture {
	t.Helper()
	server, store, _ := setupAPI(t)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner-"+prefix+"@example.com", "Owner", "google-"+prefix)
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, prefix+"-key", nil)
	domain := prefix + ".example.com"
	store.ClaimOrCreateDomain(ctx, domain, user.ID)
	store.VerifyDomain(ctx, domain, user.ID)
	agentEmail := "bot@" + domain
	agent, _ := store.CreateAgent(ctx, agentEmail, domain, "", "https://example.com/webhook", "", user.ID)
	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@gmail.com", agentEmail, "<orig-"+prefix+"@gmail.com>", "Hi", "", "", nil, nil, nil, nil, nil)
	return labelsFixture{
		server:     http.DefaultClient,
		serverURL:  server.URL,
		apiKey:     apiKey.PlaintextKey,
		agentEmail: agentEmail,
		msgID:      msg.ID,
	}
}

func patchLabels(t *testing.T, f labelsFixture, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("PATCH", f.serverURL+"/api/v1/agents/"+f.agentEmail+"/messages/"+f.msgID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.apiKey)
	resp, err := f.server.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	return resp
}

func TestUpdateMessageLabels_AddRemoveHappyPath(t *testing.T) {
	f := setupLabelsFixture(t, "lbl-happy")

	resp := patchLabels(t, f, `{"add_labels":["Urgent","Follow-Up"],"remove_labels":["unread"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		MessageID string   `json:"message_id"`
		Labels    []string `json:"labels"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.MessageID != f.msgID {
		t.Errorf("message_id = %q, want %q", body.MessageID, f.msgID)
	}
	// Labels are lowercased server-side — "Urgent" → "urgent".
	want := []string{"follow-up", "urgent"}
	sort.Strings(body.Labels)
	if len(body.Labels) != 2 || body.Labels[0] != want[0] || body.Labels[1] != want[1] {
		t.Errorf("labels = %v, want %v (lowercased + sorted)", body.Labels, want)
	}
}

func TestUpdateMessageLabels_RejectsSystemPrefix(t *testing.T) {
	f := setupLabelsFixture(t, "lbl-sys")
	resp := patchLabels(t, f, `{"add_labels":["e2a:auto-tagged"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (system prefix is server-only)", resp.StatusCode)
	}
}

func TestUpdateMessageLabels_RejectsInvalidCharset(t *testing.T) {
	f := setupLabelsFixture(t, "lbl-charset")
	cases := []struct {
		name  string
		body  string
	}{
		{"space", `{"add_labels":["hello world"]}`},
		{"slash", `{"add_labels":["foo/bar"]}`},
		{"unicode", `{"add_labels":["café"]}`},
		{"empty", `{"add_labels":[""]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := patchLabels(t, f, c.body)
			defer resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Errorf("status = %d, want 400 for %s", resp.StatusCode, c.name)
			}
		})
	}
}

func TestUpdateMessageLabels_RejectsOverLengthLabel(t *testing.T) {
	f := setupLabelsFixture(t, "lbl-toolong")
	longLabel := ""
	for i := 0; i < 65; i++ {
		longLabel += "a"
	}
	resp := patchLabels(t, f, `{"add_labels":["`+longLabel+`"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (label >64 chars)", resp.StatusCode)
	}
}

func TestUpdateMessageLabels_RejectsOverPerOpCap(t *testing.T) {
	f := setupLabelsFixture(t, "lbl-opcap")
	labels := make([]string, 51) // > MaxLabelsPerOp = 50
	for i := range labels {
		labels[i] = "label-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
	}
	body, _ := json.Marshal(map[string][]string{"add_labels": labels})
	resp := patchLabels(t, f, string(body))
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (over per-op cap)", resp.StatusCode)
	}
}

func TestUpdateMessageLabels_MessageNotFound(t *testing.T) {
	f := setupLabelsFixture(t, "lbl-404")
	req, _ := http.NewRequest("PATCH", f.serverURL+"/api/v1/agents/"+f.agentEmail+"/messages/msg_does_not_exist", bytes.NewBufferString(`{"add_labels":["x"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.apiKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestUpdateMessageLabels_CrossAgentReturns404(t *testing.T) {
	// Agent A creates a message; Agent B (different user) must NOT be
	// able to mutate it. Cross-agent access looks like not-found to
	// avoid leaking the existence of A's message via 403 vs 404.
	server, store, _ := setupAPI(t)
	ctx := context.Background()

	userA, _ := store.CreateOrGetUser(ctx, "owner-lblxA@example.com", "OwnerA", "google-lblxA")
	store.ClaimOrCreateDomain(ctx, "lblxa.example.com", userA.ID)
	store.VerifyDomain(ctx, "lblxa.example.com", userA.ID)
	agentA, _ := store.CreateAgent(ctx, "bot@lblxa.example.com", "lblxa.example.com", "", "https://example.com/webhook", "", userA.ID)
	msgA, _ := store.CreateInboundMessage(ctx, "", agentA.ID, "alice@gmail.com", "bot@lblxa.example.com", "<x@gmail.com>", "Hi", "", "", nil, nil, nil, nil, nil)

	userB, _ := store.CreateOrGetUser(ctx, "owner-lblxB@example.com", "OwnerB", "google-lblxB")
	apiKeyB, _ := store.CreateAPIKey(ctx, userB.ID, "lblxB-key", nil)
	store.ClaimOrCreateDomain(ctx, "lblxb.example.com", userB.ID)
	store.VerifyDomain(ctx, "lblxb.example.com", userB.ID)
	store.CreateAgent(ctx, "bot@lblxb.example.com", "lblxb.example.com", "", "https://example.com/webhook", "", userB.ID)

	req, _ := http.NewRequest("PATCH", server.URL+"/api/v1/agents/bot@lblxb.example.com/messages/"+msgA.ID, bytes.NewBufferString(`{"add_labels":["x"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyB.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404 (cross-agent must look like not-found)", resp.StatusCode)
	}
}

func TestUpdateMessageLabels_Unauthorized(t *testing.T) {
	server, _, _ := setupAPI(t)
	req, _ := http.NewRequest("PATCH", server.URL+"/api/v1/agents/bot@example.com/messages/msg_any", bytes.NewBufferString(`{"add_labels":["x"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestUpdateMessageLabels_OutboundCanBeLabeled(t *testing.T) {
	// The labels column lives on every messages row, not just inbound.
	// Confirm an agent can categorize their sent mail too (e.g.
	// "billing", "follow-up-sent"). Cross-direction by design — the
	// handler doesn't gate direction.
	server, store, _ := setupAPI(t)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner-lbloutb@example.com", "Owner", "google-lbloutb")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "lbloutb-key", nil)
	store.ClaimOrCreateDomain(ctx, "lbloutb.example.com", user.ID)
	store.VerifyDomain(ctx, "lbloutb.example.com", user.ID)
	agentEmail := "bot@lbloutb.example.com"
	agent, _ := store.CreateAgent(ctx, agentEmail, "lbloutb.example.com", "", "https://example.com/webhook", "", user.ID)

	outMsg, err := store.CreateOutboundMessage(ctx, agent.ID, []string{"alice@example.com"}, nil, nil, "Hello Alice", "send", "smtp", "<provider-id>", "")
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}

	req, _ := http.NewRequest("PATCH", server.URL+"/api/v1/agents/"+agentEmail+"/messages/"+outMsg.ID, bytes.NewBufferString(`{"add_labels":["billing","follow-up-sent"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (outbound messages must be labelable)", resp.StatusCode)
	}
	var body struct {
		Labels []string `json:"labels"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	sort.Strings(body.Labels)
	want := []string{"billing", "follow-up-sent"}
	if len(body.Labels) != 2 || body.Labels[0] != want[0] || body.Labels[1] != want[1] {
		t.Errorf("labels = %v, want %v", body.Labels, want)
	}
}

func TestUpdateMessageLabels_HITLPendingCanBeLabeled(t *testing.T) {
	// A reviewer should be able to tag a pending HITL message before
	// approving (e.g. "needs-legal-review"). Confirm the labels column
	// works on rows whose direction='outbound' AND status='pending_approval'.
	server, store, _ := setupAPI(t)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner-lblhitl@example.com", "Owner", "google-lblhitl")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "lblhitl-key", nil)
	store.ClaimOrCreateDomain(ctx, "lblhitl.example.com", user.ID)
	store.VerifyDomain(ctx, "lblhitl.example.com", user.ID)
	agentEmail := "bot@lblhitl.example.com"
	agent, _ := store.CreateAgent(ctx, agentEmail, "lblhitl.example.com", "", "https://example.com/webhook", "", user.ID)

	pending, err := store.CreatePendingOutboundMessage(ctx, agent.ID, []string{"alice@example.com"}, nil, nil, "Pending review", "plain body", "", nil, "send", "", "", 604800)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}

	req, _ := http.NewRequest("PATCH", server.URL+"/api/v1/agents/"+agentEmail+"/messages/"+pending.ID, bytes.NewBufferString(`{"add_labels":["needs-legal-review"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Labels []string `json:"labels"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Labels) != 1 || body.Labels[0] != "needs-legal-review" {
		t.Errorf("labels = %v, want [needs-legal-review]", body.Labels)
	}
}

func TestUpdateMessageLabels_DedupsWithinAddList(t *testing.T) {
	// Caller passes duplicates within a single add_labels list — the
	// handler must collapse to one. Defensive: an LLM-generated tag
	// stream may emit the same tag twice and we don't want that to
	// cost a slot of the per-op cap or show up duplicated in the
	// stored array.
	f := setupLabelsFixture(t, "lbl-dedup")
	resp := patchLabels(t, f, `{"add_labels":["urgent","urgent","urgent"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Labels []string `json:"labels"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Labels) != 1 || body.Labels[0] != "urgent" {
		t.Errorf("labels = %v, want exactly [urgent] (duplicates collapsed)", body.Labels)
	}
}

func TestUpdateMessageLabels_EmptyDeltaReturnsCurrentLabels(t *testing.T) {
	// Documented behavior: a PATCH with no add/remove (or empty arrays)
	// is a no-op that echoes the current label set. Useful as a
	// cheap "fetch labels only" side channel, and a sentinel test
	// against accidentally changing this to a 400 in the future.
	f := setupLabelsFixture(t, "lbl-empty")
	// Seed one label so the no-op response is non-trivially correct.
	setup := patchLabels(t, f, `{"add_labels":["urgent"]}`)
	setup.Body.Close()

	resp := patchLabels(t, f, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Labels []string `json:"labels"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Labels) != 1 || body.Labels[0] != "urgent" {
		t.Errorf("labels = %v, want [urgent] (no-op must preserve state)", body.Labels)
	}
}

// Silence the linter — identity is imported to give error types stable
// names for the test assertions even though they're only consumed by
// internal/agent storage calls.
var _ = identity.ErrMessageNotFound
