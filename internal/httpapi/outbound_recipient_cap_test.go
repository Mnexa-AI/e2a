package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

func assertTooManyRecipients(t *testing.T, code int, body map[string]any, provided int) {
	t.Helper()
	if code != 400 || errCode(body) != "too_many_recipients" {
		t.Fatalf("want 400 too_many_recipients, got %d %v", code, body)
	}
	errObj, _ := body["error"].(map[string]any)
	details, _ := errObj["details"].(map[string]any)
	if details["max_recipients"] != float64(maxRecipients) || details["provided"] != float64(provided) {
		t.Fatalf("want recipient-cap details max=%d provided=%d, got %v", maxRecipients, provided, body)
	}
}

// A single recipient field over the total cap must reach the same handler-level
// validation as a cap violation distributed across fields.
func TestSendTooManyRecipients(t *testing.T) {
	srv := testServer(t)
	to := make([]string, 51)
	for i := range to {
		to[i] = fmt.Sprintf("r%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": to, "subject": "Hi", "text": "hello",
	})
	assertTooManyRecipients(t, code, body, 51)
}

// The cap counts to+cc+bcc together, so 50 split across the three fields must
// pass and 51 split across them must fail. Each field is ≤ 50, so the schema
// layer does NOT fire — the runtime recipientCountError fires instead, returning
// 400 too_many_recipients (the combined-count path).
func TestSendRecipientCapCountsAllFields(t *testing.T) {
	srv := testServer(t)
	mk := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s%d@x.com", prefix, i)
		}
		return out
	}
	// 20 + 20 + 11 = 51 -> over the cap (each field ≤ 50, so runtime fires).
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": mk("t", 20), "cc": mk("c", 20), "bcc": mk("b", 11), "subject": "Hi", "text": "hello",
	})
	assertTooManyRecipients(t, code, body, 51)
	// 20 + 20 + 10 = 50 -> at the cap, allowed (subject HOLD avoided -> sent).
	code, _ = postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": mk("t", 20), "cc": mk("c", 20), "bcc": mk("b", 10), "subject": "Hi", "text": "hello",
	})
	if code != 200 {
		t.Fatalf("at cap: want 200, got %d", code)
	}
}

// Forward uses the same 400/code/details contract.
func TestForwardTooManyRecipients(t *testing.T) {
	srv := testServer(t)
	to := make([]string, 51)
	for i := range to {
		to[i] = fmt.Sprintf("r%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/forward", "good", map[string]any{
		"to": to, "text": "fwd",
	})
	assertTooManyRecipients(t, code, body, 51)
}

// Reply uses the same 400/code/details contract.
func TestReplyTooManyRecipients(t *testing.T) {
	srv := testServer(t)
	cc := make([]string, 51)
	for i := range cc {
		cc[i] = fmt.Sprintf("r%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/reply", "good", map[string]any{
		"text": "re", "cc": cc,
	})
	assertTooManyRecipients(t, code, body, 51)
}

// Reviewer overrides must not bypass or drift from the normal outbound cap.
func TestApproveOverrideTooManyRecipients(t *testing.T) {
	srv := testServer(t)
	to := make([]string, 51)
	for i := range to {
		to[i] = fmt.Sprintf("r%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+"/v1/reviews/msg_pending/approve", "good", map[string]any{"to": to})
	assertTooManyRecipients(t, code, body, 51)
}

// An override replaces only the fields it supplies. Count the final merged
// draft, not just the override arrays, so a partial edit cannot push a valid
// held message over the cap.
func TestApprovePartialOverrideCountsStoredRecipients(t *testing.T) {
	srv := testServer(t, func(d *Deps) {
		d.GetReviewWithContent = func(_ context.Context, _, id string) (*identity.Message, error) {
			if id != "msg_pending" {
				return nil, fmt.Errorf("not found")
			}
			to := make([]string, 30)
			for i := range to {
				to[i] = fmt.Sprintf("stored%d@x.com", i)
			}
			return &identity.Message{
				ID: id, AgentID: "support@acme.com", Direction: "outbound",
				Status: "pending_review", ToRecipients: to,
			}, nil
		}
	})
	bcc := make([]string, 21)
	for i := range bcc {
		bcc[i] = fmt.Sprintf("override%d@x.com", i)
	}
	code, body := postJSON(t, srv.URL+"/v1/reviews/msg_pending/approve", "good", map[string]any{"bcc": bcc})
	assertTooManyRecipients(t, code, body, 51)
}

// The combined cap cannot be represented by JSON Schema maxItems on each
// individual array without changing the error contract. Keep it discoverable
// in the generated schema descriptions while leaving handler validation in
// control of the uniform 400 response.
func TestRecipientCapIsDocumentedWithoutPerFieldSchemaValidation(t *testing.T) {
	raw, err := json.Marshal(New(Deps{}).API.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Description string `json:"description"`
					MaxItems    *int   `json:"maxItems"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for schema, fields := range map[string][]string{
		"SendEmailRequest": {"to", "cc", "bcc"},
		"ReplyRequest":     {"cc", "bcc"},
		"ForwardRequest":   {"to", "cc", "bcc"},
		"ApproveRequest":   {"to", "cc", "bcc"},
	} {
		for _, field := range fields {
			property := doc.Components.Schemas[schema].Properties[field]
			if property.MaxItems != nil {
				t.Errorf("%s.%s: maxItems must not preempt the uniform handler error, got %d", schema, field, *property.MaxItems)
			}
			description := strings.ToLower(property.Description)
			if !strings.Contains(description, "50") || !strings.Contains(description, "combined") {
				t.Errorf("%s.%s: recipient cap is not discoverable in description %q", schema, field, property.Description)
			}
		}
	}
}
