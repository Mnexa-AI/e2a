package sendramp_test

import (
	"testing"

	"github.com/tokencanopy/e2a/internal/sendramp"
)

func TestScheduleCapForActiveDay(t *testing.T) {
	s := sendramp.NewSchedule(50, 2000, 30)

	for _, tc := range []struct {
		day  int
		want int
	}{
		{day: 0, want: 50},
		{day: 29, want: 2000},
		{day: 99, want: 2000},
	} {
		if got := s.CapForActiveDay(tc.day); got != tc.want {
			t.Errorf("CapForActiveDay(%d) = %d, want %d", tc.day, got, tc.want)
		}
	}
}

func TestNewScheduleEnforcesProductionMinimum(t *testing.T) {
	s := sendramp.NewSchedule(0, -1, 0)
	if s.StartDaily != 50 || s.TargetDaily != 50 || s.RampDays != 1 {
		t.Fatalf("NewSchedule invalid values = %+v, want 50/50/1", s)
	}
}

func TestQualifiesRequiresHalfRoundedUp(t *testing.T) {
	if sendramp.Qualifies(24, 50) || !sendramp.Qualifies(25, 50) ||
		sendramp.Qualifies(25, 51) || !sendramp.Qualifies(26, 51) {
		t.Fatal("qualification threshold is not ceil(limit/2)")
	}
}
