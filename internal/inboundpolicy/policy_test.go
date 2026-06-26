package inboundpolicy

import "testing"

func TestEvaluateIngestion(t *testing.T) {
	tests := []struct {
		name          string
		policy        string
		allowlist     []string
		sender        string
		resolvable    bool
		authenticated bool
		requireAuth   bool
		flagged       bool
	}{
		{"open never flags", Open, nil, "anyone@evil.com", true, true, false, false},
		{"unknown policy treated as open", "bogus", nil, "x@y.com", true, true, false, false},
		{"allowlist match", Allowlist, []string{"boss@acme.com"}, "boss@acme.com", true, true, false, false},
		{"allowlist case-insensitive", Allowlist, []string{"Boss@Acme.com"}, "boss@acme.com", true, true, false, false},
		{"allowlist non-match flagged", Allowlist, []string{"boss@acme.com"}, "stranger@x.com", true, true, false, true},
		{"allowlist empty flags all", Allowlist, nil, "boss@acme.com", true, true, false, true},
		{"domain match", Domain, []string{"acme.com"}, "anyone@acme.com", true, true, false, false},
		{"domain non-match flagged", Domain, []string{"acme.com"}, "x@evil.com", true, true, false, true},
		{"domain garbage sender flagged", Domain, []string{"acme.com"}, "nodomain", true, true, false, true},
		// #299: a sender with no resolvable per-agent identity (shared relay) can
		// never satisfy a gating policy, even if the allowlist names its address
		// or domain. Open still passes — open means open.
		{"allowlist unresolvable flagged despite address match", Allowlist, []string{"agent@send.e2a.dev"}, "agent@send.e2a.dev", false, true, false, true},
		{"domain unresolvable flagged despite domain match", Domain, []string{"send.e2a.dev"}, "agent@send.e2a.dev", false, true, false, true},
		{"open unresolvable still passes", Open, nil, "agent@send.e2a.dev", false, true, false, false},
		// #318: require_authenticated is an additive opt-in. When off (default), an
		// unauthenticated allowlisted sender still matches (backward-compatible).
		// When on, an unauthenticated From is flagged regardless of policy, but an
		// authenticated allowlisted sender still passes.
		{"require_auth off: unauthenticated allowlist match passes", Allowlist, []string{"friend@trusted.com"}, "friend@trusted.com", true, false, false, false},
		{"require_auth on: unauthenticated allowlist match flagged", Allowlist, []string{"friend@trusted.com"}, "friend@trusted.com", true, false, true, true},
		{"require_auth on: authenticated allowlist match passes", Allowlist, []string{"friend@trusted.com"}, "friend@trusted.com", true, true, true, false},
		{"require_auth on: unauthenticated under open flagged", Open, nil, "anyone@nowhere.com", true, false, true, true},
		{"require_auth on: authenticated under open passes", Open, nil, "anyone@nowhere.com", true, true, true, false},
		{"require_auth on: domain match but unauthenticated flagged", Domain, []string{"trusted.com"}, "friend@trusted.com", true, false, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := EvaluateIngestion(Request{
				Policy:              tc.policy,
				Allowlist:           tc.allowlist,
				SenderEmail:         tc.sender,
				SenderResolvable:    tc.resolvable,
				SenderAuthenticated: tc.authenticated,
				RequireAuth:         tc.requireAuth,
			})
			if d.Flagged != tc.flagged {
				t.Errorf("Flagged=%v want %v (reason=%q)", d.Flagged, tc.flagged, d.Reason)
			}
			if d.Flagged && d.Reason == "" {
				t.Error("flagged decision must carry a reason")
			}
		})
	}
}
