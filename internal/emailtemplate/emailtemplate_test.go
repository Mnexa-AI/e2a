package emailtemplate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// mustRender parses src and renders it, failing the test on any error.
func mustRender(t *testing.T, src string, data map[string]any, mode EscapeMode) string {
	t.Helper()
	tmpl, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	out, err := tmpl.Render(data, mode)
	if err != nil {
		t.Fatalf("Render(%q): %v", src, err)
	}
	return out
}

func TestRender_Basics(t *testing.T) {
	tests := []struct {
		name string
		src  string
		data map[string]any
		mode EscapeMode
		want string
	}{
		{"plain text no tags", "hello world", nil, EscapeNone, "hello world"},
		{"simple substitution", "Hi {{name}}!", map[string]any{"name": "Alice"}, EscapeNone, "Hi Alice!"},
		{"whitespace tolerant", "Hi {{  name\t}}!", map[string]any{"name": "Alice"}, EscapeNone, "Hi Alice!"},
		{"raw whitespace tolerant", "Hi {{{ name }}}!", map[string]any{"name": "Alice"}, EscapeNone, "Hi Alice!"},
		{"missing var renders empty", "Hi {{missing}}!", map[string]any{}, EscapeNone, "Hi !"},
		{"missing var nil data", "Hi {{missing}}!", nil, EscapeNone, "Hi !"},
		{"dot path", "{{user.name}}", map[string]any{"user": map[string]any{"name": "Bob"}}, EscapeNone, "Bob"},
		{"deep dot path", "{{a.b.c}}", map[string]any{"a": map[string]any{"b": map[string]any{"c": "deep"}}}, EscapeNone, "deep"},
		{"dot path through non-map", "{{a.b}}", map[string]any{"a": "scalar"}, EscapeNone, ""},
		{"dot path missing leaf", "{{a.b}}", map[string]any{"a": map[string]any{}}, EscapeNone, ""},
		{"object renders empty", "[{{obj}}]", map[string]any{"obj": map[string]any{"k": "v"}}, EscapeNone, "[]"},
		{"array renders empty", "[{{arr}}]", map[string]any{"arr": []any{1, 2}}, EscapeNone, "[]"},
		{"repeated var", "{{x}}{{x}}", map[string]any{"x": "a"}, EscapeNone, "aa"},
		{"adjacent tags", "{{a}}{{b}}", map[string]any{"a": "1", "b": "2"}, EscapeNone, "12"},
		{"underscore ident", "{{_x_1}}", map[string]any{"_x_1": "ok"}, EscapeNone, "ok"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRender(t, tc.src, tc.data, tc.mode); got != tc.want {
				t.Errorf("render = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRender_Escaping(t *testing.T) {
	data := map[string]any{"v": `<b>&"bold"</b>`}
	tests := []struct {
		name string
		src  string
		mode EscapeMode
		want string
	}{
		{"html mode escapes double-brace", "{{v}}", EscapeHTML, "&lt;b&gt;&amp;&#34;bold&#34;&lt;/b&gt;"},
		{"html mode keeps triple-brace raw", "{{{v}}}", EscapeHTML, `<b>&"bold"</b>`},
		{"none mode double-brace plain", "{{v}}", EscapeNone, `<b>&"bold"</b>`},
		{"none mode triple-brace plain", "{{{v}}}", EscapeNone, `<b>&"bold"</b>`},
		{"mixed in one template", "<p>{{v}}|{{{v}}}</p>", EscapeHTML, `<p>&lt;b&gt;&amp;&#34;bold&#34;&lt;/b&gt;|<b>&"bold"</b></p>`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRender(t, tc.src, data, tc.mode); got != tc.want {
				t.Errorf("render = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRender_ScalarFormatting(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"string", "s", "s"},
		{"whole float no artifact", float64(42), "42"},
		{"fractional float", 3.14, "3.14"},
		{"large whole float", float64(1000000), "1000000"},
		{"json.Number integer", json.Number("42"), "42"},
		{"json.Number decimal", json.Number("1.50"), "1.50"},
		{"int", 7, "7"},
		{"int64", int64(-9), "-9"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"nil", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mustRender(t, "{{v}}", map[string]any{"v": tc.val}, EscapeNone)
			if got != tc.want {
				t.Errorf("format(%v) = %q, want %q", tc.val, got, tc.want)
			}
		})
	}
}

// TestRender_JSONDecodedData renders against data that went through a real
// json.Unmarshal — the exact shape the send path sees.
func TestRender_JSONDecodedData(t *testing.T) {
	var data map[string]any
	if err := json.Unmarshal([]byte(`{"name":"Ann","count":3,"price":19.99,"ok":true,"nested":{"city":"Oslo"}}`), &data); err != nil {
		t.Fatal(err)
	}
	got := mustRender(t, "{{name}} bought {{count}} at {{price}} ({{ok}}) in {{nested.city}}", data, EscapeNone)
	want := "Ann bought 3 at 19.99 (true) in Oslo"
	if got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantSub string
	}{
		{"unclosed double", "hi {{name", "unclosed"},
		{"unclosed triple", "hi {{{name}}", "unclosed"},
		{"empty tag", "{{}}", "empty tag"},
		{"whitespace-only tag", "{{   }}", "empty tag"},
		{"reserved section", "{{#each}}", "not supported yet"},
		{"reserved close", "{{/each}}", "not supported yet"},
		{"reserved inverted", "{{^empty}}", "not supported yet"},
		{"reserved partial", "{{>header}}", "not supported yet"},
		{"reserved comment", "{{! note }}", "not supported yet"},
		{"reserved with leading space", "{{ #each }}", "not supported yet"},
		{"digit-leading ident", "{{1abc}}", "invalid identifier"},
		{"space inside ident", "{{a b}}", "invalid identifier"},
		{"dash in ident", "{{a-b}}", "invalid identifier"},
		{"brace as ident (quad open)", "{{{{x}}}}", "invalid identifier"},
		{"oversized source", strings.Repeat("a", MaxSourceBytes+1), "too large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			if err == nil {
				t.Fatalf("Parse(%.40q) succeeded, want error containing %q", tc.src, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestParse_LiteralBraceEdges(t *testing.T) {
	// Sources with brace-ish text that must parse and pass through literally.
	tests := []struct {
		name string
		src  string
		data map[string]any
		want string
	}{
		{"single brace", "a { b } c", nil, "a { b } c"},
		{"lone closers", "a }} b }}} c", nil, "a }} b }}} c"},
		{"tag then trailing brace", "{{x}}}", map[string]any{"x": "v"}, "v}"},
		{"raw tag then trailing brace", "{{{x}}}}", map[string]any{"x": "v"}, "v}"},
		{"brace before tag", "}{{x}}", map[string]any{"x": "v"}, "}v"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRender(t, tc.src, tc.data, EscapeNone); got != tc.want {
				t.Errorf("render = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParse_SourceAtCapOK(t *testing.T) {
	src := strings.Repeat("a", MaxSourceBytes)
	if _, err := Parse(src); err != nil {
		t.Errorf("Parse at exactly MaxSourceBytes: %v", err)
	}
}

func TestRender_DepthCap(t *testing.T) {
	// Build data nested exactly MaxDataDepth deep (allowed) then one more.
	build := func(levels int) map[string]any {
		m := map[string]any{"leaf": "v"}
		for i := 1; i < levels; i++ {
			m = map[string]any{"k": m}
		}
		return m
	}
	tmpl, err := Parse("{{x}}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpl.Render(build(MaxDataDepth), EscapeNone); err != nil {
		t.Errorf("depth == MaxDataDepth must render: %v", err)
	}
	if _, err := tmpl.Render(build(MaxDataDepth+1), EscapeNone); err == nil {
		t.Error("depth == MaxDataDepth+1 must fail")
	}
	// Arrays count as nesting levels too.
	deepViaArrays := map[string]any{"a": []any{[]any{[]any{[]any{[]any{[]any{[]any{[]any{"v"}}}}}}}}}
	if _, err := tmpl.Render(deepViaArrays, EscapeNone); err == nil {
		t.Error("array nesting beyond the cap must fail")
	}
}

func TestRender_OutputCap(t *testing.T) {
	// A small template that multiplies data size past the rendered cap.
	tmpl, err := Parse(strings.Repeat("{{x}}", 8))
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("a", MaxRenderedBytes/4)
	if _, rerr := tmpl.Render(map[string]any{"x": big}, EscapeNone); rerr == nil {
		t.Error("output over MaxRenderedBytes must fail")
	} else if !strings.Contains(rerr.Error(), "rendered output") {
		t.Errorf("cap error = %q, want it to mention rendered output", rerr.Error())
	}
	// Under the cap renders fine.
	small, err := tmpl.Render(map[string]any{"x": "abc"}, EscapeNone)
	if err != nil || small != strings.Repeat("abc", 8) {
		t.Errorf("under-cap render = %q, %v", small, err)
	}
}

func TestVars(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"ordered", "{{b}} {{a}}", []string{"b", "a"}},
		{"deduped", "{{a}} {{b}} {{a}}", []string{"a", "b"}},
		{"raw and escaped dedupe together", "{{a}} {{{a}}}", []string{"a"}},
		{"dot paths kept whole", "{{user.name}} {{user.email}}", []string{"user.name", "user.email"}},
		{"no tags", "plain", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.src, err)
			}
			if got := tmpl.Vars(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Vars() of %q = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

func TestTemplateVarsCopyIsIsolated(t *testing.T) {
	tmpl, err := Parse("{{a}} {{b}}")
	if err != nil {
		t.Fatal(err)
	}
	v := tmpl.Vars()
	v[0] = "mutated"
	if got := tmpl.Vars(); got[0] != "a" {
		t.Errorf("Vars() must return a copy; internal state mutated to %v", got)
	}
}
