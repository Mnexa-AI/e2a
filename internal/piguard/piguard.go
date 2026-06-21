// Package piguard ("prompt-injection guard") is the content-screening seam for
// inbound and outbound agent email. It answers one question — "is this content an
// attack?" — and is deliberately separate from the identity gates
// (internal/inboundpolicy), the review queue, and the audit log.
//
// It is a stdlib-friendly leaf: it depends only on the standard library and the
// low-level emailauth package (for the parsed inbound auth verdict carried on a
// Request). Detection backends implement Detector; the Engine runs them in parallel
// and normalizes their heterogeneous outputs into a single Result. v1 ships exactly
// one detector — the dependency-free heuristics detector — but external providers
// (Lakera, Bedrock, Model Armor, Prompt Guard, …) plug in behind the same interface
// without reshaping the contract.
//
// See docs/design/2026-06-20-agent-screening-hitl.md §4.2.
package piguard

import (
	"context"
	"math"

	"github.com/Mnexa-AI/e2a/internal/emailauth"
)

// Direction is the screening direction. Inbound screens a received message aimed at
// an agent; Outbound screens what an agent is about to send (exfiltration/leakage).
type Direction int

const (
	DirectionInput Direction = iota
	DirectionOutput
)

func (d Direction) String() string {
	switch d {
	case DirectionInput:
		return "inbound"
	case DirectionOutput:
		return "outbound"
	default:
		return "unknown"
	}
}

// SegmentType identifies the provenance of a piece of extracted content. Splitting
// visible from hidden HTML, and per-part, lets a verdict point at *where* a payload
// hid and lets text-only detectors downcast while structure-aware providers see the
// full breakdown.
type SegmentType string

const (
	SegmentSubject        SegmentType = "subject"
	SegmentTextPlain      SegmentType = "text_plain"
	SegmentHTMLVisible    SegmentType = "html_visible"
	SegmentHTMLHidden     SegmentType = "html_hidden"
	SegmentAttachmentText SegmentType = "attachment_text"
	// SegmentImageOCR is reserved by the contract but never produced in v1 (no OCR).
	SegmentImageOCR SegmentType = "image_ocr"
)

// Segment is one extracted unit of content. Ref is a stable locator (e.g.
// "html#hidden[2]") used for span/offending-segment reporting.
type Segment struct {
	Type    SegmentType
	Content string
	Ref     string
}

// DecodedSignals are the cheap, deterministic, near-zero-false-positive markers the
// extractor computes once. They drive both heuristic scoring and the Engine's
// force-overrides (e.g. Unicode Tags-block present ⇒ floor the action at flag).
type DecodedSignals struct {
	// UnicodeTags reports U+E0000–U+E007F "ASCII smuggling" characters: ASCII
	// mirrored into an invisible Unicode block that LLM tokenizers still read.
	UnicodeTags bool
	// ZeroWidth reports zero-width/invisible separators (U+200B–U+200D, U+2060,
	// U+FEFF) used to hide or fragment instructions.
	ZeroWidth bool
	// HiddenCSSText reports text hidden from humans via CSS (display:none,
	// font-size:0, white-on-white, visibility:hidden, mso-hide:all, off-screen).
	HiddenCSSText bool
	// HomoglyphRatio is the fraction of letters that are non-ASCII confusables
	// (Cyrillic/Greek lookalikes) — a spoofing/obfuscation signal. 0..1.
	HomoglyphRatio float64
	// Unscannable reports a part whose bytes could not be scanned (a genuinely
	// binary attachment, no OCR in v1). Routed to review — no finding != benign.
	Unscannable bool
	// FragmentedURL reports reassembly-style obfuscation ("join 'h','ttp',…").
	FragmentedURL bool
	// PlainHTMLDiverge reports that the text/plain and visible text/html parts carry
	// materially different content (the human reads one, the agent may read the
	// other).
	PlainHTMLDiverge bool
	// Truncated reports that extraction stopped at the scan size cap — content beyond
	// the cap was not inspected, so "no finding" is not a safety guarantee.
	Truncated bool
}

// Request is the normalized input to every Detector. The caller extracts MIME once
// (via Extract) and passes Segments + Signals so each detector — raw-text classifier
// or structure-aware API — works off the same view without re-parsing.
type Request struct {
	Direction Direction
	Segments  []Segment
	Signals   DecodedSignals
	// Sender is the authenticated From (inbound) or the agent identity (outbound).
	Sender string
	// Auth is the parsed inbound auth verdict; nil on outbound.
	Auth *emailauth.Result
	// SizeBytes is the total extracted text size (post-cap).
	SizeBytes int
}

// Status reports whether a detector actually produced a verdict. A non-OK status is
// load-bearing: the Engine excludes it from the aggregate rather than counting it as
// benign, so an outage never silently allows.
type Status int

