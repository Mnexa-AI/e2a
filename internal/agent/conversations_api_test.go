package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// convoFixture provisions a verified domain + agent and returns the
// running test server, the API key, and the agent email.
type convoFixture struct {
	serverURL  string
	apiKey     string
	agentEmail string
}

func setupConvoFixture(t *testing.T, prefix string) convoFixture {
	t.Helper()
	server, store, _ := setupAPI(t)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner-"+prefix+"@example.com", "Owner", "google-"+prefix)
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, prefix+"-key", nil)
	domain := prefix + ".example.com"
	store.ClaimOrCreateDomain(ctx, domain, user.ID)
	store.VerifyDomain(ctx, domain, user.ID)
	agentEmail := "bot@" + domain
	store.CreateAgent(ctx, agentEmail, domain, "", "https://example.com/webhook", "", user.ID)
	return convoFixture{
		serverURL:  server.URL,
		apiKey:     apiKey.PlaintextKey,
		agentEmail: agentEmail,
	}
}

func (f convoFixture) get(t *testing.T, path string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.serverURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+f.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	resp.Body.Close()
	return resp, buf.Bytes()
}

func TestListConversations_Unauthorized(t *testing.T) {
	server, _, _ := setupAPI(t)
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/any@example.com/conversations", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestListConversations_AgentNotFound(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner-convo404@example.com", "Owner", "google-convo404")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "convo404-key", nil)
	// No agent for this user

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/missing@example.com/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestListConversations_GroupsAndReturnsSummaryShape(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner-convo-shape@example.com", "Owner", "google-convo-shape")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "convo-shape-key", nil)
	store.ClaimOrCreateDomain(ctx, "convo-shape.example.com", user.ID)
	store.VerifyDomain(ctx, "convo-shape.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@convo-shape.example.com", "convo-shape.example.com", "", "https://example.com/webhook", "", user.ID)

	// Two conversations: A (2 inbound, both unread) and B (1 inbound).
	store.CreateInboundMessage(ctx, "", agent.ID, "alice@x.com", "bot@convo-shape.example.com", "<a1@x>", "Subj A", "conv-shape-A", "unread", nil, nil, nil, nil, nil)
	store.CreateInboundMessage(ctx, "", agent.ID, "alice@x.com", "bot@convo-shape.example.com", "<a2@x>", "Subj A reply", "conv-shape-A", "unread", nil, nil, nil, nil, nil)
	store.CreateInboundMessage(ctx, "", agent.ID, "bob@x.com", "bot@convo-shape.example.com", "<b1@x>", "Subj B", "conv-shape-B", "unread", nil, nil, nil, nil, nil)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/bot@convo-shape.example.com/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Conversations []struct {
			ConversationID string `json:"conversation_id"`
			MessageCount   int    `json:"message_count"`
			InboundCount   int    `json:"inbound_count"`
			OutboundCount  int    `json:"outbound_count"`
			HasUnread      bool   `json:"has_unread"`
			LatestSubject  string `json:"latest_subject"`
			LatestSender   string `json:"latest_sender"`
			LastMessageAt  string `json:"last_message_at"`
		} `json:"conversations"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Conversations) != 2 {
		t.Fatalf("len(conversations) = %d, want 2", len(body.Conversations))
	}
	byID := map[string]int{}
	for _, c := range body.Conversations {
		byID[c.ConversationID] = c.MessageCount
		if c.LastMessageAt == "" {
			t.Errorf("%s: last_message_at is empty", c.ConversationID)
		}
		if !c.HasUnread {
			t.Errorf("%s: has_unread = false, want true", c.ConversationID)
		}
	}
	if byID["conv-shape-A"] != 2 {
		t.Errorf("conv-shape-A count = %d, want 2", byID["conv-shape-A"])
	}
	if byID["conv-shape-B"] != 1 {
		t.Errorf("conv-shape-B count = %d, want 1", byID["conv-shape-B"])
	}
}

func TestListConversations_InvalidSinceUntil(t *testing.T) {
	f := setupConvoFixture(t, "convo-invalid")
	cases := []struct {
		name string
		qs   string
	}{
		{"since-bad", "since=not-a-date"},
		{"until-bad", "until=23:00"},
		{"since-after-until", "since=2026-12-01T00:00:00Z&until=2026-01-01T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, _ := f.get(t, "/api/v1/agents/"+f.agentEmail+"/conversations?"+c.qs)
			if resp.StatusCode != 400 {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestListConversations_ExcludesEmptyConversationID(t *testing.T) {
	// Regression: messages with empty conversation_id are NOT
	// "the empty conversation" — they're standalone and must not
	// appear in any list row.
	server, store, _ := setupAPI(t)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner-convo-empty@example.com", "Owner", "google-convo-empty")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "convo-empty-key", nil)
	store.ClaimOrCreateDomain(ctx, "convo-empty.example.com", user.ID)
	store.VerifyDomain(ctx, "convo-empty.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@convo-empty.example.com", "convo-empty.example.com", "", "https://example.com/webhook", "", user.ID)

	store.CreateInboundMessage(ctx, "", agent.ID, "x@x.com", "bot@convo-empty.example.com", "<m1@x>", "no-conv", "", "unread", nil, nil, nil, nil, nil)
	store.CreateInboundMessage(ctx, "", agent.ID, "x@x.com", "bot@convo-empty.example.com", "<m2@x>", "with-conv", "conv-some", "unread", nil, nil, nil, nil, nil)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/bot@convo-empty.example.com/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	var body struct {
		Conversations []struct {
			ConversationID string `json:"conversation_id"`
		} `json:"conversations"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Conversations) != 1 {
		t.Fatalf("len = %d, want 1 (only the message with conv-some)", len(body.Conversations))
	}
	if body.Conversations[0].ConversationID != "conv-some" {
		t.Errorf("conversation_id = %q, want conv-some", body.Conversations[0].ConversationID)
	}
}

