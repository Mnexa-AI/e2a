package piguard

import (
	"context"
	"math"
	"regexp"
	"strings"
)

// HeuristicsDetector is the built-in, dependency-free detector shipped in v1. It is
// deterministic, requires no network or model, and is tuned for near-zero false
// positives: it leans on the high-confidence DecodedSignals (which the extractor
// already computed) plus a small set of well-known injection / exfiltration content
// patterns. It is the reference Detector implementation and the baseline that
// external providers augment.
type HeuristicsDetector struct{}

// NewHeuristicsDetector returns the built-in detector.
func NewHeuristicsDetector() *HeuristicsDetector { return &HeuristicsDetector{} }

func (h *HeuristicsDetector) Name() string { return "heuristics" }

// signalContribution is the per-signal weight + category attribution. Weights are
// the detector's own confidence that the signal indicates an attack, combined via
// noisy-OR so multiple weak signals reinforce without exceeding 1.0.
type signalContribution struct {
	score      float64
	categories []string
}

// Inspect screens req using DecodedSignals and content patterns. It never returns a
// non-nil error (it cannot fail on adversarial input) and always reports StatusOK.
func (h *HeuristicsDetector) Inspect(_ context.Context, req Request) (*Result, error) {
	catScores := map[string]float64{}
	var contributions []float64

	add := func(score float64, cats ...string) {
		if score <= 0 {
			return
		}
		contributions = append(contributions, score)
		for _, c := range cats {
			if score > catScores[c] {
				catScores[c] = score
			}
		}
	}

	s := req.Signals
	if s.UnicodeTags {
		add(0.9, CategoryObfuscation, CategoryInjectionIndirect)
	}
	if s.HiddenCSSText {
		add(0.75, CategoryObfuscation, CategoryInjectionIndirect)
	}
	if s.ZeroWidth {
		add(0.5, CategoryObfuscation)
	}
	if s.FragmentedURL {
		add(0.5, CategoryObfuscation, CategoryExfiltration)
	}
	if s.HomoglyphRatio > 0.15 {
		add(min(0.6, s.HomoglyphRatio*2), CategoryObfuscation)
	}
	if s.PlainHTMLDiverge {
		add(0.4, CategoryObfuscation)
	}

	// Content pattern scan over EVERY segment, including hidden HTML and attachment
	// text — the hidden parts are exactly where injection payloads live.
	var combined strings.Builder
	for _, seg := range req.Segments {
		combined.WriteString(seg.Content)
		combined.WriteByte('\n')
	}
	body := combined.String()
	// Run the injection lexicon over a confusable-folded copy so a single homoglyph
	// (e.g. Cyrillic 'і' in "іgnore") can't slip the phrase past an ASCII-only regex.
	folded := foldConfusables(body)

	if injectionPhraseRe.MatchString(folded) {
		add(0.7, CategoryInjectionDirect)
	}
	if systemPromptRe.MatchString(folded) {
		add(0.6, CategoryInjectionDirect)
	}

	// Outbound-specific: exfiltration / sensitive-disclosure egress signatures.
	if req.Direction == DirectionOutput {
		if secretRe.MatchString(body) {
			add(0.7, CategorySensitive, CategoryExfiltration)
		}
		if egressImageRe.MatchString(body) || dataURLRe.MatchString(body) {
			add(0.5, CategoryExfiltration)
		}
	}

	score := noisyOR(contributions)
	categories := make([]Category, 0, len(catScores))
	for name, sc := range catScores {
		categories = append(categories, Category{Name: name, Score: sc})
	}
	sortCategories(categories)

	return &Result{
		Flagged:    len(categories) > 0,
		Score:      score,
		Categories: categories,
		Status:     StatusOK,
		Provider: ProviderMeta{
			Name:         h.Name(),
			ModelVersion: "heuristics-v1",
		},
	}, nil
}

