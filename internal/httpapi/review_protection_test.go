package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
)

func f64p(v float64) *float64 { return &v }

// A scan finding projects its categories (highest-confidence first) and pulls
// the flagged detector's rationale out of the per-detector raw breakdown — the
// two halves of "prompt-injection: instructs the agent to wire funds".
func TestProtectionFindings_ScanCategoriesAndRationale(t *testing.T) {
	raw := json.RawMessage(`[
		{"flagged":false,"provider":{"native_verdict":"looks benign"}},
		{"flagged":true,"provider":{"native_verdict":"instructs the agent to wire funds"}}
	]`)
	events := []identity.ProtectionEvent{{
		Source:     "scan",
		Action:     "review",
		Detector:   "gemini",
		Score:      f64p(0.92),
		Categories: json.RawMessage(`[{"name":"prompt-injection","score":0.92},{"name":"jailbreak","score":0.4}]`),
		Raw:        raw,
	}}

	got := protectionFindings(events)
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got))
	}
	f := got[0]
	if f.Source != "scan" || f.Detector != "gemini" {
		t.Errorf("source/detector = %q/%q", f.Source, f.Detector)
	}
	if f.Score == nil || *f.Score != 0.92 {
		t.Errorf("score = %v, want 0.92", f.Score)
	}
	if len(f.Categories) != 2 || f.Categories[0].Name != "prompt-injection" || f.Categories[0].Score != 0.92 {
		t.Errorf("categories = %+v", f.Categories)
	}
	// Prefers the FLAGGED detector's rationale over the earlier benign one.
	if f.Summary != "instructs the agent to wire funds" {
		t.Errorf("summary = %q", f.Summary)
	}
}

// A gate finding has no scan detail: no categories, no rationale, just the
// producer + action.
func TestProtectionFindings_GateHasNoScanDetail(t *testing.T) {
	got := protectionFindings([]identity.ProtectionEvent{{
		Source: "gate", Action: "review", SubjectAddr: "evil@x.com",
	}})
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].Source != "gate" || len(got[0].Categories) != 0 || got[0].Summary != "" {
		t.Errorf("gate finding leaked scan detail: %+v", got[0])
	}
}

func TestRationaleFromRaw(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"no flagged → first non-empty", `[{"flagged":false,"provider":{"native_verdict":"first"}},{"flagged":false,"provider":{"native_verdict":"second"}}]`, "first"},
		{"flagged wins over earlier non-flagged", `[{"flagged":false,"provider":{"native_verdict":"benign"}},{"flagged":true,"provider":{"native_verdict":"the real threat"}}]`, "the real threat"},
		{"all empty verdicts", `[{"flagged":true,"provider":{}}]`, ""},
		{"malformed json → empty, no panic", `{not json`, ""},
	}
	for _, c := range cases {
		if got := rationaleFromRaw(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("%s: rationaleFromRaw = %q, want %q", c.name, got, c.want)
		}
	}
}

// Malformed categories JSON must not panic or error — it yields no categories.
func TestProtectionFindings_MalformedCategoriesIsSafe(t *testing.T) {
	got := protectionFindings([]identity.ProtectionEvent{{
		Source: "scan", Categories: json.RawMessage(`{bad`), Raw: json.RawMessage(`also bad`),
	}})
	if len(got) != 1 || len(got[0].Categories) != 0 || got[0].Summary != "" {
		t.Errorf("malformed input not handled safely: %+v", got)
	}
}
