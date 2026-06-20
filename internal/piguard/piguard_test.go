package piguard

import "testing"

func TestActionForScore(t *testing.T) {
	const review, block = 0.5, 0.9
	tests := []struct {
		score float64
		want  Action
	}{
		{0.0, ActionAllow},
		{0.49, ActionAllow},
		{0.5, ActionReview}, // inclusive lower bound of review band
		{0.89, ActionReview},
		{0.9, ActionBlock}, // inclusive lower bound of block band
		{1.0, ActionBlock},
	}
	for _, tt := range tests {
		if got := ActionForScore(tt.score, review, block); got != tt.want {
			t.Errorf("ActionForScore(%v) = %v, want %v", tt.score, got, tt.want)
		}
	}
}

func TestMoreSevere(t *testing.T) {
	tests := []struct {
		a, b, want Action
	}{
		{ActionAllow, ActionFlag, ActionFlag},
		{ActionFlag, ActionReview, ActionReview},
		{ActionReview, ActionBlock, ActionBlock},
		{ActionBlock, ActionReview, ActionBlock},
		{ActionReview, ActionAllow, ActionReview},
		{ActionAllow, ActionAllow, ActionAllow},
	}
	for _, tt := range tests {
		if got := MoreSevere(tt.a, tt.b); got != tt.want {
			t.Errorf("MoreSevere(%v,%v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestDirectionString(t *testing.T) {
	if DirectionInput.String() != "inbound" || DirectionOutput.String() != "outbound" {
		t.Errorf("direction strings: in=%q out=%q", DirectionInput, DirectionOutput)
	}
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{StatusOK: "ok", StatusTimeout: "timeout", StatusError: "error", StatusUnsupported: "unsupported"}
	for s, want := range cases {
		if s.String() != want {
			t.Errorf("Status(%d).String() = %q, want %q", s, s.String(), want)
		}
	}
}
