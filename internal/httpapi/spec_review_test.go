package httpapi

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// operationByID finds an operation anywhere in paths by operationId.
func operationByID(t *testing.T, doc map[string]any, operationID string) map[string]any {
	t.Helper()
	paths, _ := doc["paths"].(map[string]any)
	for _, pi := range paths {
		item, _ := pi.(map[string]any)
		for _, op := range item {
			opm, ok := op.(map[string]any)
			if ok && opm["operationId"] == operationID {
				return opm
			}
		}
	}
	t.Fatalf("operation %q not found in spec", operationID)
	return nil
}

// renderSpec renders the live OpenAPI document and unmarshals it into a generic
// map so the review-hardening contract tests can assert on operations + schemas
// without a typed OpenAPI model.
func renderSpec(t *testing.T) map[string]any {
	t.Helper()
	y, err := New(Deps{}).OpenAPIYAML()
	if err != nil {
		t.Fatalf("render spec: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(y, &doc); err != nil {
		t.Fatalf("unmarshal spec: %v", err)
	}
	return doc
}

// operationResponses finds the operation with the given operationId anywhere in
// paths and returns its responses map.
func operationResponses(t *testing.T, doc map[string]any, operationID string) map[string]any {
	t.Helper()
	resp, _ := operationByID(t, doc, operationID)["responses"].(map[string]any)
	return resp
}

// Once an outbound request is durably accepted, the message row, River job,
// and keyed response commit atomically. The public contract must tell callers
// how to recover from the ambiguous boundary where that commit succeeds but
// the HTTP response is lost: replay the same request with the same key.
func TestSpecOutboundIdempotencyDocumentsResponseLoss(t *testing.T) {
	doc := renderSpec(t)
	const required = "If the response is lost after durable acceptance"
	for _, operationID := range []string{"sendMessage", "replyToMessage", "forwardMessage"} {
		op := operationByID(t, doc, operationID)
		params, _ := op["parameters"].([]any)
		var description string
		for _, raw := range params {
			param, _ := raw.(map[string]any)
			if param["in"] == "header" && param["name"] == "Idempotency-Key" {
				description, _ = param["description"].(string)
				break
			}
		}
		if !strings.Contains(description, required) {
			t.Errorf("%s Idempotency-Key description must document the post-commit response-loss retry boundary; got %q", operationID, description)
		}
	}
}

// schemaProps returns the properties map of a named component schema.
func schemaProps(t *testing.T, doc map[string]any, schemaName string) map[string]any {
	t.Helper()
	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	sc, ok := schemas[schemaName].(map[string]any)
	if !ok {
		t.Fatalf("schema %q not found in spec", schemaName)
	}
	props, _ := sc["properties"].(map[string]any)
	return props
}

// enumOf returns the enum string slice for a property, or nil if absent.
func enumOf(props map[string]any, field string) []string {
	p, ok := props[field].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := p["enum"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// typeIsNullable reports whether a property's type includes "null"
// (either as a list ["array","null"] or via a separate nullable flag).
func typeIsNullable(props map[string]any, field string) bool {
	p, ok := props[field].(map[string]any)
	if !ok {
		return false
	}
	switch ty := p["type"].(type) {
	case []any:
		for _, v := range ty {
			if v == "null" {
				return true
			}
		}
	case string:
		if ty == "null" {
			return true
		}
	}
	return false
}

// Held outbound drafts have not been composed into MIME yet: reviewers read
// their editable content from `body`, while `raw_message` is explicitly null.
// Keep the field required so every detail response has a stable shape, but make
// its value nullable until approval composes the canonical sent copy.
func TestSpecMessageViewRawMessageRequiredNullable(t *testing.T) {
	doc := renderSpec(t)
	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	messageView, ok := schemas["MessageView"].(map[string]any)
	if !ok {
		t.Fatal("MessageView schema not found")
	}

	required, _ := messageView["required"].([]any)
	var rawRequired bool
	for _, field := range required {
		if field == "raw_message" {
			rawRequired = true
			break
		}
	}
	if !rawRequired {
		t.Fatal("MessageView.raw_message must remain required")
	}
	if !typeIsNullable(schemaProps(t, doc, "MessageView"), "raw_message") {
		t.Fatal("MessageView.raw_message must allow null for uncomposed outbound review drafts")
	}
}

func setEqual(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, y := range b {
		if !seen[y] {
			return false
		}
	}
	return true
}

// MED-1 — outbound operations must declare a 202 response (HITL hold).
func TestSpecOutbound202Declared(t *testing.T) {
	doc := renderSpec(t)
	for _, opID := range []string{"sendMessage", "replyToMessage", "forwardMessage", "testAgent"} {
		resp := operationResponses(t, doc, opID)
		if _, ok := resp["202"]; !ok {
			t.Errorf("operation %q is missing a 202 response (HITL hold returns 202 at runtime); declared codes: %v", opID, keysOf(resp))
		}
	}
}

// Async outbound approval is another non-terminal enqueue and follows the same
// 202 Accepted convention as send/reply/forward. Terminal sent/released
// approval outcomes remain represented by the operation's inferred 200.
func TestSpecApprove202Declared(t *testing.T) {
	doc := renderSpec(t)
	resp := operationResponses(t, doc, "approveReview")
	if ref := responseSchemaRef(resp, "202"); ref != "#/components/schemas/SendResultView" {
		t.Errorf("approveReview: 202 must be declared with SendResultView, got %q; codes=%v", ref, keysOf(resp))
	}
	if ref := responseSchemaRef(resp, "200"); ref != "#/components/schemas/SendResultView" {
		t.Errorf("approveReview: terminal 200 must remain declared with SendResultView, got %q; codes=%v", ref, keysOf(resp))
	}
}

// verifyDomain must NOT declare a 412: a not-yet-published record is the normal
// verified:false outcome, returned as 200. 412 (Precondition Failed) is reserved
// for conditional-request failures; clients branch on the body's `verified`, not
// the HTTP status.
func TestSpecVerifyDomainNo412(t *testing.T) {
	doc := renderSpec(t)
	resp := operationResponses(t, doc, "verifyDomain")
	if _, ok := resp["412"]; ok {
		t.Errorf("verifyDomain must not declare a 412 response (not-yet-verified is a normal 200 with verified:false); declared codes: %v", keysOf(resp))
	}
	if _, ok := resp["200"]; !ok {
		t.Errorf("verifyDomain must declare a 200 response (both verified and not-yet-verified return 200); declared codes: %v", keysOf(resp))
	}
}

// MED-3 — registerDomain must declare a 409 response (duplicate domain conflict).
func TestSpecRegisterDomain409Declared(t *testing.T) {
	doc := renderSpec(t)
	resp := operationResponses(t, doc, "registerDomain")
	if _, ok := resp["409"]; !ok {
		t.Errorf("registerDomain is missing a 409 response (another owner -> conflict); declared codes: %v", keysOf(resp))
	}
}

// responseSchemaRef returns the $ref of a response's application/json schema, or
// "" when the response (or its schema) is absent.
func responseSchemaRef(resp map[string]any, code string) string {
	r, ok := resp[code].(map[string]any)
	if !ok {
		return ""
	}
	content, _ := r["content"].(map[string]any)
	mt, _ := content["application/json"].(map[string]any)
	sc, _ := mt["schema"].(map[string]any)
	ref, _ := sc["$ref"].(string)
	return ref
}

// The permanent GA 402/429 split (decision #49/#50): 402 limit_exceeded is a
// stock/flow QUOTA cap, 429 rate_limited is a throughput/request-RATE cap. This
// test pins BOTH halves of the split on the write operations so neither the
// typed 402 (LimitExceededEnvelope, #439) nor the typed 429 (RateLimitedEnvelope)
// declaration silently regresses. Quota-enforcing writes must declare 402;
// throughput/send-rate-limited writes must declare 429; the two are typed to
// distinct envelopes so codegen surfaces concrete detail shapes.
func TestSpec402_429Split(t *testing.T) {
	doc := renderSpec(t)

	// 402 limit_exceeded (QUOTA) — every cap-enforcing write.
	for _, opID := range []string{"createAgent", "registerDomain", "sendMessage", "replyToMessage", "forwardMessage", "testAgent"} {
		resp := operationResponses(t, doc, opID)
		if ref := responseSchemaRef(resp, "402"); ref != "#/components/schemas/LimitExceededEnvelope" {
			t.Errorf("%s: 402 must be declared with LimitExceededEnvelope, got %q; codes=%v", opID, ref, keysOf(resp))
		}
	}

	// 429 rate_limited (RATE) — every throughput/send-rate-limited write.
	for _, opID := range []string{"createAgent", "sendMessage", "replyToMessage", "forwardMessage", "testAgent", "approveReview"} {
		resp := operationResponses(t, doc, opID)
		if ref := responseSchemaRef(resp, "429"); ref != "#/components/schemas/RateLimitedEnvelope" {
			t.Errorf("%s: 429 must be declared with RateLimitedEnvelope, got %q; codes=%v", opID, ref, keysOf(resp))
		}
	}

	// The 429 detail schema must carry the typed retry hint (retry_after_seconds).
	props := schemaProps(t, doc, "RateLimitedDetails")
	if _, ok := props["retry_after_seconds"]; !ok {
		t.Errorf("RateLimitedDetails must declare retry_after_seconds; props=%v", keysOf(props))
	}
}

// MED-5 — response enum policy for GA (see docs/api.md "Versioning & stability"):
//   - A field whose value set is genuinely closed forever (direction: a message is
//     inbound or outbound, period) carries a closed enum.
//   - A field whose value set the server may grow (delivery/review/send/event
//     status, sending_status, sent_as, method, event types) is an OPEN string with
//     no enum, so adding a value later doesn't break strict spec-generated clients.
//
// This test pins both halves so neither regresses: an open field must not silently
// re-acquire a closed enum, and direction must not lose its enum.
func TestSpecStatusEnums(t *testing.T) {
	doc := renderSpec(t)

	// Closed-forever enums (must carry the exact enum).
	closed := []struct {
		schema string
		field  string
		want   []string
	}{
		{"MessageView", "direction", []string{"inbound", "outbound"}},
		{"MessageSummaryView", "direction", []string{"inbound", "outbound"}},
	}
	for _, c := range closed {
		props := schemaProps(t, doc, c.schema)
		got := enumOf(props, c.field)
		if !setEqual(got, c.want...) {
			t.Errorf("%s.%s enum = %v, want closed enum %v", c.schema, c.field, got, c.want)
		}
	}

	// Open (growable) response fields — must be plain strings with NO enum, so a
	// new server value doesn't break a frozen strict client. Re-closing any of
	// these is a breaking change for the GA contract.
	open := []struct {
		schema string
		field  string
	}{
		{"MessageView", "review_status"},
		{"MessageSummaryView", "review_status"},
		{"MessageView", "delivery_status"},
		{"MessageSummaryView", "delivery_status"},
		{"MessageView", "sent_as"},
		{"MessageSummaryView", "sent_as"},
		{"SendResultView", "status"},
		{"SendResultView", "sent_as"},
		{"SendResultView", "method"},
		{"EventJSON", "status"},
		{"EventJSON", "type"},
		{"RedeliverView", "status"},
		{"RedeliverDelivery", "status"},
		{"WebhookDeliveryView", "status"},
		{"WebhookDeliveryView", "type"},
		{"DomainView", "sending_status"},
		{"ReviewView", "review_status"},
	}
	for _, c := range open {
		props := schemaProps(t, doc, c.schema)
		if got := enumOf(props, c.field); len(got) != 0 {
			t.Errorf("%s.%s must be an OPEN string (no enum) for GA forward-compat, but has enum %v", c.schema, c.field, got)
		}
	}
}

// MED-6 — Page.items must NOT be nullable. (As of GA blocker #3 every list —
// agents/domains/webhooks/suppressions included — uses the shared Page[T]
// envelope, so the per-resource wrapper schemas are gone; the generic items
// check below now covers all of them.)
func TestSpecListArraysNotNullable(t *testing.T) {
	doc := renderSpec(t)
	// Page[*].items — find every schema with an "items" + "next_cursor" pair.
	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	checked := 0
	for name, sc := range schemas {
		scm, _ := sc.(map[string]any)
		props, _ := scm["properties"].(map[string]any)
		if props == nil {
			continue
		}
		if _, hasItems := props["items"]; !hasItems {
			continue
		}
		if _, hasCursor := props["next_cursor"]; !hasCursor {
			continue
		}
		if typeIsNullable(props, "items") {
			t.Errorf("page schema %s.items is declared nullable but the handler always emits []", name)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("found no Page schemas to check — test wiring is wrong")
	}
}

// LOW-1 — WebhookView must not declare the vestigial previous_secret_expires_at.
func TestSpecWebhookViewNoPreviousSecretExpiry(t *testing.T) {
	doc := renderSpec(t)
	props := schemaProps(t, doc, "WebhookView")
	if _, ok := props["previous_secret_expires_at"]; ok {
		t.Errorf("WebhookView still declares previous_secret_expires_at (never populated by webhookView; rotate response carries it)")
	}
}

// LOW-2 — webhook view timestamps must carry format: date-time.
func TestSpecWebhookTimestampsFormatted(t *testing.T) {
	doc := renderSpec(t)
	check := func(schema string, fields ...string) {
		props := schemaProps(t, doc, schema)
		for _, f := range fields {
			p, ok := props[f].(map[string]any)
			if !ok {
				continue // omitempty optional field may be absent only if removed; here all present
			}
			if p["format"] != "date-time" {
				t.Errorf("%s.%s format = %v, want date-time", schema, f, p["format"])
			}
		}
	}
	check("WebhookView", "created_at", "auto_disabled_at", "last_delivered_at")
	check("WebhookDeliveryView", "created_at", "last_attempt_at", "next_retry_at")
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
