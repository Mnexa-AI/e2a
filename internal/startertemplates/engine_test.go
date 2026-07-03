package startertemplates_test

// Cross-validation against the REAL template engine (internal/emailtemplate).
//
// The QA gates in startertemplates_test.go use a deliberately naive regex
// substitute for speed and isolation; this file closes the gap between that
// stand-in and the engine the send path actually runs. Every master must
// Parse cleanly and Render without error under the real engine, in the same
// escape modes the send path uses (subject/text: EscapeNone, html: EscapeHTML).
//
// The import direction is test-only: the startertemplates package itself
// stays a stdlib-only leaf; only this external test package pulls in
// emailtemplate.

import (
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/emailtemplate"
	"github.com/Mnexa-AI/e2a/internal/startertemplates"
)

// exampleData builds the render data for a master from each declared
// variable's meta example value — the same values previews and QA use.
func exampleData(m startertemplates.Master) map[string]any {
	data := make(map[string]any, len(m.Variables))
	for _, v := range m.Variables {
		data[v.Name] = v.Example
	}
	return data
}

// TestMastersParseAndRenderWithRealEngine: every master's subject, HTML body
// and text body must parse with emailtemplate.Parse and render without error
// against the variable examples, and every identifier the real tokenizer
// finds must be declared in meta.json (catching any divergence between the
// naive regex extraction and the real parser).
func TestMastersParseAndRenderWithRealEngine(t *testing.T) {
	cat := startertemplates.Catalog()
	if len(cat) == 0 {
		t.Fatal("Catalog() returned no masters")
	}
	for _, m := range cat {
		declared := make(map[string]bool, len(m.Variables))
		for _, v := range m.Variables {
			declared[v.Name] = true
		}
		data := exampleData(m)

		parts := []struct {
			name   string
			src    string
			escape emailtemplate.EscapeMode
		}{
			{"subject", m.Subject, emailtemplate.EscapeNone},
			{"body.txt", m.TextBody, emailtemplate.EscapeNone},
			{"body.html", m.HTMLBody, emailtemplate.EscapeHTML},
		}
		for _, p := range parts {
			tmpl, err := emailtemplate.Parse(p.src)
			if err != nil {
				t.Errorf("%s/%s: real engine failed to parse: %v", m.Alias, p.name, err)
				continue
			}
			for _, ident := range tmpl.Vars() {
				if !declared[ident] {
					t.Errorf("%s/%s: real engine found identifier %q not declared in meta.json", m.Alias, p.name, ident)
				}
			}
			out, err := tmpl.Render(data, p.escape)
			if err != nil {
				t.Errorf("%s/%s: real engine failed to render with example values: %v", m.Alias, p.name, err)
				continue
			}
			if strings.Contains(out, "{{") || strings.Contains(out, "}}") {
				t.Errorf("%s/%s: rendered output still contains template braces", m.Alias, p.name)
			}
			if p.name == "subject" && strings.ContainsAny(out, "\r\n") {
				t.Errorf("%s: rendered subject contains CR/LF — it would fail outbound validation", m.Alias)
			}
		}
	}
}
