package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/webhook"
)

// sampleAgent is the canonical fixture agent owned by user u_1.
func sampleAgent() identity.AgentIdentity {
	return identity.AgentIdentity{
		ID:                   "support@acme.com",
		Domain:               "acme.com",
		Name:                 "Acme Support",
		DomainVerified:       true,
		UserID:               "u_1",
		CreatedAt:            time.Unix(1700000000, 0).UTC(),
		HITLEnabled:          true,
		HITLTTLSeconds:       604800,
		HITLExpirationAction: "reject",
	}
}

// testServer builds a Server with fake collaborators and a sentinel legacy
// handler, returning an httptest server so tests exercise the real chi+Huma
// stack over the wire (transport layer in scope per the implement skill).
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	deps := Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			switch r.Header.Get("Authorization") {
			case "Bearer good":
				return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
			case "Bearer overcap":
				return &identity.User{ID: "u_overcap", Email: "full@acme.com"}, nil
			default:
				return nil, errors.New("unauthorized")
			}
		},
		ListAgents: func(ctx context.Context, userID string) ([]identity.AgentIdentity, error) {
			if userID != "u_1" {
				return nil, errors.New("unexpected user")
			}
			return []identity.AgentIdentity{sampleAgent()}, nil
		},
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			if address == "support@acme.com" {
				a := sampleAgent()
				return &a, nil
			}
			return nil, errors.New("not found")
		},
		ListMessages: func(ctx context.Context, f identity.MessageListFilter) ([]identity.Message, error) {
			if f.AgentID != "support@acme.com" {
				return nil, errors.New("unexpected agent")
			}
			// Two messages, newest-first; honor Limit + AfterID so the
			// cursor round-trip is exercised end to end.
			all := []identity.Message{
				{ID: "msg_b", Direction: "inbound", Sender: "b@x.com", Recipient: "support@acme.com", Subject: "B", InboxStatus: "unread", CreatedAt: time.Unix(1700000200, 0).UTC()},
				{ID: "msg_a", Direction: "inbound", Sender: "a@x.com", Recipient: "support@acme.com", Subject: "A", InboxStatus: "unread", CreatedAt: time.Unix(1700000100, 0).UTC()},
			}
			start := 0
			if f.AfterID != "" {
				for i, m := range all {
					if m.ID == f.AfterID {
						start = i + 1
						break
					}
				}
			}
			rest := all[start:]
			if f.Limit > 0 && len(rest) > f.Limit {
				rest = rest[:f.Limit]
			}
			return rest, nil
		},
		ListConversations: func(ctx context.Context, f identity.ConversationListFilter) ([]identity.ConversationSummary, error) {
			if f.AgentID != "support@acme.com" {
				return nil, errors.New("unexpected agent")
			}
			return []identity.ConversationSummary{{
				ID: "conv_1", MessageCount: 2, InboundCount: 1, OutboundCount: 1,
				HasUnread: true, LatestSubject: "Help", LatestSender: "alice@example.com",
				LastMessageAt: time.Unix(1700000200, 0).UTC(), FirstMessageAt: time.Unix(1700000100, 0).UTC(),
			}}, nil
		},
		GetConversation: func(ctx context.Context, agentID, convoID string) (*identity.ConversationDetail, error) {
			if agentID == "support@acme.com" && convoID == "conv_1" {
				return &identity.ConversationDetail{
					ConversationSummary: identity.ConversationSummary{
						ID: "conv_1", MessageCount: 1, LatestSubject: "Help",
						LastMessageAt: time.Unix(1700000200, 0).UTC(), FirstMessageAt: time.Unix(1700000200, 0).UTC(),
					},
					Participants: []string{"alice@example.com", "support@acme.com"},
					Labels:       []string{"urgent"},
					Messages:     []identity.Message{{ID: "msg_1", Direction: "inbound", Sender: "alice@example.com", Subject: "Help", InboxStatus: "unread", CreatedAt: time.Unix(1700000200, 0).UTC()}},
				}, nil
			}
			return nil, errors.New("not found")
		},
		GetMessage: func(ctx context.Context, messageID, agentID string) (*identity.Message, error) {
			if agentID == "support@acme.com" && messageID == "msg_1" {
				return &identity.Message{
					ID:             "msg_1",
					Sender:         "alice@example.com",
					ToRecipients:   []string{"support@acme.com"},
					Recipient:      "support@acme.com",
					Subject:        "Help",
					ConversationID: "conv_1",
					DeliveryStatus: "unread",
					CreatedAt:      time.Unix(1700000000, 0).UTC(),
					AuthHeaders:    map[string]string{"spf": "pass"},
					RawMessage:     []byte("raw"),
				}, nil
			}
			return nil, errors.New("not found")
		},
		LookupDomain: func(ctx context.Context, domain, userID string) (*identity.Domain, error) {
			switch domain {
			case "acme.com":
				return &identity.Domain{Domain: domain, Verified: true, VerificationToken: "e2a-verify=tok", IsPrimary: true}, nil
			case "pending.com":
				return &identity.Domain{Domain: domain, Verified: false}, nil
			case "busy.com":
				return &identity.Domain{Domain: domain, Verified: true}, nil
			case "fresh.com":
				// Registered, TXT published, not yet marked verified.
				return &identity.Domain{Domain: domain, Verified: false, VerificationToken: "e2a-verify=fresh"}, nil
			default:
				return nil, errors.New("not registered")
			}
		},
		ListDomains: func(ctx context.Context, userID string) ([]identity.Domain, error) {
			return []identity.Domain{{Domain: "acme.com", Verified: true, VerificationToken: "e2a-verify=tok", IsPrimary: true, AgentCount: 2}}, nil
		},
		ClaimDomain: func(ctx context.Context, domain, userID string) (*identity.Domain, error) {
			return &identity.Domain{Domain: domain, Verified: false, VerificationToken: "e2a-verify=new"}, nil
		},
		EnforceDomainCreate: func(ctx context.Context, userID string) error {
			if userID == "u_overcap" {
				return &limits.LimitExceededError{Resource: "domains", Limit: 1, Current: 1, Limits: limits.Limits{PlanCode: "free"}}
			}
			return nil
		},
		SetDomainPrimary: func(ctx context.Context, domain, userID string) error {
			if domain == "missing.com" {
				return identity.ErrDomainNotFound
			}
			return nil
		},
		DeleteDomain:      func(ctx context.Context, domain, userID string) error { return nil },
		HasAgentsOnDomain: func(ctx context.Context, domain, userID string) (bool, error) { return domain == "busy.com", nil },
		SMTPDomain:        "mx.e2a.dev",
		GetLimits: func(ctx context.Context, userID string) (limits.Limits, error) {
			return limits.Limits{PlanCode: "pro", MaxAgents: 10, MaxDomains: 5, MaxMessagesMonth: 1000, MaxStorageBytes: 1 << 30, UpgradeURL: "https://e2a.dev/upgrade"}, nil
		},
		GetUsage: func(ctx context.Context, userID string) LimitsUsageView {
			return LimitsUsageView{Agents: 2, Domains: 1, MessagesMonth: 42, StorageBytes: 1234}
		},
		ExportUserData: func(ctx context.Context, userID string) (*identity.UserExport, error) {
			return &identity.UserExport{}, nil
		},
		DeleteUserData: func(ctx context.Context, user *identity.User) (*identity.DeleteUserDataResult, error) {
			return &identity.DeleteUserDataResult{}, nil
		},
		ListEvents: func(ctx context.Context, q EventQuery) ([]agent.EventJSON, error) {
			// Two events, honoring Limit + cursor (CursorID) so the
			// cursor round-trip is exercised.
			all := []agent.EventJSON{
				{ID: "evt_b", Type: "email.received", Status: "delivered", CreatedAt: time.Unix(1700000200, 0).UTC()},
				{ID: "evt_a", Type: "email.sent", Status: "delivered", CreatedAt: time.Unix(1700000100, 0).UTC()},
			}
			start := 0
			if q.CursorID != "" {
				for i, e := range all {
					if e.ID == q.CursorID {
						start = i + 1
						break
					}
				}
			}
			rest := all[start:]
			if q.Limit > 0 && len(rest) > q.Limit {
				rest = rest[:q.Limit]
			}
			return rest, nil
		},
		GetEvent2: func(ctx context.Context, userID, eventID string) (*agent.EventJSON, error) {
			switch eventID {
			case "evt_a":
				return &agent.EventJSON{ID: "evt_a", Type: "email.sent", Status: "delivered", CreatedAt: time.Unix(1700000100, 0).UTC()}, nil
			case "evt_expired":
				return nil, agent.ErrEventExpired
			default:
				return nil, agent.ErrEventNotFound
			}
		},
		LoadReplayEvent: func(ctx context.Context, userID, eventID string) (*agent.ReplayEvent, error) {
			switch eventID {
			case "evt_a":
				return &agent.ReplayEvent{EventType: "email.received", MatchedWebhookIDs: []string{"wh_1", "wh_2"}}, nil
			case "evt_expired":
				return nil, agent.ErrEventExpired
			default:
				return nil, agent.ErrEventNotFound
			}
		},
		InsertReplayDelivery: func(ctx context.Context, eventID, webhookID, eventType string, messageID *string, envelope []byte) (string, error) {
			return "whd_" + webhookID, nil
		},
		CreateWebhook: func(ctx context.Context, userID, url, description string, events []string, filters identity.WebhookFilters) (*identity.Webhook, error) {
			if strings.Contains(url, "capped") {
				return nil, identity.ErrWebhookCapReached
			}
			return &identity.Webhook{ID: "wh_1", URL: url, Description: description, Events: events, Filters: filters, SigningSecret: "whsec_xyz", Enabled: true, CreatedAt: time.Unix(1700000000, 0).UTC()}, nil
		},
		ListWebhooks: func(ctx context.Context, userID string) ([]identity.Webhook, error) {
			return []identity.Webhook{{ID: "wh_1", URL: "https://x.com/h", Events: []string{"email.received"}, Enabled: true, SigningSecret: "whsec_should_be_hidden", CreatedAt: time.Unix(1700000000, 0).UTC()}}, nil
		},
		GetWebhook: func(ctx context.Context, webhookID, userID string) (*identity.Webhook, error) {
			if webhookID == "wh_1" || webhookID == "wh_cooldown" {
				return &identity.Webhook{ID: webhookID, URL: "https://x.com/h", Events: []string{"email.received"}, Enabled: true, SigningSecret: "whsec_should_be_hidden", CreatedAt: time.Unix(1700000000, 0).UTC()}, nil
			}
			return nil, identity.ErrWebhookNotFound
		},
		UpdateWebhook: func(ctx context.Context, webhookID, userID string, u identity.WebhookUpdate) (*identity.Webhook, error) {
			switch webhookID {
			case "wh_cooldown":
				return nil, identity.ErrWebhookCooldown
			case "wh_1":
				wh := &identity.Webhook{ID: "wh_1", URL: "https://x.com/h", Events: []string{"email.received"}, Enabled: true, CreatedAt: time.Unix(1700000000, 0).UTC()}
				if u.Description != nil {
					wh.Description = *u.Description
				}
				return wh, nil
			default:
				return nil, identity.ErrWebhookNotFound
			}
		},
		DeleteWebhook: func(ctx context.Context, webhookID, userID string) error {
			if webhookID == "wh_1" {
				return nil
			}
			return identity.ErrWebhookNotFound
		},
		RotateSecret: func(ctx context.Context, webhookID, userID string) (string, time.Time, error) {
			if webhookID == "wh_1" {
				return "whsec_rotated", time.Unix(1700086400, 0).UTC(), nil
			}
			return "", time.Time{}, identity.ErrWebhookNotFound
		},
		TestWebhookInsert: func(ctx context.Context, webhookID, eventType string, envelope []byte) (string, error) {
			return "whd_test_1", nil
		},
		ListDeliveries: func(ctx context.Context, webhookID, status string, limit int) ([]webhook.SubscriberDelivery, error) {
			return []webhook.SubscriberDelivery{
				{ID: "whd_1", EventType: "email.received", Status: "delivered", Attempts: 1, NextRetryAt: time.Unix(1700000000, 0).UTC(), CreatedAt: time.Unix(1700000000, 0).UTC()},
			}, nil
		},
		EnforceMessageSend: func(ctx context.Context, userID string) error {
			if userID == "u_overcap" {
				return &limits.LimitExceededError{Resource: "messages", Limit: 1, Current: 1, Limits: limits.Limits{PlanCode: "free"}}
			}
			return nil
		},
		SendTest: func(ctx context.Context, ag *identity.AgentIdentity) (*agent.OutboundResult, *agent.OutboundError) {
			return &agent.OutboundResult{MessageID: "msg_test_1", Method: "smtp"}, nil
		},
		ApprovePending: func(ctx context.Context, userID, messageID, expectedAgentEmail string, ovr agent.ApproveOverrides) (*identity.Message, *agent.OutboundError) {
			switch messageID {
			case "msg_pending":
				return &identity.Message{ID: "msg_pending", Status: "sent", ProviderMessageID: "<prov@ses>", Method: "smtp"}, nil
			case "msg_notpending":
				return nil, &agent.OutboundError{Status: http.StatusConflict, Code: "message_not_pending", Msg: "message is not pending approval"}
			default:
				return nil, &agent.OutboundError{Status: http.StatusNotFound, Code: "not_found", Msg: "message not found"}
			}
		},
		RejectPending: func(ctx context.Context, userID, messageID, expectedAgentEmail, reason string) (*identity.Message, *agent.OutboundError) {
			if messageID == "msg_pending" {
				return &identity.Message{ID: "msg_pending", Status: "rejected", RejectionReason: reason}, nil
			}
			return nil, &agent.OutboundError{Status: http.StatusNotFound, Code: "not_found", Msg: "message not found"}
		},
		GetInboundMessage: func(ctx context.Context, messageID string) (*identity.Message, error) {
			if messageID == "msg_in1" {
				return &identity.Message{
					ID: "msg_in1", AgentID: "support@acme.com", Sender: "alice@x.com",
					Subject: "Question", EmailMessageID: "<abc@x.com>",
					RawMessage: []byte("From: alice@x.com\r\nTo: support@acme.com\r\nSubject: Question\r\nMessage-ID: <abc@x.com>\r\n\r\nhi"),
				}, nil
			}
			return nil, errors.New("not found")
		},
		DeliverOutbound: func(ctx context.Context, user *identity.User, ag *identity.AgentIdentity, req outbound.SendRequest, msgType, replyTo string, referenced *identity.Message) (*agent.OutboundResult, *agent.OutboundError) {
			switch {
			case strings.Contains(req.Subject, "HOLD"):
				exp := time.Unix(1700090000, 0).UTC()
				return &agent.OutboundResult{Held: true, PendingMessageID: "msg_pending_1", ApprovalExpiresAt: &exp}, nil
			case strings.Contains(req.Subject, "FAIL"):
				return nil, &agent.OutboundError{Status: http.StatusInternalServerError, Code: "internal_error", Msg: "send failed"}
			default:
				return &agent.OutboundResult{MessageID: "msg_sent_1", Method: "smtp"}, nil
			}
		},
		TouchDomainChecked: func(ctx context.Context, domain, userID string) error { return nil },
		VerifyDomain:       func(ctx context.Context, domain, userID string) error { return nil },
		VerifyProbe: func(domain, token, dkimSel, dkimKey string) DomainCheckResult {
			// "pending.com" has not published its TXT yet; everything else has.
			if domain == "pending.com" {
				return DomainCheckResult{TXTFound: false, MX: "missing", SPF: "missing", DKIM: "missing"}
			}
			return DomainCheckResult{TXTFound: true, MX: "found", SPF: "found", DKIM: "found"}
		},
		CreateAgent: func(ctx context.Context, email, domain, name, webhookURL, agentMode, userID string) (*identity.AgentIdentity, error) {
			if email == "dupe@acme.com" {
				return nil, errors.New("duplicate key value")
			}
			return &identity.AgentIdentity{ID: email, Domain: domain, Email: email, Name: name, UserID: userID}, nil
		},
		EnforceAgentCreate: func(ctx context.Context, userID string) error {
			if userID == "u_overcap" {
				return &limits.LimitExceededError{Resource: "agents", Limit: 1, Current: 1, Limits: limits.Limits{PlanCode: "free", UpgradeURL: "https://e2a.dev/upgrade"}}
			}
			return nil
		},
		UpdateAgentHITL: func(ctx context.Context, agentID, userID string, enabled bool, ttl int, action string) error {
			return nil
		},
		DeleteAgent: func(ctx context.Context, agentID, userID string) error {
			if userID != "u_1" {
				return errors.New("unexpected user")
			}
			return nil
		},
		SharedDomain: "agents.e2a.dev",
		PublicURL:    "https://api.e2a.dev",
		Legacy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("legacy:" + r.URL.Path))
		}),
	}
	srv := httptest.NewServer(New(deps))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetInfo(t *testing.T) {
	srv := testServer(t)
	resp, err := http.Get(srv.URL + "/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("missing X-Request-Id header")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff header")
	}
	var body DeploymentInfoView
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.SharedDomain != "agents.e2a.dev" || !body.SlugRegistrationEnabled || body.PublicURL != "https://api.e2a.dev" {
		t.Fatalf("unexpected info: %+v", body)
	}
}