// noisyOR combines independent probabilities: 1 - Π(1 - pᵢ). Order-independent and
// bounded in [0,1); two 0.7 signals → 0.91, not 1.4. NaN contributions are skipped
// so a stray NaN can never poison the result into NaN.
func noisyOR(ps []float64) float64 {
	prod := 1.0
	for _, p := range ps {
		if math.IsNaN(p) {
			continue
		}
		if p < 0 {
			p = 0
		}
		if p > 1 {
			p = 1
		}
		prod *= (1 - p)
	}
	return 1 - prod
}

// confusableFold maps the common Latin-lookalike homoglyphs (Cyrillic, Greek) to
// their ASCII counterparts. Used to normalize content before running the ASCII
// injection lexicon so a one-character homoglyph swap doesn't defeat detection.
var confusableFold = map[rune]rune{
	// Cyrillic lowercase
	'а': 'a', 'е': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'у': 'y', 'х': 'x',
	'і': 'i', 'ѕ': 's', 'ј': 'j', 'һ': 'h', 'ԁ': 'd', 'ո': 'n', 'м': 'm',
	'т': 't', 'в': 'b', 'к': 'k', 'г': 'r', 'п': 'n',
	// Cyrillic uppercase
	'А': 'A', 'В': 'B', 'Е': 'E', 'К': 'K', 'М': 'M', 'Н': 'H', 'О': 'O',
	'Р': 'P', 'С': 'C', 'Т': 'T', 'Х': 'X', 'І': 'I',
	// Greek
	'ο': 'o', 'α': 'a', 'ε': 'e', 'ρ': 'p', 'ι': 'i', 'ν': 'v', 'τ': 't',
	'υ': 'u', 'Ο': 'O', 'Α': 'A', 'Ε': 'E', 'Ρ': 'P', 'Τ': 'T', 'Χ': 'X',
}

func foldConfusables(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if f, ok := confusableFold[r]; ok {
			b.WriteRune(f)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sortCategories orders by descending score then name, so output is deterministic.
func sortCategories(cats []Category) {
	for i := 1; i < len(cats); i++ {
		for j := i; j > 0; j-- {
			a, b := cats[j-1], cats[j]
			if b.Score > a.Score || (b.Score == a.Score && b.Name < a.Name) {
				cats[j-1], cats[j] = cats[j], cats[j-1]
			} else {
				break
			}
		}
	}
}

var (
	// Direct-injection imperative phrases ("ignore previous instructions" family).
	injectionPhraseRe = regexp.MustCompile(`(?is)\b(ignore|disregard|forget|override)\b[^.!?\n]{0,40}\b(previous|prior|above|earlier|all|the)\b[^.!?\n]{0,30}\b(instruction|instructions|prompt|prompts|rule|rules|context|message|directive|directives)\b`)
	// References to the system prompt / role reassignment.
	systemPromptRe = regexp.MustCompile(`(?is)\b(system\s*prompt|you\s+are\s+now|new\s+instructions?\s*:|reveal\s+your|act\s+as\s+(an?\s+)?|developer\s+mode|do\s+anything\s+now)\b`)
	// Secret/credential material (API keys, AWS keys, private keys, password assignments).
	secretRe = regexp.MustCompile(`(?is)(sk-[a-z0-9]{16,}|AKIA[0-9A-Z]{12,}|-----BEGIN\s+[A-Z ]*PRIVATE KEY-----|\b(password|passwd|secret|api[_-]?key|token)\s*[:=]\s*\S{6,})`)
	// Markdown-image exfiltration: ![..](http...) — a classic auto-fetch egress channel.
	egressImageRe = regexp.MustCompile(`(?is)!\[[^\]]*\]\(\s*https?://`)
	// data: URLs used to smuggle payloads outbound.
	dataURLRe = regexp.MustCompile(`(?is)\bdata:[a-z]+/[a-z0-9.+-]+;base64,`)
)
