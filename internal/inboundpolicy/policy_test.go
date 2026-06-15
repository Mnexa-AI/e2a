package inboundpolicy

import "testing"

func TestEvaluateIngestion(t *testing.T) {
	tests := []struct {
		name      string
		policy    string
		allowlist []string
		sender    string
		dmarcPass bool
		flagged   bool
	}{
		{"open never flags", Open, nil, "anyone@evil.com", false, false},
		{"unknown policy treated as open", "bogus", nil, "x@y.com", false, false},
		{"allowlist match", Allowlist, []string{"boss@acme.com"}, "boss@acme.com", false, false},
		{"allowlist case-insensitive", Allowlist, []string{"Boss@Acme.com"}, "boss@acme.com", false, false},
		{"allowlist non-match flagged", Allowlist, []string{"boss@acme.com"}, "stranger@x.com", true, true},
		{"allowlist empty flags all", Allowlist, nil, "boss@acme.com", false, true},
		{"domain match", Domain, []string{"acme.com"}, "anyone@acme.com", false, false},
		{"domain non-match flagged", Domain, []string{"acme.com"}, "x@evil.com", false, true},
		{"domain garbage sender flagged", Domain, []string{"acme.com"}, "nodomain", false, true},
		{"verified_only dmarc pass", VerifiedOnly, nil, "x@y.com", true, false},
		{"verified_only dmarc fail flagged", VerifiedOnly, nil, "x@y.com", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := EvaluateIngestion(tc.policy, tc.allowlist, tc.sender, tc.dmarcPass)
			if d.Flagged != tc.flagged {
				t.Errorf("Flagged=%v want %v (reason=%q)", d.Flagged, tc.flagged, d.Reason)
			}
			if d.Flagged && d.Reason == "" {
				t.Error("flagged decision must carry a reason")
			}
		})
	}
}
