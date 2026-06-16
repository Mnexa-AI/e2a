package actiongate

import "testing"

func TestEvaluate_AllModeHoldsEverything(t *testing.T) {
	// all mode holds regardless of trust signals.
	for _, tc := range []struct{ ref, untrusted, impact bool }{
		{false, false, false}, {true, false, false}, {true, true, true},
	} {
		if d := Evaluate(ModeAll, tc.ref, tc.untrusted, tc.impact); !d.Hold {
			t.Errorf("all mode must hold (%+v)", tc)
		}
	}
}

func TestEvaluate_HighImpactMode(t *testing.T) {
	cases := []struct {
		name                         string
		ref, untrusted, impact, hold bool
	}{
		{"untrusted + high-impact → hold", true, true, true, true},
		{"trusted input → send", true, false, true, false},
		{"low-impact (in-thread) → send", true, true, false, false},
		{"no referenced input (cold send) → send", false, true, true, false},
		{"trusted + low-impact → send", true, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Evaluate(ModeHighImpact, c.ref, c.untrusted, c.impact)
			if d.Hold != c.hold {
				t.Errorf("Hold = %v, want %v", d.Hold, c.hold)
			}
		})
	}
}

// TestEvaluate_UnknownModeFailsClosed: a garbage mode holds (never silently
// sends when the agent opted into HITL).
func TestEvaluate_UnknownModeFailsClosed(t *testing.T) {
	if d := Evaluate("bogus", true, false, false); !d.Hold {
		t.Error("unknown mode must fail closed to holding")
	}
}

func TestHighImpact(t *testing.T) {
	participants := []string{"boss@acme.com", "support@acme.com", "cc@acme.com"}
	cases := []struct {
		name       string
		recipients []string
		want       bool
	}{
		{"reply within participant domains", []string{"boss@acme.com"}, false},
		{"reply to new participant same domain", []string{"newperson@acme.com"}, false},
		{"reply adds external domain", []string{"boss@acme.com", "outsider@evil.com"}, true},
		{"forward to third party", []string{"legal@external.com"}, true},
		{"case-insensitive domain match", []string{"boss@ACME.com"}, false},
		{"garbage recipient fails closed", []string{"not-an-email"}, true},
		{"empty recipients", []string{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HighImpact(participants, c.recipients); got != c.want {
				t.Errorf("HighImpact(%v) = %v, want %v", c.recipients, got, c.want)
			}
		})
	}
}

func TestValid(t *testing.T) {
	if !Valid(ModeAll) || !Valid(ModeHighImpact) || Valid("nope") || Valid("") {
		t.Error("Valid mismatch")
	}
}
