package warmup

import (
	"testing"
	"time"
)

func TestDailyCapRampCurve(t *testing.T) {
	s := Schedule{StartDaily: 50, TargetDaily: 2000, RampDays: 30}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		day      int
		wantCap  int
		wantDone bool
	}{
		{0, 50, false},    // day one = start
		{1, 115, false},   // 50 + 1950*1/30 = 115
		{15, 1025, false}, // midpoint
		{29, 1935, false}, // last ramp day, still below target
		{30, 2000, true},  // ramp complete
		{45, 2000, true},  // past ramp, stays at target
	}
	for _, tc := range cases {
		now := start.Add(time.Duration(tc.day)*24*time.Hour + time.Hour) // mid-day
		cap, done := s.DailyCap(start, now)
		if cap != tc.wantCap || done != tc.wantDone {
			t.Errorf("day %d: got cap=%d done=%v, want cap=%d done=%v", tc.day, cap, done, tc.wantCap, tc.wantDone)
		}
	}
}

func TestDailyCapMonotonicNonDescending(t *testing.T) {
	s := Schedule{StartDaily: 20, TargetDaily: 5000, RampDays: 14}
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	prev := 0
	for day := 0; day <= 20; day++ {
		now := start.Add(time.Duration(day) * 24 * time.Hour)
		cap, _ := s.DailyCap(start, now)
		if cap < prev {
			t.Fatalf("day %d cap %d dropped below previous %d — ramp must not descend", day, cap, prev)
		}
		if cap > s.TargetDaily {
			t.Fatalf("day %d cap %d overshot target %d", day, cap, s.TargetDaily)
		}
		prev = cap
	}
}

func TestDailyCapStepsAtUTCMidnight(t *testing.T) {
	// The ramp day index must advance at UTC midnight — the same boundary the
	// per-domain daily counter resets on. Indexing by 24h-from-start instead
	// would let a domain that started mid-day sample two counter days inside
	// one ramp day and send double its cap.
	s := Schedule{StartDaily: 50, TargetDaily: 2000, RampDays: 30}
	start := time.Date(2026, 1, 1, 20, 0, 0, 0, time.UTC) // verified at 20:00 UTC

	// 22:00 same calendar day: still day 0.
	if cap, _ := s.DailyCap(start, start.Add(2*time.Hour)); cap != 50 {
		t.Fatalf("same UTC day: got cap=%d want 50", cap)
	}
	// 00:01 next calendar day (only 4h elapsed): day 1, cap steps with the
	// counter reset.
	if cap, _ := s.DailyCap(start, start.Add(4*time.Hour+time.Minute)); cap != 115 {
		t.Fatalf("after UTC midnight: got cap=%d want 115 (day 1)", cap)
	}
}

func TestDailyCapClockSkewBeforeStart(t *testing.T) {
	s := Schedule{StartDaily: 50, TargetDaily: 2000, RampDays: 30}
	start := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	// now before start (skew): treated as day 0.
	cap, done := s.DailyCap(start, start.Add(-48*time.Hour))
	if cap != 50 || done {
		t.Fatalf("pre-start: got cap=%d done=%v, want 50/false", cap, done)
	}
}

func TestNewScheduleSanitizes(t *testing.T) {
	// start<1 floored, target<start raised, rampDays<1 floored.
	s := NewSchedule(0, 10, 0)
	if s.StartDaily != 1 || s.RampDays != 1 || s.TargetDaily != 10 {
		t.Fatalf("got %+v", s)
	}
	// target below start is raised to start (no descending ramp): a flat
	// ramp that sits at 100 the whole time but is not "done" until the window
	// elapses.
	s = NewSchedule(100, 40, 5)
	if s.TargetDaily != 100 {
		t.Fatalf("target should clamp up to start, got %+v", s)
	}
	start := time.Unix(0, 0).UTC()
	if cap, done := s.DailyCap(start, start); cap != 100 || done {
		t.Fatalf("flat ramp day0: got cap=%d done=%v want 100/false", cap, done)
	}
	if cap, done := s.DailyCap(start, start.Add(5*24*time.Hour)); cap != 100 || !done {
		t.Fatalf("flat ramp day5: got cap=%d done=%v want 100/true", cap, done)
	}
}

func TestZeroValueScheduleDoesNotPanic(t *testing.T) {
	var s Schedule // zero value
	start := time.Unix(0, 0).UTC()
	// Must not divide by zero (RampDays==0 sanitized to 1).
	cap, done := s.DailyCap(start, start)
	if cap < 1 {
		t.Fatalf("zero-value schedule produced cap %d < 1", cap)
	}
	_ = done
}
