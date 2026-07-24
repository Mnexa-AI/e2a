package sendramp_test

import (
	"testing"

	"github.com/tokencanopy/e2a/internal/sendramp"
)

func TestCapForActiveDaySingleDayRampReachesTargetImmediately(t *testing.T) {
	s := sendramp.NewSchedule(50, 2000, 1)
	if got := s.CapForActiveDay(0); got != 2000 {
		t.Fatalf("CapForActiveDay(0) = %d, want target 2000 on a one-day ramp", got)
	}
}

func TestCapForActiveDayInterpolatesLinearlyBetweenStartAndTarget(t *testing.T) {
	s := sendramp.NewSchedule(50, 150, 3)
	if got := s.CapForActiveDay(1); got != 100 {
		t.Fatalf("CapForActiveDay(1) = %d, want midpoint 100", got)
	}
}

func TestCapForActiveDayNegativeIndexBehavesAsDayZero(t *testing.T) {
	s := sendramp.NewSchedule(50, 150, 3)
	if got := s.CapForActiveDay(-2); got != 50 {
		t.Fatalf("CapForActiveDay(-2) = %d, want start 50", got)
	}
	if got := sendramp.NewSchedule(50, 2000, 1).CapForActiveDay(-2); got != 2000 {
		t.Fatalf("one-day ramp CapForActiveDay(-2) = %d, want target 2000", got)
	}
}
