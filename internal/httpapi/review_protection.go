package httpapi

import (
	"encoding/json"

	"github.com/tokencanopy/e2a/internal/identity"
)

// ProtectionFindingView is one screening producer's verdict on a held message —
// the review-detail breakdown behind the coded review_reason. A gate finding
// (source=gate) names the address that tripped a trust policy; a scan finding
// (source=scan) carries the content-detector's categories + one-line rationale.
// Review surface only (GET /v1/reviews/{id}); the agent /messages read paths
// never return holds. Beta: the shape may change.
type ProtectionFindingView struct {
	Source     string               `json:"source" doc:"Which producer flagged the message. Open set; tolerate unknown values. Known values: gate, scan."`
	Action     string               `json:"action,omitempty" doc:"Applied action for this finding. Open set. Known values: flag, review, block."`
	Detector   string               `json:"detector,omitempty" doc:"Content-scan detector(s) that contributed (scan findings only), e.g. \"gemini\"."`
	Score      *float64             `json:"score,omitempty" doc:"Aggregate content-scan score 0..1 (scan findings only)."`
	Categories []ThreatCategoryView `json:"categories,omitempty" doc:"Detected threat categories, highest-confidence first (scan findings only)."`
	// Summary is the detector's short natural-language rationale (e.g. "instructs
	// the agent to wire funds"). Curated from the per-detector verdict — the raw
	// provider payload is never exposed.
	Summary string `json:"summary,omitempty" doc:"Short natural-language rationale for a scan finding (curated; never the raw provider payload)."`
}

// ThreatCategoryView is a single detected threat class + its confidence.
type ThreatCategoryView struct {
	Name  string  `json:"name"`
	Score float64 `json:"score,omitempty"`
}

// protectionFindings projects the stored protection_events audit rows into the
// review-detail view. Categories come straight off the row; the rationale is
// pulled out of the per-detector `raw` breakdown (see rationaleFromRaw) so the
// response carries a curated summary string, not the provider blob.
func protectionFindings(events []identity.ProtectionEvent) []ProtectionFindingView {
	out := make([]ProtectionFindingView, 0, len(events))
	for _, e := range events {
		v := ProtectionFindingView{
			Source:   e.Source,
			Action:   e.Action,
			Detector: e.Detector,
			Score:    e.Score,
		}
		if len(e.Categories) > 0 {
			var cats []struct {
				Name  string  `json:"name"`
				Score float64 `json:"score"`
			}
			if json.Unmarshal(e.Categories, &cats) == nil {
				for _, c := range cats {
					v.Categories = append(v.Categories, ThreatCategoryView{Name: c.Name, Score: c.Score})
				}
			}
		}
		v.Summary = rationaleFromRaw(e.Raw)
		out = append(out, v)
	}
	return out
}

// rationaleFromRaw extracts the detector's short rationale from the per-detector
// `raw` breakdown (a JSON array of piguard results). It prefers the detector
// that actually flagged the message; otherwise the first non-empty verdict. The
// decode struct is intentionally minimal (not the full piguard.Result) so the
// API layer stays decoupled from the detector internals and tolerant of shape
// drift — any parse failure yields an empty summary, never an error.
func rationaleFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var results []struct {
		Flagged  bool `json:"flagged"`
		Provider struct {
			NativeVerdict string `json:"native_verdict"`
		} `json:"provider"`
	}
	if json.Unmarshal(raw, &results) != nil {
		return ""
	}
	fallback := ""
	for _, r := range results {
		if r.Provider.NativeVerdict == "" {
			continue
		}
		if r.Flagged {
			return r.Provider.NativeVerdict
		}
		if fallback == "" {
			fallback = r.Provider.NativeVerdict
		}
	}
	return fallback
}
