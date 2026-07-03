package httpapi

import (
	"net/http"
	"testing"
)

func TestCreateTemplate(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates", "good", map[string]any{
		"name": "Welcome", "alias": "welcome-2",
		"subject": "Hello {{name}}", "body": "Hi {{name}}!", "html_body": "<p>{{name}} {{{markup}}}</p>",
	})
	if code != 201 {
		t.Fatalf("want 201, got %d %v", code, body)
	}
	if body["id"] != "tmpl_new" || body["alias"] != "welcome-2" || body["subject"] != "Hello {{name}}" {
		t.Fatalf("unexpected view: %v", body)
	}
	if body["created_at"] == nil || body["updated_at"] == nil {
		t.Fatalf("missing timestamps: %v", body)
	}
}

func TestCreateTemplateNoAlias(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates", "good", map[string]any{
		"name": "Bare", "subject": "S", "body": "B",
	})
	if code != 201 {
		t.Fatalf("want 201 without alias, got %d %v", code, body)
	}
	if _, present := body["alias"]; present {
		t.Fatalf("empty alias must be omitted, got %v", body)
	}
}

func TestCreateTemplateNameRequired(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates", "good", map[string]any{
		"name": "", "subject": "S", "body": "B",
	})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request for empty name, got %d %v", code, body)
	}
}

func TestCreateTemplateSubjectBodyRequired(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates", "good", map[string]any{
		"name": "T", "subject": "S", "body": "",
	})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request for empty body, got %d %v", code, body)
	}
}

func TestCreateTemplateBadAlias(t *testing.T) {
	srv := testServer(t)
	for _, alias := range []string{"1leading-digit", "has space", "-leading-dash", "with/slash"} {
		code, body := postJSON(t, srv.URL+"/v1/templates", "good", map[string]any{
			"name": "T", "alias": alias, "subject": "S", "body": "B",
		})
		if code != 400 || errCode(body) != "invalid_request" {
			t.Fatalf("alias %q: want 400 invalid_request, got %d %v", alias, code, body)
		}
	}
}

func TestCreateTemplateAliasTaken(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates", "good", map[string]any{
		"name": "T", "alias": "taken", "subject": "S", "body": "B",
	})
	if code != 409 || errCode(body) != "alias_taken" {
		t.Fatalf("want 409 alias_taken, got %d %v", code, body)
	}
}

func TestCreateTemplateInvalidSyntax(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates", "good", map[string]any{
		"name": "T", "subject": "Hi {{name", "body": "B",
	})
	if code != 400 || errCode(body) != "invalid_template" {
		t.Fatalf("want 400 invalid_template for unclosed tag, got %d %v", code, body)
	}
	details, _ := body["error"].(map[string]any)["details"].(map[string]any)
	if details["part"] != "subject" {
		t.Fatalf("details must name the failing part, got %v", body)
	}
}

func TestCreateTemplateReservedSyntax(t *testing.T) {
	srv := testServer(t)
	// Reserved Mustache structural syntax in each part is rejected at create.
	for part, payload := range map[string]map[string]any{
		"subject":   {"name": "T", "subject": "{{#x}}", "body": "B"},
		"body":      {"name": "T", "subject": "S", "body": "{{>partial}}"},
		"html_body": {"name": "T", "subject": "S", "body": "B", "html_body": "{{!comment}}"},
	} {
		code, body := postJSON(t, srv.URL+"/v1/templates", "good", payload)
		if code != 400 || errCode(body) != "invalid_template" {
			t.Fatalf("%s: want 400 invalid_template for reserved syntax, got %d %v", part, code, body)
		}
	}
}

func TestCreateTemplateCapReached(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates", "overcap", map[string]any{
		"name": "T", "subject": "S", "body": "B",
	})
	if code != 400 || errCode(body) != "template_limit_reached" {
		t.Fatalf("want 400 template_limit_reached, got %d %v", code, body)
	}
}

func TestListTemplates(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/templates", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "tmpl_1" {
		t.Fatalf("unexpected items: %v", body)
	}
	if body["next_cursor"] != nil {
		t.Fatalf("expected null next_cursor on single page, got %v", body["next_cursor"])
	}
}

func TestGetTemplate(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/templates/tmpl_1", "good")
	if code != 200 || body["id"] != "tmpl_1" || body["alias"] != "welcome" {
		t.Fatalf("want 200 tmpl_1, got %d %v", code, body)
	}
}

func TestGetTemplateNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/templates/tmpl_missing", "good")
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestUpdateTemplate(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/templates/tmpl_1", "good", map[string]any{
		"name": "Renamed", "subject": "New {{name}}",
	})
	if code != 200 || body["name"] != "Renamed" || body["subject"] != "New {{name}}" {
		t.Fatalf("want 200 updated, got %d %v", code, body)
	}
}

