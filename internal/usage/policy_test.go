package usage_test

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/usage"
)

func TestPolicyFor(t *testing.T) {
	cases := []struct {
		class      usage.AccountClass
		wantMeter  bool
		wantBill   bool
		wantAnalyt bool
	}{
		{usage.ClassStandard, true, true, true},
		{usage.ClassInternal, false, false, false},
		{usage.ClassSystem, false, false, false},
		{usage.ClassDemo, false, false, false},
		// Unknown / empty fails closed to standard (meter) — a misclassified
		// account must never be silently exempted from billing.
		{usage.AccountClass(""), true, true, true},
		{usage.AccountClass("bogus"), true, true, true},
	}
	for _, c := range cases {
		got := usage.PolicyFor(c.class)
		if got.Meter != c.wantMeter || got.Bill != c.wantBill || got.Analytics != c.wantAnalyt {
			t.Errorf("PolicyFor(%q) = %+v, want meter=%v bill=%v analytics=%v",
				c.class, got, c.wantMeter, c.wantBill, c.wantAnalyt)
		}
	}
}
