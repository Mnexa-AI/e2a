package httpapi

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/tokencanopy/e2a/internal/identity"
)

// ProtectionFindingView is one screening producer's verdict on a held message —
// the review-detail breakdown behind hold_reason. A scan finding
// (source=scan) carries the content-detector's categories + one-line rationale;
// a gate finding (source=scan's counterpart, source=gate) carries only its
// source + action today (the address that tripped the policy is intentionally
// not surfaced here — the reviewer already sees sender/recipient on the message).
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
			Score:    validConfidence(e.Score),
		}
		if len(e.Categories) > 0 {
			var cats []struct {
				Name  string  `json:"name"`
				Score float64 `json:"score"`
			}
			if json.Unmarshal(e.Categories, &cats) == nil {
				for _, c := range cats {
					if strings.TrimSpace(c.Name) == "" || !validConfidenceValue(c.Score) {
						continue
					}
					v.Categories = append(v.Categories, ThreatCategoryView{Name: c.Name, Score: c.Score})
				}
			}
		}
		v.Summary = rationaleFromRaw(e.Raw)
		out = append(out, v)
	}
	return out
}

// enrichHoldReason adds scan evidence only when the stored primary reason says
// a scan caused the hold. A secondary scan event must never replace a gate or
// outbound-send explanation.
func enrichHoldReason(reason *HoldReasonView, events []identity.ProtectionEvent) *HoldReasonView {
	if reason == nil || (reason.Code != identity.ReviewReasonInboundScan && reason.Code != identity.ReviewReasonOutboundScan) {
		return reason
	}
	findings := protectionFindings(events)
	for _, finding := range findings {
		if finding.Source != "scan" {
			continue
		}
		if finding.Summary != "" {
			reason.Detail = finding.Summary
		}
		var top *ThreatCategoryView
		for i := range finding.Categories {
			if top == nil || finding.Categories[i].Score > top.Score {
				top = &finding.Categories[i]
			}
		}
		if top != nil {
			reason.Category = top.Name
			reason.Confidence = validConfidence(&top.Score)
		} else {
			reason.Confidence = validConfidence(finding.Score)
		}
		return reason
	}
	return reason
}

func validConfidence(score *float64) *float64 {
	if score == nil || !validConfidenceValue(*score) {
		return nil
	}
	value := *score
	return &value
}

func validConfidenceValue(score float64) bool {
	return !math.IsNaN(score) && !math.IsInf(score, 0) && score >= 0 && score <= 1
}

// rationaleFromRaw extracts the detector's short rationale from the per-detector
// `raw` breakdown (a JSON array of piguard results). Only a successful detector
// that actually flagged the message may supply public rationale. The
// decode struct is intentionally minimal (not the full piguard.Result) so the
// API layer stays decoupled from the detector internals and tolerant of shape
// drift — any parse failure yields an empty summary, never an error.
func rationaleFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var results []struct {
		Status   string `json:"status"`
		Flagged  bool   `json:"flagged"`
		Provider struct {
			NativeVerdict string `json:"native_verdict"`
		} `json:"provider"`
	}
	if json.Unmarshal(raw, &results) != nil {
		return ""
	}
	for _, r := range results {
		if r.Status == "ok" && r.Flagged {
			return strings.TrimSpace(r.Provider.NativeVerdict)
		}
	}
	return ""
}