func TestListAgentsAuthorized(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body=%s", resp.StatusCode, b)
	}
	var body struct {
		Agents []AgentView `json:"agents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Agents) != 1 {
		t.Fatalf("want 1 agent, got %d", len(body.Agents))
	}
	a := body.Agents[0]
	if a.Email != "support@acme.com" || a.Domain != "acme.com" || !a.DomainVerified {
		t.Fatalf("unexpected agent view: %+v", a)
	}
}

func TestGetAgentOwned(t *testing.T) {
	srv := testServer(t)
	// The address is URL-encoded in the path (@ -> %40); the real chi+Huma
	// stack must decode it before lookup.
	req, _ := http.NewRequest("GET", srv.URL+"/v1/agents/support%40acme.com", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body=%s", resp.StatusCode, b)
	}
	var a AgentView
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		t.Fatal(err)
	}
	if a.Email != "support@acme.com" || a.Name != "Acme Support" {
		t.Fatalf("unexpected agent: %+v", a)
	}
}

func TestGetAgentForbiddenWhenUnknown(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/agents/other%40acme.com", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// Mirrors legacy: unknown/non-owned agent -> 403, not 404.
	if resp.StatusCode != 403 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Error.Code != "forbidden" {
		t.Fatalf("want code forbidden, got %q", env.Error.Code)
	}
}

func TestGetMessageOwned(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/agents/support%40acme.com/messages/msg_1", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body=%s", resp.StatusCode, b)
	}
	// Decode into a map to assert the legacy keys are all present
	// (including unconditional cc/reply_to/auth_headers/raw_message).
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"message_id", "from", "to", "cc", "reply_to", "recipient", "subject", "conversation_id", "status", "labels", "created_at", "auth_headers", "raw_message"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in message view", k)
		}
	}
	if m["message_id"] != "msg_1" || m["status"] != "unread" {
		t.Fatalf("unexpected message: %+v", m)
	}
	// raw_message is []byte -> base64 string ("raw" -> "cmF3").
	if m["raw_message"] != "cmF3" {
		t.Fatalf("raw_message not base64-encoded: %v", m["raw_message"])
	}
}

func TestGetMessageNotFound(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/agents/support%40acme.com/messages/msg_missing", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestListAgentsUnauthorizedEnvelope(t *testing.T) {
	srv := testServer(t)
	resp, err := http.Get(srv.URL + "/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	headerID := resp.Header.Get("X-Request-Id")
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "unauthorized" {
		t.Fatalf("want code unauthorized, got %q", env.Error.Code)
	}
	if env.Error.RequestID == "" || env.Error.RequestID != headerID {
		t.Fatalf("request_id body=%q header=%q must match and be non-empty", env.Error.RequestID, headerID)
	}
}

func TestRequestIDPropagation(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/info", nil)
	req.Header.Set("X-Request-Id", "req_caller_supplied")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Request-Id"); got != "req_caller_supplied" {
		t.Fatalf("request id not propagated: %q", got)
	}
}

func TestLegacyFallback(t *testing.T) {
	srv := testServer(t)
	// A route the v1 layer does not own must fall through to the legacy
	// handler unchanged (strangler) — and still carry the new request id.
	resp, err := http.Get(srv.URL + "/api/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("expected legacy 418, got %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "legacy:/api/v1/agents" {
		t.Fatalf("unexpected legacy body: %s", b)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("legacy fallback should still carry X-Request-Id")
	}
}
