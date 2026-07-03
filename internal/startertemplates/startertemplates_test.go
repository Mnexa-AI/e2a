package startertemplates

import (
	"encoding/json"
	"html"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// The 7 masters, in expected (alias-sorted) order.
var expectedAliases = []string{
	"agent-status",
	"approval-request",
	"daily-digest",
	"password-reset",
	"receipt",
	"verify-code",
	"welcome",
}

// Brand variables every master must declare and use.
var sharedVars = []string{"company_name", "support_email", "company_address", "preheader"}

// Masters that must carry the automated-sender disclosure line.
var disclosureAliases = []string{"agent-status", "daily-digest", "approval-request"}

const disclosureLine = "This is an automated message from {{agent_name}} via {{company_name}}."

// Token syntax mirrors the real engine: flat idents only.
var (
	rawTokenRe = regexp.MustCompile(`\{\{\{([a-zA-Z_][a-zA-Z0-9_.]*)\}\}\}`)
	escTokenRe = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_.]*)\}\}`)
)

// extractTokens returns the escaped ({{var}}) and raw ({{{var}}}) idents used
// in src, and fails the test if any stray {{ / }} remains after removing all
// well-formed tokens (catches typos like {{ var }} or unbalanced braces).
func extractTokens(t *testing.T, where, src string) (esc, raw map[string]bool) {
	t.Helper()
	esc = map[string]bool{}
	raw = map[string]bool{}
	rest := rawTokenRe.ReplaceAllStringFunc(src, func(m string) string {
		raw[rawTokenRe.FindStringSubmatch(m)[1]] = true
		return ""
	})
	rest = escTokenRe.ReplaceAllStringFunc(rest, func(m string) string {
		esc[escTokenRe.FindStringSubmatch(m)[1]] = true
		return ""
	})
	if i := strings.Index(rest, "{{"); i >= 0 {
		lo, hi := i-20, i+20
		if lo < 0 {
			lo = 0
		}
		if hi > len(rest) {
			hi = len(rest)
		}
		t.Errorf("%s: malformed template token near %q", where, rest[lo:hi])
	}
	return esc, raw
}

// renderNaive is a deliberately tiny stand-in for the real engine: flat
// substitution only, HTML-escaping {{var}} when escapeVars is set, inserting
// {{{var}}} verbatim. Missing variables render empty.
func renderNaive(src string, values map[string]string, escapeVars bool) string {
	out := rawTokenRe.ReplaceAllStringFunc(src, func(m string) string {
		return values[rawTokenRe.FindStringSubmatch(m)[1]]
	})
	out = escTokenRe.ReplaceAllStringFunc(out, func(m string) string {
		v := values[escTokenRe.FindStringSubmatch(m)[1]]
		if escapeVars {
			return html.EscapeString(v)
		}
		return v
	})
	return out
}

func exampleValues(m Master) map[string]string {
	vals := make(map[string]string, len(m.Variables))
	for _, v := range m.Variables {
		vals[v.Name] = v.Example
	}
	return vals
}

// Gate 7: Catalog() returns exactly the 7 masters, sorted by alias, and
// Get() round-trips each one.
func TestCatalog(t *testing.T) {
	cat := Catalog()
	if len(cat) != len(expectedAliases) {
		t.Fatalf("Catalog() returned %d masters, want %d", len(cat), len(expectedAliases))
	}
	if !sort.SliceIsSorted(cat, func(i, j int) bool { return cat[i].Alias < cat[j].Alias }) {
		t.Errorf("Catalog() is not sorted by alias")
	}
	for i, want := range expectedAliases {
		if cat[i].Alias != want {
			t.Errorf("Catalog()[%d].Alias = %q, want %q", i, cat[i].Alias, want)
		}
	}
	for _, alias := range expectedAliases {
		m, ok := Get(alias)
		if !ok {
			t.Errorf("Get(%q) not found", alias)
			continue
		}
		if m.Alias != alias {
			t.Errorf("Get(%q).Alias = %q", alias, m.Alias)
		}
	}
	if _, ok := Get("no-such-template"); ok {
		t.Errorf("Get(no-such-template) unexpectedly found")
	}
}

// Gate 1: every masters/<alias>/ directory has a parseable meta.json whose
// alias matches the directory, with version, subject, and >= 1 variable.
func TestMetaParsesAndMatchesDirectory(t *testing.T) {
	entries, err := fs.ReadDir(mastersFS, "masters")
	if err != nil {
		t.Fatalf("reading embedded masters dir: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	if strings.Join(dirs, ",") != strings.Join(expectedAliases, ",") {
		t.Fatalf("masters/ directories = %v, want %v", dirs, expectedAliases)
	}
	versionRe := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	for _, dir := range dirs {
		raw, err := mastersFS.ReadFile("masters/" + dir + "/meta.json")
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		var m Master
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Errorf("%s/meta.json: %v", dir, err)
			continue
		}
		if m.Alias != dir {
			t.Errorf("%s/meta.json: alias %q does not match directory", dir, m.Alias)
		}
		if m.Name == "" || m.Description == "" {
			t.Errorf("%s: name and description must be non-empty", dir)
		}
		if !versionRe.MatchString(m.Version) {
			t.Errorf("%s: version %q is not semver", dir, m.Version)
		}
		if m.Subject == "" {
			t.Errorf("%s: subject must be non-empty", dir)
		}
		if len(m.Variables) < 1 {
			t.Errorf("%s: must declare at least one variable", dir)
		}
	}
}

// Gate 2: declared variables and used tokens must match exactly, raw-ness
// must be consistent, and the shared brand variables must be present.
func TestVariablesDeclaredAndUsed(t *testing.T) {
	for _, m := range Catalog() {
		declared := map[string]Variable{}
		for _, v := range m.Variables {
			if _, dup := declared[v.Name]; dup {
				t.Errorf("%s: variable %q declared twice", m.Alias, v.Name)
			}
			if v.Description == "" || v.Example == "" {
				t.Errorf("%s: variable %q needs a description and an example", m.Alias, v.Name)
			}
			declared[v.Name] = v
		}
		for _, name := range sharedVars {
			if _, ok := declared[name]; !ok {
				t.Errorf("%s: missing shared brand variable %q", m.Alias, name)
			}
		}

		usedEsc := map[string]bool{}
		usedRaw := map[string]bool{}
		for where, src := range map[string]string{"subject": m.Subject, "body.html": m.HTMLBody, "body.txt": m.TextBody} {
			esc, raw := extractTokens(t, m.Alias+"/"+where, src)
			for name := range esc {
				usedEsc[name] = true
			}
			for name := range raw {
				usedRaw[name] = true
			}
			// Raw slots are HTML fragments; they may only appear in the HTML part.
			if where != "body.html" && len(raw) > 0 {
				t.Errorf("%s/%s: raw {{{...}}} tokens are only allowed in body.html, got %v", m.Alias, where, keys(raw))
			}
		}

		for name := range usedEsc {
			v, ok := declared[name]
			if !ok {
				t.Errorf("%s: token {{%s}} is not declared in meta.json", m.Alias, name)
			} else if v.Raw {
				t.Errorf("%s: raw variable %q used as escaped {{%s}}", m.Alias, name, name)
			}
		}
		for name := range usedRaw {
			v, ok := declared[name]
			if !ok {
				t.Errorf("%s: token {{{%s}}} is not declared in meta.json", m.Alias, name)
			} else if !v.Raw {
				t.Errorf("%s: non-raw variable %q used as raw {{{%s}}}", m.Alias, name, name)
			}
		}
		for name := range declared {
			if !usedEsc[name] && !usedRaw[name] {
				t.Errorf("%s: declared variable %q is never used in subject, body.html, or body.txt", m.Alias, name)
			}
		}
	}
}

// Gate 3: no loops, conditionals, partials, or comments — flat tokens only.
func TestNoForbiddenSyntax(t *testing.T) {
	forbidden := []string{"{{#", "{{/", "{{^", "{{>", "{{!"}
	for _, m := range Catalog() {
		for where, src := range map[string]string{"subject": m.Subject, "body.html": m.HTMLBody, "body.txt": m.TextBody} {
			for _, f := range forbidden {
				if strings.Contains(src, f) {
					t.Errorf("%s/%s: contains forbidden syntax %q", m.Alias, where, f)
				}
			}
		}
	}
}

// Gate 4: HTML part structural checks.
func TestHTMLGates(t *testing.T) {
	for _, m := range Catalog() {
		h := m.HTMLBody
		if len(h) >= 30*1024 {
			t.Errorf("%s: body.html is %d bytes, must be < 30KB", m.Alias, len(h))
		}
		for _, want := range []string{
			`<html lang="en">`,
			`<meta name="color-scheme" content="light dark">`,
			`supported-color-schemes`,
			`role="presentation"`,
			`{{preheader}}`,
			`prefers-color-scheme: dark`,
		} {
			if !strings.Contains(h, want) {
				t.Errorf("%s: body.html missing %q", m.Alias, want)
			}
		}
		// The preheader must live in a hidden div at the top of the body.
		if !strings.Contains(h, `display:none;visibility:hidden;max-height:0;max-width:0;overflow:hidden;opacity:0;mso-hide:all;">{{preheader}}</div>`) {
			t.Errorf("%s: preheader div is missing or not hidden", m.Alias)
		}
		// Exactly three marked accent usages, each followed closely by the hex.
		const marker = "<!-- BRAND: accent -->"
		if got := strings.Count(h, marker); got != 3 {
			t.Errorf("%s: found %d %q markers, want exactly 3", m.Alias, got, marker)
		}
		parts := strings.Split(h, marker)
		for i, seg := range parts[1:] {
			window := seg
			if len(window) > 400 {
				window = window[:400]
			}
			if !strings.Contains(window, "#4F46E5") {
				t.Errorf("%s: accent marker %d is not followed by the accent hex #4F46E5", m.Alias, i+1)
			}
		}
		// No insecure or hardcoded external references; all URLs come from
		// variables (mailto:{{support_email}} is the only allowed scheme).
		if strings.Contains(h, "http://") {
			t.Errorf("%s: body.html contains http:// (https only)", m.Alias)
		}
		if strings.Contains(h, "https://") {
			t.Errorf("%s: body.html contains a hardcoded https URL; URLs must come from variables", m.Alias)
		}
		if strings.Contains(strings.ToLower(h), "<script") {
			t.Errorf("%s: body.html contains a script tag", m.Alias)
		}
		if strings.Contains(strings.ToLower(h), "src=") {
			t.Errorf("%s: body.html contains src= (templates must have zero images)", m.Alias)
		}
	}

	// The three agent-sent templates must carry the disclosure line verbatim.
	for _, alias := range disclosureAliases {
		m, ok := Get(alias)
		if !ok {
			t.Fatalf("Get(%q) not found", alias)
		}
		if !strings.Contains(m.HTMLBody, disclosureLine) {
			t.Errorf("%s: body.html missing disclosure line %q", alias, disclosureLine)
		}
		if !strings.Contains(m.TextBody, disclosureLine) {
			t.Errorf("%s: body.txt missing disclosure line %q", alias, disclosureLine)
		}
	}
}