const (
	StatusOK Status = iota
	StatusTimeout
	StatusError
	StatusUnsupported
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusTimeout:
		return "timeout"
	case StatusError:
		return "error"
	case StatusUnsupported:
		return "unsupported"
	default:
		return "unknown"
	}
}

// Normalized category names, mapped to public taxonomies for portable audit:
// OWASP LLM Top 10, MITRE ATLAS, NIST AI 100-2e2025.
const (
	CategoryInjectionDirect   = "prompt_injection_direct"   // OWASP LLM01 / ATLAS T0051.000
	CategoryInjectionIndirect = "prompt_injection_indirect" // ATLAS T0051.001 / NISTAML.015
	CategoryJailbreak         = "jailbreak"                 // ATLAS T0054
	CategoryExfiltration      = "data_exfiltration"
	CategoryObfuscation       = "obfuscation"
	CategorySensitive         = "sensitive_disclosure" // OWASP LLM02
)

// Category is a normalized detection label. NativeCode preserves the provider's own
// code (e.g. Bedrock "PROMPT_ATTACK", Llama Guard "S1") so policy can route on a
// stable name while audit keeps the source.
type Category struct {
	Name       string  `json:"name"`
	NativeCode string  `json:"native_code,omitempty"`
	Score      float64 `json:"score,omitempty"`
}

// Span is an optional flagged character range. Few providers populate it
// (essentially only Lakera), so it is always optional.
type Span struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text,omitempty"`
	Label string `json:"label,omitempty"`
	Ref   string `json:"ref,omitempty"`
}

// ProviderMeta carries adapter identity and the raw provider response for forensics.
type ProviderMeta struct {
	Name          string         `json:"name"`
	ModelVersion  string         `json:"model_version,omitempty"`
	NativeVerdict string         `json:"native_verdict,omitempty"`
	NativeScore   string         `json:"native_score,omitempty"`
	LatencyMS     int            `json:"latency_ms,omitempty"`
	Raw           map[string]any `json:"raw,omitempty"`
}

// Result is the normalized verdict from a single detector. Flagged and Score are
// required (every adapter derives them, mapping enum/boolean providers onto a 0..1
// score); Categories, Spans, and the richer ProviderMeta fields are optional.
type Result struct {
	Flagged    bool         `json:"flagged"`
	Score      float64      `json:"score"`
	Categories []Category   `json:"categories,omitempty"`
	Spans      []Span       `json:"spans,omitempty"`
	Status     Status       `json:"status"`
	Provider   ProviderMeta `json:"provider"`
}

// Detector is the pluggable screening backend. Implementations must be safe for
// concurrent use (the Engine fans out across them) and must not panic on adversarial
// input — return a StatusError Result instead.
type Detector interface {
	// Inspect screens req and returns a normalized verdict. A returned error and a
	// non-OK Result.Status both signal "no usable verdict"; the Engine treats them
	// identically (excluded from the aggregate).
	//
	// Implementations MUST return promptly when ctx is cancelled or times out. The
	// Engine bounds each call with a timeout, but Go cannot force-cancel a goroutine:
	// a detector that ignores ctx and blocks leaks one goroutine per call. Network
	// backends must pass ctx into their request.
	Inspect(ctx context.Context, req Request) (*Result, error)
	// Name is the stable adapter id, e.g. "heuristics", "lakera".
	Name() string
}

// Action is the screening decision a producer policy emits. Severity orders
// allow < flag < review < block.
type Action string

const (
	ActionAllow  Action = "allow"
	ActionFlag   Action = "flag"
	ActionReview Action = "review"
	ActionBlock  Action = "block"
)

func (a Action) severity() int {
	switch a {
	case ActionAllow:
		return 0
	case ActionFlag:
		return 1
	case ActionReview:
		return 2
	case ActionBlock:
		return 3
	default:
		// Unknown/invalid action → fail closed at review-level, so a malformed or
		// future action value is never silently downgraded to allow.
		return 2
	}
}

// MoreSevere returns whichever action is more severe (block > review > flag > allow).
// Used to combine a gate verdict and a scan verdict into one applied action.
func MoreSevere(a, b Action) Action {
	if b.severity() > a.severity() {
		return b
	}
	return a
}

// ActionForScore maps an aggregate score to an action band using the per-agent
// threshold ladder: below review = allow; [review, block) = review; ≥ block = block.
// Thresholds must satisfy 0 ≤ reviewThreshold ≤ blockThreshold ≤ 1; callers validate
// that upstream (ValidateScanConfig). This is the scan equivalent of SpamAssassin's
// score bands.
func ActionForScore(score, reviewThreshold, blockThreshold float64) Action {
	if math.IsNaN(score) {
		// An unusable score must never read as allow (every `NaN >= x` is false).
		return ActionReview
	}
	switch {
	case score >= blockThreshold:
		return ActionBlock
	case score >= reviewThreshold:
		return ActionReview
	default:
		return ActionAllow
	}
}
