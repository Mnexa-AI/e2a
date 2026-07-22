package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/messagelifecycle"
)

const lifecycleTestSecret = "message-lifecycle-test-secret"

func lifecycleTransitions(count int) []messagelifecycle.MessageLifecycleTransition {
	base := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	items := make([]messagelifecycle.MessageLifecycleTransition, count)
	for i := range items {
		occurredAt := base.Add(time.Duration(i/2) * time.Second)
		items[i] = messagelifecycle.MessageLifecycleTransition{
			ID: fmt.Sprintf("mlt_%03d", i), MessageID: "msg_one", Direction: "outbound",
			Stage: messagelifecycle.StageAccepted, Outcome: messagelifecycle.OutcomeAccepted,
			ReasonCode: messagelifecycle.ReasonAcceptanceOutboundAPI,
			Evidence:   map[string]any{}, CorrelationIDs: map[string]string{}, OccurredAt: occurredAt,
		}
	}
	return items
}

func newLifecycleServer(t *testing.T, items []messagelifecycle.MessageLifecycleTransition, mutate ...func(*Deps)) (*Server, *[]string) {
	t.Helper()
	calls := []string{}
	deps := Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") != "Bearer good" {
				return nil, errors.New("unauthorized")
			}
			return &identity.User{ID: "u_1", Email: "owner@example.com"}, nil
		},
		GetAgent: func(_ context.Context, address string) (*identity.AgentIdentity, error) {
			if address == "agent@example.com" || address == "other@example.com" {
				return &identity.AgentIdentity{ID: address, UserID: "u_1"}, nil
			}
			if address == "foreign@example.com" {
				return &identity.AgentIdentity{ID: address, UserID: "u_2"}, nil
			}
			return nil, errors.New("not found")
		},
		ListMessageLifecycle: func(_ context.Context, messageID, agentID string) ([]messagelifecycle.MessageLifecycleTransition, error) {
			calls = append(calls, messageID+"|"+agentID)
			if messageID == "missing" {
				return nil, messagelifecycle.ErrMessageNotFound
			}
			out := make([]messagelifecycle.MessageLifecycleTransition, len(items))
			copy(out, items)
			for i := range out {
				out[i].MessageID = messageID
			}
			return out, nil
		},
		CursorSecret: lifecycleTestSecret,
	}
	for _, fn := range mutate {
		fn(&deps)
	}
	s := New(deps)
	return s, &calls
}

func lifecycleGET(t *testing.T, handler http.Handler, agentID, messageID, query string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "/v1/agents/"+url.PathEscape(agentID)+"/messages/"+url.PathEscape(messageID)+"/lifecycle"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer good")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return resp.Code, body
}

func lifecycleItems(t *testing.T, body map[string]any) []any {
	t.Helper()
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items = %#v, want array", body["items"])
	}
	return items
}

func TestMessageLifecyclePaginationIsStableAtEqualTimestamps(t *testing.T) {
	items := lifecycleTransitions(5)
	items[0], items[1] = items[1], items[0]
	srv, calls := newLifecycleServer(t, items)
	status, first := lifecycleGET(t, srv, "agent@example.com", "msg_one", "?limit=1")
	if status != http.StatusOK || lifecycleItems(t, first)[0].(map[string]any)["id"] != "mlt_000" {
		t.Fatalf("page 1 = %d %#v", status, first)
	}
	cursor := first["next_cursor"].(string)
	status, second := lifecycleGET(t, srv, "agent@example.com", "msg_one", "?limit=1&cursor="+url.QueryEscape(cursor))
	if status != http.StatusOK || lifecycleItems(t, second)[0].(map[string]any)["id"] != "mlt_001" {
		t.Fatalf("page 2 = %d %#v", status, second)
	}
	if len(*calls) != 2 || (*calls)[0] != "msg_one|agent@example.com" {
		t.Fatalf("lister calls = %v", *calls)
	}
}

func TestMessageLifecyclePageBoundaryHasNoDuplicates(t *testing.T) {
	srv, _ := newLifecycleServer(t, lifecycleTransitions(4))
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 2; page++ {
		query := "?limit=2"
		if cursor != "" {
			query += "&cursor=" + url.QueryEscape(cursor)
		}
		status, body := lifecycleGET(t, srv, "agent@example.com", "msg_one", query)
		if status != http.StatusOK {
			t.Fatalf("page %d = %d %#v", page, status, body)
		}
		for _, raw := range lifecycleItems(t, body) {
			id := raw.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatalf("duplicate %s", id)
			}
			seen[id] = true
		}
		cursor, _ = body["next_cursor"].(string)
	}
	if len(seen) != 4 || cursor != "" {
		t.Fatalf("seen=%v final cursor=%q", seen, cursor)
	}
}

func TestMessageLifecycleDefaultAndMaximumLimits(t *testing.T) {
	srv, _ := newLifecycleServer(t, lifecycleTransitions(101))
	for _, query := range []string{"", "?limit=100"} {
		status, body := lifecycleGET(t, srv, "agent@example.com", "msg_one", query)
		if status != http.StatusOK || len(lifecycleItems(t, body)) != 100 || body["next_cursor"] == nil {
			t.Fatalf("query %q = %d %#v", query, status, body)
		}
	}
	status, body := lifecycleGET(t, srv, "agent@example.com", "msg_one", "?limit=101")
	if status != http.StatusUnprocessableEntity || errCode(body) != "invalid_request" {
		t.Fatalf("over max = %d %#v", status, body)
	}
}

