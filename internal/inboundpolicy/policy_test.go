package inboundpolicy

import "testing"

func TestEvaluateIngestion(t *testing.T) {
	tests := []struct {
		name      string
		policy    string
		allowlist []string
		sender    string
		flagged   bool
	}{
		{"open never flags", Open, nil, "anyone@evil.com", false},
		{"unknown policy treated as open", "bogus", nil, "x@y.com", false},
		{"allowlist match", Allowlist, []string{"boss@acme.com"}, "boss@acme.com", false},
		{"allowlist case-insensitive", Allowlist, []string{"Boss@Acme.com"}, "boss@acme.com", false},
		{"allowlist non-match flagged", Allowlist, []string{"boss@acme.com"}, "stranger@x.com", true},
		{"allowlist empty flags all", Allowlist, nil, "boss@acme.com", true},
		{"domain match", Domain, []string{"acme.com"}, "anyone@acme.com", false},
		{"domain non-match flagged", Domain, []string{"acme.com"}, "x@evil.com", true},
		{"domain garbage sender flagged", Domain, []string{"acme.com"}, "nodomain", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := EvaluateIngestion(tc.policy, tc.allowlist, tc.sender)
			if d.Flagged != tc.flagged {
				t.Errorf("Flagged=%v want %v (reason=%q)", d.Flagged, tc.flagged, d.Reason)
			}
			if d.Flagged && d.Reason == "" {
				t.Error("flagged decision must carry a reason")
			}
		})
	}
}