func TestUpdateTemplateInvalidSyntax(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/templates/tmpl_1", "good", map[string]any{
		"body": "{{/close}}",
	})
	if code != 400 || errCode(body) != "invalid_template" {
		t.Fatalf("want 400 invalid_template on changed part, got %d %v", code, body)
	}
}

func TestUpdateTemplateEmptyBodyRejected(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/templates/tmpl_1", "good", map[string]any{"body": ""})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request for clearing body, got %d %v", code, body)
	}
}

func TestUpdateTemplateAliasTaken(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/templates/tmpl_1", "good", map[string]any{"alias": "taken"})
	if code != 409 || errCode(body) != "alias_taken" {
		t.Fatalf("want 409 alias_taken, got %d %v", code, body)
	}
}

func TestUpdateTemplateNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "PATCH", srv.URL+"/v1/templates/tmpl_missing", "good", map[string]any{"name": "x"})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestDeleteTemplate(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("DELETE", srv.URL+"/v1/templates/tmpl_1", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
}

func TestDeleteTemplateNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/templates/tmpl_missing", "good", nil)
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestTemplatesUnauthorized(t *testing.T) {
	srv := testServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/templates", "")
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
}

func TestValidateTemplateValid(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates/validate", "good", map[string]any{
		"subject":   "Hi {{name}}",
		"body":      "Plan: {{plan.tier}} & co",
		"html_body": "<p>{{html}} / {{{html}}}</p>",
		"test_data": map[string]any{
			"name": "Alice", "plan": map[string]any{"tier": "pro"}, "html": "<b>x</b>",
		},
	})
	if code != 200 || body["valid"] != true {
		t.Fatalf("want 200 valid, got %d %v", code, body)
	}
	rendered, _ := body["rendered"].(map[string]any)
	if rendered["subject"] != "Hi Alice" {
		t.Fatalf("rendered subject = %v", rendered)
	}
	if rendered["body"] != "Plan: pro & co" {
		t.Fatalf("text body must not be HTML-escaped: %v", rendered)
	}
	if rendered["html_body"] != "<p>&lt;b&gt;x&lt;/b&gt; / <b>x</b></p>" {
		t.Fatalf("html body must escape {{x}} and keep {{{x}}} raw: %v", rendered)
	}
	suggested, _ := body["suggested_data"].(map[string]any)
	for _, v := range []string{"name", "plan.tier", "html"} {
		if suggested[v] != v+"_value" {
			t.Fatalf("suggested_data missing %q: %v", v, suggested)
		}
	}
	errs, _ := body["errors"].([]any)
	if len(errs) != 0 {
		t.Fatalf("valid response must have empty errors array, got %v", body["errors"])
	}
}

func TestValidateTemplateInvalid(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates/validate", "good", map[string]any{
		"subject": "{{#loop}}", "body": "ok {{x}}", "html_body": "{{broken",
	})
	if code != 200 || body["valid"] != false {
		t.Fatalf("want 200 with valid=false, got %d %v", code, body)
	}
	errs, _ := body["errors"].([]any)
	if len(errs) != 2 {
		t.Fatalf("want 2 part errors (subject, html_body), got %v", body["errors"])
	}
	parts := map[string]bool{}
	for _, e := range errs {
		parts[e.(map[string]any)["part"].(string)] = true
	}
	if !parts["subject"] || !parts["html_body"] {
		t.Fatalf("errors must name subject + html_body, got %v", body["errors"])
	}
	if _, present := body["rendered"]; present {
		t.Fatalf("invalid response must omit rendered, got %v", body)
	}
	// The valid part's vars still feed suggested_data.
	suggested, _ := body["suggested_data"].(map[string]any)
	if suggested["x"] != "x_value" {
		t.Fatalf("suggested_data should include vars from parsing parts: %v", suggested)
	}
}

// TestValidateTemplateBigIntPrecision mirrors the send-path guarantee on the
// validate endpoint: test_data numbers arrive as json.Number and render
// digit-exact past 2^53.
func TestValidateTemplateBigIntPrecision(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates/validate", "good", map[string]any{
		"subject":   "Order {{n}}",
		"body":      "B",
		"test_data": map[string]any{"n": int64(123456789012345678)},
	})
	if code != 200 || body["valid"] != true {
		t.Fatalf("want 200 valid, got %d %v", code, body)
	}
	rendered, _ := body["rendered"].(map[string]any)
	if rendered["subject"] != "Order 123456789012345678" {
		t.Fatalf("rendered subject = %v, want exact big-int digits", rendered["subject"])
	}
}

func TestValidateTemplateNoTestData(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/templates/validate", "good", map[string]any{
		"subject": "Hi {{name}}", "body": "B",
	})
	if code != 200 || body["valid"] != true {
		t.Fatalf("want 200 valid, got %d %v", code, body)
	}
	rendered, _ := body["rendered"].(map[string]any)
	if rendered["subject"] != "Hi " {
		t.Fatalf("missing vars must render empty in the preview: %v", rendered)
	}
}