func TestMessageLifecycleCursorBindingAndTamper(t *testing.T) {
	srv, calls := newLifecycleServer(t, lifecycleTransitions(3))
	_, first := lifecycleGET(t, srv, "agent@example.com", "msg_one", "?limit=1")
	cursor := first["next_cursor"].(string)
	var decoded messageLifecycleCursor
	if err := DecodeCursor([]string{lifecycleTestSecret}, cursor, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 1 || decoded.AgentID != "agent@example.com" || decoded.MessageID != "msg_one" || decoded.ID != "mlt_000" || decoded.OccurredAt.IsZero() {
		t.Fatalf("cursor payload = %#v", decoded)
	}
	for name, tc := range map[string][3]string{
		"message": {"agent@example.com", "msg_two", cursor},
		"agent":   {"other@example.com", "msg_one", cursor},
		"tamper":  {"agent@example.com", "msg_one", cursor[:len(cursor)-1] + "x"},
	} {
		t.Run(name, func(t *testing.T) {
			status, body := lifecycleGET(t, srv, tc[0], tc[1], "?cursor="+url.QueryEscape(tc[2]))
			if status != http.StatusBadRequest || errCode(body) != "invalid_cursor" {
				t.Fatalf("got %d %#v", status, body)
			}
		})
	}
	if len(*calls) != 1 {
		t.Fatalf("invalid cursors reached lister: %v", *calls)
	}
}

func TestMessageLifecycleHidesForeignAndMissingOwnership(t *testing.T) {
	srv, calls := newLifecycleServer(t, nil)
	for _, agentID := range []string{"foreign@example.com", "absent@example.com"} {
		status, body := lifecycleGET(t, srv, agentID, "msg_one", "")
		if status != http.StatusNotFound || errCode(body) != "not_found" {
			t.Fatalf("%s = %d %#v", agentID, status, body)
		}
	}
	status, body := lifecycleGET(t, srv, "agent@example.com", "missing", "")
	if status != http.StatusNotFound || errCode(body) != "not_found" {
		t.Fatalf("missing message = %d %#v", status, body)
	}
	if len(*calls) != 1 {
		t.Fatalf("ownership failures reached lister: %v", *calls)
	}
}

func TestMessageLifecycleEmptyShapeAndCanonicalFields(t *testing.T) {
	emptySrv, _ := newLifecycleServer(t, nil)
	status, empty := lifecycleGET(t, emptySrv, "agent@example.com", "msg_one", "")
	if status != http.StatusOK || len(lifecycleItems(t, empty)) != 0 || empty["next_cursor"] != nil {
		t.Fatalf("empty = %d %#v", status, empty)
	}

	items := lifecycleTransitions(1)
	items[0].Recipient = "recipient@example.net"
	items[0].Evidence = map[string]any{"smtp_detail": "250 accepted"}
	items[0].CorrelationIDs = map[string]string{"provider_message_id": "provider-1"}
	srv, _ := newLifecycleServer(t, items)
	_, body := lifecycleGET(t, srv, "agent@example.com", "msg_one", "")
	item := lifecycleItems(t, body)[0].(map[string]any)
	for _, key := range []string{"id", "message_id", "direction", "recipient", "stage", "outcome", "reason_code", "retryable", "evidence", "correlation_ids", "occurred_at", "reconstructed"} {
		if _, ok := item[key]; !ok {
			t.Errorf("canonical field %q missing from %#v", key, item)
		}
	}
}

func TestMessageLifecycleOpenAPIOperationAndEnums(t *testing.T) {
	s, _ := newLifecycleServer(t, nil)
	doc := s.API.OpenAPI()
	op := doc.Paths["/v1/agents/{email}/messages/{id}/lifecycle"].Get
	if op == nil || op.OperationID != "get-message-lifecycle" {
		t.Fatalf("lifecycle operation = %#v", op)
	}
	var transitionSchemaFound bool
	for name, schema := range doc.Components.Schemas.Map() {
		if schema.Properties["reason_code"] == nil || schema.Properties["correlation_ids"] == nil || schema.Properties["reconstructed"] == nil {
			continue
		}
		transitionSchemaFound = true
		for field, wants := range map[string][]any{
			"direction":   {"inbound", "outbound"},
			"stage":       {"accepted", "authentication", "review", "suppression", "queued", "submission", "delivery", "complaint"},
			"outcome":     {"accepted", "passed", "failed", "indeterminate", "pending", "approved", "rejected", "blocked", "applied", "enqueued", "deferred", "delivered", "bounced", "reported"},
			"reason_code": {string(messagelifecycle.ReasonAcceptanceInboundSMTP), string(messagelifecycle.ReasonComplaintRecipientReported)},
		} {
			got := schema.Properties[field].Enum
			for _, want := range wants {
				found := false
				for _, value := range got {
					if value == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("schema %s field %s enum %v missing %v", name, field, got, want)
				}
			}
		}
		for _, field := range []string{"evidence", "correlation_ids"} {
			if schema.Properties[field].AdditionalProperties == nil {
				t.Errorf("schema %s field %s is not an open object", name, field)
			}
		}
	}
	if !transitionSchemaFound {
		t.Fatal("message lifecycle transition schema not found")
	}
}