// Gate 5: text part checks — complete emails, hard-wrapped, with footer.
func TestTextGates(t *testing.T) {
	for _, m := range Catalog() {
		txt := m.TextBody
		if strings.TrimSpace(txt) == "" {
			t.Errorf("%s: body.txt is empty", m.Alias)
			continue
		}
		for i, line := range strings.Split(txt, "\n") {
			// Lines carrying variables (labels, URLs, fragments) may exceed
			// the wrap once substituted; only static copy is gated.
			if strings.Contains(line, "{{") {
				continue
			}
			if n := utf8.RuneCountInString(line); n > 70 {
				t.Errorf("%s: body.txt line %d is %d chars (max 70): %q", m.Alias, i+1, n, line)
			}
		}
		if !strings.Contains(txt, "{{company_name}} · {{company_address}}") {
			t.Errorf("%s: body.txt missing footer address line", m.Alias)
		}
		if !strings.Contains(txt, "----------") {
			t.Errorf("%s: body.txt missing the ---------- divider", m.Alias)
		}
		if !strings.Contains(txt, "{{support_email}}") {
			t.Errorf("%s: body.txt missing support email", m.Alias)
		}
	}
}

// Gate 6: hostile render check. Substituting examples must consume every
// token; substituting hostile values into every non-raw slot must never
// produce a <script from an escaped position.
func TestHostileRender(t *testing.T) {
	hostileValue := strings.Repeat("A", 200) +
		`<script>alert(1)</script>` +
		"‮مرحبا" // RTL override + Arabic text

	for _, m := range Catalog() {
		examples := exampleValues(m)

		// Raw examples must themselves be benign, or this gate proves nothing.
		for _, v := range m.Variables {
			if v.Raw && strings.Contains(strings.ToLower(v.Example), "<script") {
				t.Errorf("%s: raw variable %q has a script tag in its example", m.Alias, v.Name)
			}
		}

		// Example render: no tokens may survive substitution.
		for where, out := range map[string]string{
			"subject":   renderNaive(m.Subject, examples, false),
			"body.html": renderNaive(m.HTMLBody, examples, true),
			"body.txt":  renderNaive(m.TextBody, examples, false),
		} {
			if strings.Contains(out, "{{") || strings.Contains(out, "}}") {
				t.Errorf("%s/%s: unresolved template tokens after example render", m.Alias, where)
			}
		}

		// Hostile render: raw slots get their (trusted, pre-rendered)
		// examples; every escaped slot gets the hostile payload.
		hostile := make(map[string]string, len(m.Variables))
		for _, v := range m.Variables {
			if v.Raw {
				hostile[v.Name] = v.Example
			} else {
				hostile[v.Name] = hostileValue
			}
		}
		out := renderNaive(m.HTMLBody, hostile, true)
		if strings.Contains(strings.ToLower(out), "<script") {
			t.Errorf("%s: hostile value in an escaped slot produced a live <script tag", m.Alias)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
