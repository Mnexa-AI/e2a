package delivery

import "testing"

func TestMergeMonotonic(t *testing.T) {
	tests := []struct {
		name              string
		current, incoming Status
		want              Status
	}{
		{"queued→sent", StatusQueued, StatusSent, StatusSent},
		{"sent→delivered", StatusSent, StatusDelivered, StatusDelivered},
		{"sent→bounced", StatusSent, StatusBounced, StatusBounced},
		{"delivered→bounced (bounce wins)", StatusDelivered, StatusBounced, StatusBounced},
		{"bounced→complained (complaint wins)", StatusBounced, StatusComplained, StatusComplained},
		// The load-bearing invariant: a late lower-rank event never regresses a terminal status.
		{"complained NOT clobbered by late delivered", StatusComplained, StatusDelivered, StatusComplained},
		{"bounced NOT clobbered by late delivered", StatusBounced, StatusDelivered, StatusBounced},
		{"delivered NOT regressed by late deferred", StatusDelivered, StatusDeferred, StatusDelivered},
		{"deferred→delivered (resolution wins)", StatusDeferred, StatusDelivered, StatusDelivered},
		{"duplicate delivered is idempotent", StatusDelivered, StatusDelivered, StatusDelivered},
		{"empty current accepts any valid", Status(""), StatusSent, StatusSent},
		{"invalid incoming is ignored", StatusDelivered, Status("garbage"), StatusDelivered},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Merge(tc.current, tc.incoming); got != tc.want {
				t.Errorf("Merge(%q,%q) = %q, want %q", tc.current, tc.incoming, got, tc.want)
			}
		})
	}
}

func TestStatusTerminal(t *testing.T) {
	terminal := []Status{StatusDelivered, StatusBounced, StatusComplained, StatusFailed}
	nonTerminal := []Status{StatusQueued, StatusSent, StatusDeferred}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range nonTerminal {
		if s.Terminal() {
			t.Errorf("%q should not be terminal", s)
		}
	}
}