func TestGetConversation_Unauthorized(t *testing.T) {
	server, _, _ := setupAPI(t)
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/any@example.com/conversations/conv_x", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestGetConversation_NotFound(t *testing.T) {
	f := setupConvoFixture(t, "convo-detail-404")
	resp, _ := f.get(t, "/api/v1/agents/"+f.agentEmail+"/conversations/conv_does_not_exist")
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetConversation_DetailShape(t *testing.T) {
	server, store, _ := setupAPI(t)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner-convo-detail@example.com", "Owner", "google-convo-detail")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "convo-detail-key", nil)
	store.ClaimOrCreateDomain(ctx, "convo-detail.example.com", user.ID)
	store.VerifyDomain(ctx, "convo-detail.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@convo-detail.example.com", "convo-detail.example.com", "", "https://example.com/webhook", "", user.ID)

	m1, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@x.com", "bot@convo-detail.example.com", "<m1@x>", "first", "conv-d", "unread", nil, nil, []string{"bot@convo-detail.example.com"}, nil, nil)
	m2, _ := store.CreateInboundMessage(ctx, "", agent.ID, "bob@x.com", "bot@convo-detail.example.com", "<m2@x>", "second", "conv-d", "unread", nil, nil, []string{"bot@convo-detail.example.com"}, []string{"carol@x.com"}, nil)
	store.ModifyMessageLabels(ctx, m1.ID, agent.ID, []string{"urgent"}, nil)
	store.ModifyMessageLabels(ctx, m2.ID, agent.ID, []string{"follow-up"}, nil)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/bot@convo-detail.example.com/conversations/"+url.PathEscape("conv-d"), nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var detail struct {
		ConversationID string   `json:"conversation_id"`
		MessageCount   int      `json:"message_count"`
		Participants   []string `json:"participants"`
		Labels         []string `json:"labels"`
		Messages       []struct {
			MessageID string   `json:"message_id"`
			Subject   string   `json:"subject"`
			Labels    []string `json:"labels"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.ConversationID != "conv-d" {
		t.Errorf("conversation_id = %q", detail.ConversationID)
	}
	if detail.MessageCount != 2 {
		t.Errorf("message_count = %d, want 2", detail.MessageCount)
	}
	// Labels = union of member labels — sorted
	if fmt.Sprintf("%v", detail.Labels) != "[follow-up urgent]" {
		t.Errorf("labels = %v, want [follow-up urgent]", detail.Labels)
	}
	// Participants must include senders + recipient + to + cc
	found := map[string]bool{}
	for _, p := range detail.Participants {
		found[p] = true
	}
	for _, want := range []string{"alice@x.com", "bob@x.com", "bot@convo-detail.example.com", "carol@x.com"} {
		if !found[want] {
			t.Errorf("participants missing %q: %v", want, detail.Participants)
		}
	}
	// Messages chronological — first then second
	if len(detail.Messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(detail.Messages))
	}
	if detail.Messages[0].Subject != "first" || detail.Messages[1].Subject != "second" {
		t.Errorf("messages out of order: %v / %v", detail.Messages[0].Subject, detail.Messages[1].Subject)
	}
	// Regression: each message row must have labels field (never null)
	for i, m := range detail.Messages {
		if m.Labels == nil {
			t.Errorf("messages[%d].labels = nil, want array", i)
		}
	}
}

func TestGetConversation_RejectsOversizedID(t *testing.T) {
	// Defensive cap: a 300-char path param should 400 before reaching
	// the DB. Matches the same maxFilterStr cap the list endpoint
	// applies to the conversation_id query param.
	f := setupConvoFixture(t, "convo-oversize")
	longID := strings.Repeat("a", 250)
	resp, _ := f.get(t, "/api/v1/agents/"+f.agentEmail+"/conversations/"+longID)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (oversized conversation_id)", resp.StatusCode)
	}
}

func TestGetConversation_RejectsCRLFInID(t *testing.T) {
	// validateConversationID rejects CR/LF; the path param is no
	// different from the query param for this guard.
	f := setupConvoFixture(t, "convo-crlf")
	resp, _ := f.get(t, "/api/v1/agents/"+f.agentEmail+"/conversations/conv%0D%0Ainjected")
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (CRLF in conversation_id)", resp.StatusCode)
	}
}

func TestGetConversation_MessageRowHasAllSummaryFields(t *testing.T) {
	// Contract regression: each message row in the conversation
	// detail's `messages[]` must carry the same set of summary fields
	// as `GET /messages`. Previously the detail handler hand-rolled
	// a map[string]interface{} that omitted hitl_status, webhook_status,
	// webhook_error, size_bytes, and could silently drift from
	// MessageSummary on every list-endpoint addition. Now both paths
	// share messageSummaryFromIdentity — this test prevents future
	// drift by asserting that the detail-message JSON keys are a
	// superset of the list-endpoint keys for the same row.
	server, store, _ := setupAPI(t)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner-convo-shape@example.com", "Owner", "google-convo-shape-2")
	apiKey, _ := store.CreateAPIKey(ctx, user.ID, "convo-shape-2-key", nil)
	store.ClaimOrCreateDomain(ctx, "convo-shape-2.example.com", user.ID)
	store.VerifyDomain(ctx, "convo-shape-2.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@convo-shape-2.example.com", "convo-shape-2.example.com", "", "https://example.com/webhook", "", user.ID)

	msg, _ := store.CreateInboundMessage(ctx, "", agent.ID, "alice@x.com", "bot@convo-shape-2.example.com", "<m1@x>", "hi", "conv-shape-2", "unread", nil, nil, nil, nil, nil)
	store.ModifyMessageLabels(ctx, msg.ID, agent.ID, []string{"urgent"}, nil)

	// Fetch the row via the messages list.
	reqList, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/bot@convo-shape-2.example.com/messages?status=all&direction=all", nil)
	reqList.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	respList, _ := http.DefaultClient.Do(reqList)
	defer respList.Body.Close()
	var listBody struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	json.NewDecoder(respList.Body).Decode(&listBody)
	if len(listBody.Messages) != 1 {
		t.Fatalf("list len = %d, want 1", len(listBody.Messages))
	}
	listKeys := map[string]bool{}
	for k := range listBody.Messages[0] {
		listKeys[k] = true
	}

	// Fetch the same row via the conversation detail.
	reqDetail, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/bot@convo-shape-2.example.com/conversations/conv-shape-2", nil)
	reqDetail.Header.Set("Authorization", "Bearer "+apiKey.PlaintextKey)
	respDetail, _ := http.DefaultClient.Do(reqDetail)
	defer respDetail.Body.Close()
	var detailBody struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	json.NewDecoder(respDetail.Body).Decode(&detailBody)
	if len(detailBody.Messages) != 1 {
		t.Fatalf("detail messages len = %d, want 1", len(detailBody.Messages))
	}
	detailKeys := map[string]bool{}
	for k := range detailBody.Messages[0] {
		detailKeys[k] = true
	}

	// Every key the list endpoint emits must also appear on the
	// detail's member row. The reverse isn't required (the detail
	// could emit MORE), so we only assert the superset direction.
	for k := range listKeys {
		if !detailKeys[k] {
			t.Errorf("conversation detail message missing key %q (drift from MessageSummary contract)", k)
		}
	}

	// Field-level spot checks: the keys we'd most want to know about
	// if they ever disappeared from the conversation row.
	for _, must := range []string{"message_id", "direction", "from", "subject", "labels", "created_at", "status"} {
		if _, ok := detailBody.Messages[0][must]; !ok {
			t.Errorf("conversation detail message missing critical key %q", must)
		}
	}
}

func TestGetConversation_CrossAgentReturns404(t *testing.T) {
	// Agent B (different user) must NOT be able to read agent A's
	// conversation, even with the right conversation_id. Same
	// not-found masking as single-message reads.
	server, store, _ := setupAPI(t)
	ctx := context.Background()
	userA, _ := store.CreateOrGetUser(ctx, "owner-convoxA@example.com", "OwnerA", "google-convoxA")
	store.ClaimOrCreateDomain(ctx, "convoxa.example.com", userA.ID)
	store.VerifyDomain(ctx, "convoxa.example.com", userA.ID)
	agentA, _ := store.CreateAgent(ctx, "bot@convoxa.example.com", "convoxa.example.com", "", "https://example.com/webhook", "", userA.ID)
	store.CreateInboundMessage(ctx, "", agentA.ID, "x@gmail.com", "bot@convoxa.example.com", "<x@x>", "secret", "conv-shared", "unread", nil, nil, nil, nil, nil)

	userB, _ := store.CreateOrGetUser(ctx, "owner-convoxB@example.com", "OwnerB", "google-convoxB")
	apiKeyB, _ := store.CreateAPIKey(ctx, userB.ID, "convoxB-key", nil)
	store.ClaimOrCreateDomain(ctx, "convoxb.example.com", userB.ID)
	store.VerifyDomain(ctx, "convoxb.example.com", userB.ID)
	store.CreateAgent(ctx, "bot@convoxb.example.com", "convoxb.example.com", "", "https://example.com/webhook", "", userB.ID)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/agents/bot@convoxb.example.com/conversations/conv-shared", nil)
	req.Header.Set("Authorization", "Bearer "+apiKeyB.PlaintextKey)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404 (cross-agent must look like not-found)", resp.StatusCode)
	}
}
