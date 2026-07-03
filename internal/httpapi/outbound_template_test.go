package httpapi

import (
	"reflect"
	"strings"
	"testing"
)

// Slice D: template references on POST /v1/agents/{email}/messages.
// The fixture template (sampleTemplate, tmpl_1 / alias "welcome"):
//   subject:   "Hello {{name}}"
//   body:      "Hi {{name}}, your plan is {{plan.tier}}."
//   html_body: "<p>Hi {{name}}: {{{markup}}}</p>"

func TestSendWithTemplateID(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to":          []string{"alice@x.com"},
		"template_id": "tmpl_1",
		"template_data": map[string]any{
			"name": "A&B", "plan": map[string]any{"tier": "pro"}, "markup": "<i>hi</i>",
		},
	})
	if code != 200 || body["status"] != "sent" {
		t.Fatalf("want 200 sent, got %d %v", code, body)
	}
	// The delivery seam must receive RENDERED content, never template source.
	req := lastDeliveredReq()
	if req.Subject != "Hello A&B" {
		t.Errorf("delivered subject = %q, want rendered %q", req.Subject, "Hello A&B")
	}
	if req.Body != "Hi A&B, your plan is pro." {
		t.Errorf("delivered body = %q (dot path + no HTML escaping in text)", req.Body)
	}
	// HTML part: {{name}} escaped, {{{markup}}} raw.
	if req.HTMLBody != "<p>Hi A&amp;B: <i>hi</i></p>" {
		t.Errorf("delivered html = %q", req.HTMLBody)
	}
}

func TestSendWithTemplateAlias(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to":             []string{"alice@x.com"},
		"template_alias": "welcome",
		"template_data":  map[string]any{"name": "Zoe"},
	})
	if code != 200 || body["status"] != "sent" {
		t.Fatalf("want 200 sent, got %d %v", code, body)
	}
	req := lastDeliveredReq()
	if req.Subject != "Hello Zoe" {
		t.Errorf("delivered subject = %q", req.Subject)
	}
	// Missing vars render empty (permissive model).
	if req.Body != "Hi Zoe, your plan is ." {
		t.Errorf("delivered body = %q, want missing plan.tier as empty", req.Body)
	}
}

// TestSendTemplateHeldShowsRenderedBody: rendering happens BEFORE the hold —
// the fake DeliverOutbound (which owns the HITL hold, persisting the draft
// verbatim) holds when the subject contains HOLD, and must already see the
// rendered content the reviewer will be shown.
func TestSendTemplateHeldShowsRenderedBody(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to":            []string{"alice@x.com"},
		"template_id":   "tmpl_1",
		"template_data": map[string]any{"name": "HOLD reviewer", "plan": map[string]any{"tier": "pro"}},
	})
	if code != 202 || body["status"] != "pending_review" {
		t.Fatalf("want 202 pending_review, got %d %v", code, body)
	}
	req := lastDeliveredReq()
	if req.Subject != "Hello HOLD reviewer" || req.Body != "Hi HOLD reviewer, your plan is pro." {
		t.Errorf("held draft must carry rendered content, got subject=%q body=%q", req.Subject, req.Body)
	}
	if strings.Contains(req.Body, "{{") {
		t.Errorf("held draft still contains template syntax: %q", req.Body)
	}
}

func TestSendTemplateIDAndAliasMutuallyExclusive(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "template_id": "tmpl_1", "template_alias": "welcome",
	})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request, got %d %v", code, body)
	}
}

func TestSendTemplateExclusiveWithLiteralContent(t *testing.T) {
	srv := testServer(t)
	for name, extra := range map[string]map[string]any{
		"subject":   {"subject": "literal"},
		"body":      {"body": "literal"},
		"html_body": {"html_body": "<p>literal</p>"},
	} {
		payload := map[string]any{"to": []string{"alice@x.com"}, "template_id": "tmpl_1"}
		for k, v := range extra {
			payload[k] = v
		}
		code, body := postJSON(t, srv.URL+sendURL, "good", payload)
		if code != 400 || errCode(body) != "invalid_request" {
			t.Fatalf("%s + template_id: want 400 invalid_request, got %d %v", name, code, body)
		}
	}
}

func TestSendTemplateDataWithoutReference(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "subject": "S", "body": "B",
		"template_data": map[string]any{"name": "x"},
	})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request, got %d %v", code, body)
	}
}

func TestSendTemplateNotFound(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "template_id": "tmpl_missing",
	})
	if code != 404 || errCode(body) != "template_not_found" {
		t.Fatalf("want 404 template_not_found, got %d %v", code, body)
	}
	code, body = postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "template_alias": "no-such-alias",
	})
	if code != 404 || errCode(body) != "template_not_found" {
		t.Fatalf("alias: want 404 template_not_found, got %d %v", code, body)
	}
}

func TestSendTemplateRenderFailureBadSource(t *testing.T) {
	srv := testServer(t)
	// tmpl_stale's stored body no longer parses ({{#section}}).
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "template_id": "tmpl_stale",
	})
	if code != 400 || errCode(body) != "template_render_failed" {
		t.Fatalf("want 400 template_render_failed, got %d %v", code, body)
	}
	details, _ := body["error"].(map[string]any)["details"].(map[string]any)
	if details["part"] != "body" {
		t.Fatalf("details must name the failing part, got %v", body)
	}
}

func TestSendTemplateRenderFailureDeepData(t *testing.T) {
	srv := testServer(t)
	// template_data nested past the depth cap fails at render.
	deep := map[string]any{}
	cur := deep
	for i := 0; i < 9; i++ {
		next := map[string]any{}
		cur["k"] = next
		cur = next
	}
	cur["leaf"] = "v"
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"alice@x.com"}, "template_id": "tmpl_1", "template_data": deep,
	})
	if code != 400 || errCode(body) != "template_render_failed" {
		t.Fatalf("want 400 template_render_failed for deep data, got %d %v", code, body)
	}
}

// TestSendTemplateRenderedSubjectStillValidated: rendered values flow through
// the same validateOutboundBody checks as literal ones — data that smuggles
// CR/LF into the subject is rejected exactly like a literal CR/LF subject.
func TestSendTemplateRenderedSubjectStillValidated(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to":            []string{"alice@x.com"},
		"template_id":   "tmpl_1",
		"template_data": map[string]any{"name": "x\r\nInjected: header"},
	})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400 invalid_request for rendered CRLF subject, got %d %v", code, body)
	}
}

// TestReplyRequestHasNoTemplateFields locks the scope decision: template
// references exist on SendEmailRequest only, not reply/forward.
func TestReplyRequestHasNoTemplateFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(ReplyRequest{}),
		reflect.TypeOf(ForwardRequest{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			if strings.HasPrefix(typ.Field(i).Name, "Template") {
				t.Errorf("%s must not carry template fields, found %s", typ.Name(), typ.Field(i).Name)
			}
		}
	}
}

// TestTemplateBetaDocOnSendFields guards the beta-marking convention: every
// template field on the send body carries the templatesBetaDoc sentence.
func TestTemplateBetaDocOnSendFields(t *testing.T) {
	typ := reflect.TypeOf(SendEmailRequest{})
	for _, name := range []string{"TemplateID", "TemplateAlias", "TemplateData"} {
		f, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("SendEmailRequest missing field %s", name)
		}
		if !strings.Contains(f.Tag.Get("doc"), templatesBetaDoc) {
			t.Errorf("%s doc tag must contain the beta sentence %q", name, templatesBetaDoc)
		}
	}
}
