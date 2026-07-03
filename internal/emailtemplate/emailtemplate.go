// Package emailtemplate is the hand-rolled variable-interpolation engine for
// user email templates (beta). It deliberately implements a flat subset of
// Mustache — `{{ident}}` (escaped under EscapeHTML) and `{{{ident}}}` (raw) —
// with NO third-party dependency, and reserves Mustache's structural syntax
// (`{{#…}}`, `{{/…}}`, `{{^…}}`, `{{>…}}`, `{{!…}}`) as a parse error so a
// future upgrade to sections/partials/comments stays purely additive: no
// template that parses today can change meaning later.
//
// The API is two-phase — Parse then Render — so the create/validate endpoints
// can reject bad syntax without any data, and the send path can render one
// parsed part against caller-supplied template_data. Missing variables render
// as the empty string (the permissive Postmark model); a path that resolves to
// an object or array also renders empty.
package emailtemplate

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
)

// EscapeMode selects how `{{ident}}` output is encoded. `{{{ident}}}` is
// always raw.
type EscapeMode int

const (
	// EscapeNone renders both `{{x}}` and `{{{x}}}` verbatim — used for the
	// subject and plain-text body, where HTML entities would be garbage.
	EscapeNone EscapeMode = iota
	// EscapeHTML HTML-escapes `{{x}}` (and leaves `{{{x}}}` raw) — used for
	// html_body so interpolated user data can't inject markup by default.
	EscapeHTML
)

// Caps. Enforced here so every caller (create, validate, send) shares one
// definition; the outbound path re-validates rendered output independently.
const (
	// MaxSourceBytes caps one template part's source at Parse time.
	MaxSourceBytes = 256 * 1024
	// MaxDataDepth caps template_data nesting (maps/arrays) at Render time.
	MaxDataDepth = 8
	// MaxRenderedBytes caps one rendered part at Render time.
	MaxRenderedBytes = 2 * 1024 * 1024
)

// node is one parsed segment: literal text, or a tag with its dot-path ident.
type node struct {
	text  string // literal segment (tag idents live in ident)
	ident string // non-empty ⇒ this node is a tag
	raw   bool   // triple-brace tag ({{{ident}}}): never escaped
}

// Template is a parsed template part, safe for concurrent Render calls.
type Template struct {
	nodes []node
	vars  []string // ordered, deduped idents
}

// ParseError describes a syntax error with its byte offset in the source.
type ParseError struct {
	Offset  int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("template parse error at byte %d: %s", e.Offset, e.Message)
}

// reservedLead are the Mustache structural sigils we reject at parse time so
// a future sections/partials/comments upgrade is additive.
const reservedLead = "#/^>!"

// Parse compiles a template part. It fails on source over MaxSourceBytes,
// unclosed tags, empty tags, invalid identifiers, and reserved (Mustache
// structural) syntax.
func Parse(src string) (*Template, error) {
	if len(src) > MaxSourceBytes {
		return nil, fmt.Errorf("template too large: %d bytes (max %d)", len(src), MaxSourceBytes)
	}
	t := &Template{}
	seen := map[string]bool{}
	pos := 0
	for {
		open := strings.Index(src[pos:], "{{")
		if open < 0 {
			if pos < len(src) {
				t.nodes = append(t.nodes, node{text: src[pos:]})
			}
			return t, nil
		}
		open += pos
		if open > pos {
			t.nodes = append(t.nodes, node{text: src[pos:open]})
		}

		raw := strings.HasPrefix(src[open:], "{{{")
		openLen, closer := 2, "}}"
		if raw {
			openLen, closer = 3, "}}}"
		}
		body := src[open+openLen:]
		end := strings.Index(body, closer)
		if end < 0 {
			return nil, &ParseError{Offset: open, Message: fmt.Sprintf("unclosed %q tag", src[open:open+openLen])}
		}
		ident := strings.TrimSpace(body[:end])
		switch {
		case ident == "":
			return nil, &ParseError{Offset: open, Message: "empty tag"}
		case strings.ContainsRune(reservedLead, rune(ident[0])):
			return nil, &ParseError{Offset: open, Message: fmt.Sprintf(
				"reserved syntax %q: sections/partials/comments are not supported yet", "{{"+string(ident[0])+"…}}")}
		case !validIdent(ident):
			return nil, &ParseError{Offset: open, Message: fmt.Sprintf(
				"invalid identifier %q: expected [a-zA-Z_][a-zA-Z0-9_.]*", ident)}
		}
		t.nodes = append(t.nodes, node{ident: ident, raw: raw})
		if !seen[ident] {
			seen[ident] = true
			t.vars = append(t.vars, ident)
		}
		pos = open + openLen + end + len(closer)
	}
}

// validIdent reports whether s matches [a-zA-Z_][a-zA-Z0-9_.]*.
func validIdent(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && (r >= '0' && r <= '9' || r == '.'):
		default:
			return false
		}
	}
	return len(s) > 0
}

// Vars returns the template's identifiers in first-appearance order, deduped.
// A template with no tags yields nil.
func (t *Template) Vars() []string {
	if len(t.vars) == 0 {
		return nil
	}
	out := make([]string, len(t.vars))
	copy(out, t.vars)
	return out
}

// Render interpolates data into the template. Missing variables (and paths
// resolving to objects/arrays) render as ""; scalars render naturally (see
// formatScalar). It fails when data nests deeper than MaxDataDepth or the
// output exceeds MaxRenderedBytes.
func (t *Template) Render(data map[string]any, escape EscapeMode) (string, error) {
	if depth(data) > MaxDataDepth {
		return "", fmt.Errorf("template_data nests deeper than %d levels", MaxDataDepth)
	}
	var b strings.Builder
	for _, n := range t.nodes {
		s := n.text
		if n.ident != "" {
			s = formatScalar(resolve(data, n.ident))
			if escape == EscapeHTML && !n.raw {
				s = html.EscapeString(s)
			}
		}
		if b.Len()+len(s) > MaxRenderedBytes {
			return "", fmt.Errorf("rendered output exceeds %d bytes", MaxRenderedBytes)
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

// resolve walks a dot path through nested map[string]any. A missing key or a
// non-map intermediate yields nil (rendered as "").
func resolve(data map[string]any, ident string) any {
	var cur any = data
	for _, seg := range strings.Split(ident, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[seg]
		if !ok {
			return nil
		}
	}
	return cur
}

// formatScalar renders a resolved value. Strings pass through; numbers render
// without float artifacts (42.0 → "42", json.Number verbatim); bools render
// true/false; nil, objects and arrays render empty.
func formatScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int:
		return strconv.Itoa(x)
	case int8, int16, int32, int64:
		return fmt.Sprintf("%d", x)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", x)
	case map[string]any, []any:
		// Composite values have no natural scalar rendering — empty, matching
		// the missing-variable behavior.
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

// depth measures map/array nesting: a scalar is 0, {"a": 1} is 1,
// {"a": {"b": 1}} is 2. Arrays count as a level too.
func depth(v any) int {
	switch x := v.(type) {
	case map[string]any:
		max := 0
		for _, e := range x {
			if d := depth(e); d > max {
				max = d
			}
		}
		return max + 1
	case []any:
		max := 0
		for _, e := range x {
			if d := depth(e); d > max {
				max = d
			}
		}
		return max + 1
	default:
		return 0
	}
}
