package inboundpolicy

import "testing"

func TestEvaluateIngestion(t *testing.T) {
	tests := []struct {
		name       string
		policy     string
		allowlist  []string
		sender     string
		resolvable bool
		flagged    bool
	}{
		{"open never flags", Open, nil, "anyone@evil.com", true, false},
		{"unknown policy treated as open", "bogus", nil, "x@y.com", true, false},
		{"allowlist match", Allowlist, []string{"boss@acme.com"}, "boss@acme.com", true, false},
		{"allowlist case-insensitive", Allowlist, []string{"Boss@Acme.com"}, "boss@acme.com", true, false},
		{"allowlist non-match flagged", Allowlist, []string{"boss@acme.com"}, "stranger@x.com", true, true},
		{"allowlist empty flags all", Allowlist, nil, "boss@acme.com", true, true},
		{"domain match", Domain, []string{"acme.com"}, "anyone@acme.com", true, false},
		{"domain non-match flagged", Domain, []string{"acme.com"}, "x@evil.com", true, true},
		{"domain garbage sender flagged", Domain, []string{"acme.com"}, "nodomain", true, true},
		// #299: a sender with no resolvable per-agent identity (shared relay) can
		// never satisfy a gating policy, even if the allowlist names its address
		// or domain. Open still passes — open means open.
		{"allowlist unresolvable flagged despite address match", Allowlist, []string{"agent@send.e2a.dev"}, "agent@send.e2a.dev", false, true},
		{"domain unresolvable flagged despite domain match", Domain, []string{"send.e2a.dev"}, "agent@send.e2a.dev", false, true},
		{"open unresolvable still passes", Open, nil, "agent@send.e2a.dev", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := EvaluateIngestion(tc.policy, tc.allowlist, tc.sender, tc.resolvable)
			if d.Flagged != tc.flagged {
				t.Errorf("Flagged=%v want %v (reason=%q)", d.Flagged, tc.flagged, d.Reason)
			}
			if d.Flagged && d.Reason == "" {
				t.Error("flagged decision must carry a reason")
			}
		})
	}
}
