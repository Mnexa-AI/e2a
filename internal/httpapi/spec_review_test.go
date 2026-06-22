package httpapi

import (
	"testing"

	"gopkg.in/yaml.v3"
)

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
	paths, _ := doc["paths"].(map[string]any)
	for _, pi := range paths {
		item, _ := pi.(map[string]any)
		for _, op := range item {
			opm, ok := op.(map[string]any)
			if !ok {
				continue
			}
			if opm["operationId"] == operationID {
				resp, _ := opm["responses"].(map[string]any)
				return resp
			}
		}
	}
	t.Fatalf("operation %q not found in spec", operationID)
	return nil
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

// MED-2 — verifyDomain must declare a 412 response (TXT not yet published).
func TestSpecVerifyDomain412Declared(t *testing.T) {
	doc := renderSpec(t)
	resp := operationResponses(t, doc, "verifyDomain")
	if _, ok := resp["412"]; !ok {
		t.Errorf("verifyDomain is missing a 412 response (missing TXT returns 412 at runtime); declared codes: %v", keysOf(resp))
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

// MED-5 — status-ish fields must carry enums matching the values the server emits.
func TestSpecStatusEnums(t *testing.T) {
	doc := renderSpec(t)

	cases := []struct {
		schema string
		field  string
		want   []string
	}{
		{"MessageView", "direction", []string{"inbound", "outbound"}},
		{"MessageSummaryView", "direction", []string{"inbound", "outbound"}},
		{"MessageView", "hitl_status", []string{"pending_review", "sent", "review_rejected", "review_expired_approved", "review_expired_rejected"}},
		{"MessageSummaryView", "hitl_status", []string{"pending_review", "sent", "review_rejected", "review_expired_approved", "review_expired_rejected"}},
		{"SendResultView", "status", []string{"sent", "pending_review", "review_approved"}},
		{"EventJSON", "status", []string{"pending", "processed", "no_match"}},
		{"RedeliverView", "status", []string{"pending", "scheduled"}},
		{"WebhookDeliveryView", "status", []string{"pending", "delivered", "failed"}},
	}
	for _, c := range cases {
		props := schemaProps(t, doc, c.schema)
		got := enumOf(props, c.field)
		if !setEqual(got, c.want...) {
			t.Errorf("%s.%s enum = %v, want %v", c.schema, c.field, got, c.want)
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
