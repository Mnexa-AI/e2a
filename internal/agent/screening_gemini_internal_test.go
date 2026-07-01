package agent

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/piguard"
)

// TestBuildAgentScreenEngine_HeuristicsOnly guards against re-wiring GeminiDetector
// into outbound screening. Gemini's prompt classifies content aimed at the AI
// agent (inbound-shaped) and never checks Request.Direction, so on outbound mail
// it would false-positive on agents quoting injection-like text while missing the
// actual outbound concern (egress/exfiltration) — see buildAgentScreenEngine's doc
// comment and the design doc §4.2. Even with a Gemini API key configured, the
// outbound engine must run heuristics only.
func TestBuildAgentScreenEngine_HeuristicsOnly(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key-not-a-real-credential")

	engine := buildAgentScreenEngine()

	agg := engine.Evaluate(context.Background(), piguard.Request{
		Direction: piguard.DirectionOutput,
		Segments: []piguard.Segment{
			{Type: piguard.SegmentTextPlain, Content: "routine outbound reply"},
		},
	})

	if len(agg.PerDetector) != 1 {
		t.Fatalf("PerDetector has %d entries, want 1 (heuristics only): %+v", len(agg.PerDetector), agg.PerDetector)
	}
	if got := agg.PerDetector[0].Provider.Name; got != "heuristics" {
		t.Errorf("only detector = %q, want %q (gemini must not be wired into outbound screening)", got, "heuristics")
	}
}
