package httpapi

import (
	"sort"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/startertemplates"
)

// GET /v1/starter-templates — the read-only embedded catalog (beta).

func TestListStarterTemplates(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/starter-templates", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != len(startertemplates.Catalog()) {
		t.Fatalf("want %d starters, got %d", len(startertemplates.Catalog()), len(items))
	}
	if body["next_cursor"] != nil {
		t.Fatalf("expected null next_cursor on single page, got %v", body["next_cursor"])
	}
	aliases := make([]string, 0, len(items))
	for _, it := range items {
		m := it.(map[string]any)
		aliases = append(aliases, m["alias"].(string))
		// Catalog metadata must be present; the large body sources must NOT be.
		for _, k := range []string{"name", "description", "version", "subject", "variables"} {
			if _, ok := m[k]; !ok {
				t.Errorf("list item %q missing key %q", m["alias"], k)
			}
		}
		for _, k := range []string{"body", "html_body"} {
			if _, ok := m[k]; ok {
				t.Errorf("list item %q must not include %q (sources are detail-only)", m["alias"], k)
			}
		}
	}
	if !sort.StringsAreSorted(aliases) {
		t.Fatalf("starters must be sorted by alias, got %v", aliases)
	}
}

func TestGetStarterTemplate(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/starter-templates/welcome", "good")
	if code != 200 || body["alias"] != "welcome" {
		t.Fatalf("want 200 welcome, got %d %v", code, body)
	}
	master, ok := startertemplates.Get("welcome")
	if !ok {
		t.Fatal("welcome master missing from catalog")
	}
	// The detail view carries the FULL body sources, verbatim.
	if body["subject"] != master.Subject {
		t.Errorf("subject = %v, want master subject %q", body["subject"], master.Subject)
	}
	if body["body"] != master.TextBody {
		t.Errorf("body does not match master text source")
	}
	if body["html_body"] != master.HTMLBody {
		t.Errorf("html_body does not match master HTML source")
	}
	vars, _ := body["variables"].([]any)
	if len(vars) != len(master.Variables) {
		t.Fatalf("want %d variables, got %d", len(master.Variables), len(vars))
	}
	v0 := vars[0].(map[string]any)
	for _, k := range []string{"name", "required", "raw", "description", "example"} {
		if _, ok := v0[k]; !ok {
			t.Errorf("variable view missing key %q: %v", k, v0)
		}
	}
}

func TestGetStarterTemplateNotFound(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/starter-templates/no-such-starter", "good")
	if code != 404 || errCode(body) != "starter_template_not_found" {
		t.Fatalf("want 404 starter_template_not_found, got %d %v", code, body)
	}
}

func TestStarterTemplatesUnauthorized(t *testing.T) {
	srv := testServer(t)
	if code, _ := getJSON(t, srv.URL+"/v1/starter-templates", ""); code != 401 {
		t.Fatalf("list: want 401, got %d", code)
	}
	if code, _ := getJSON(t, srv.URL+"/v1/starter-templates/welcome", ""); code != 401 {
		t.Fatalf("get: want 401, got %d", code)
	}
}
