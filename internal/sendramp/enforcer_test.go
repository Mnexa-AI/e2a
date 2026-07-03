package sendramp

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeReader struct {
	status  string
	started *time.Time
	err     error
}

func (f fakeReader) GetSendingRampState(context.Context, string) (string, *time.Time, error) {
	return f.status, f.started, f.err
}

// fakeReserver models the atomic counter: it holds the day's running count and
// applies the increment-if-below-cap contract in-memory.
type fakeReserver struct {
	sent    int
	err     error
	calls   int
	lastDay time.Time
	lastCap int
}

func (f *fakeReserver) ReserveDomainSend(_ context.Context, _ string, day time.Time, cap int) (bool, int, error) {
	f.calls++
	f.lastDay = day
	f.lastCap = cap
	if f.err != nil {
		return false, 0, f.err
	}
	if f.sent >= cap {
		return false, f.sent, nil
	}
	f.sent++
	return true, f.sent, nil
}

func newTestEnforcer(r StateReader, c DailyReserver, now time.Time) *Enforcer {
	e := NewEnforcer(r, c, Schedule{StartDaily: 50, TargetDaily: 2000, RampDays: 30})
	e.now = func() time.Time { return now }
	e.logf = func(string, ...any) {} // quiet fail-open logging in tests
	return e
}

func TestReserveNoopWhenNotActive(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	started := now.Add(-time.Hour)
	for _, status := range []string{StatusInactive, StatusPaused, "", "unknown"} {
		c := &fakeReserver{sent: 100000}
		e := newTestEnforcer(fakeReader{status: status, started: &started}, c, now)
		if err := e.Reserve(context.Background(), "acme.test"); err != nil {
			t.Fatalf("status %q: expected allow, got %v", status, err)
		}
		if c.calls != 0 {
			t.Fatalf("status %q: inactive domain must not touch the counter", status)
		}
	}
}

func TestReserveNoopWhenStartedAtNil(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e := newTestEnforcer(fakeReader{status: StatusActive, started: nil}, &fakeReserver{sent: 999}, now)
	if err := e.Reserve(context.Background(), "acme.test"); err != nil {
		t.Fatalf("nil started_at should allow, got %v", err)
	}
}

func TestReserveAllowsUnderCap(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(12 * time.Hour) // day 0, cap = 50
	c := &fakeReserver{sent: 49}
	e := newTestEnforcer(fakeReader{status: StatusActive, started: &start}, c, now)
	if err := e.Reserve(context.Background(), "acme.test"); err != nil {
		t.Fatalf("49 < 50 should allow, got %v", err)
	}
	if c.lastCap != 50 {
		t.Fatalf("day-0 cap should be 50, reserver saw %d", c.lastCap)
	}
	if want := now.Truncate(24 * time.Hour); !c.lastDay.Equal(want) {
		t.Fatalf("reserver day: got %v want %v", c.lastDay, want)
	}
}

func TestReserveThrottlesAtCap(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(12 * time.Hour) // day 0, cap = 50
	e := newTestEnforcer(fakeReader{status: StatusActive, started: &start}, &fakeReserver{sent: 50}, now)
	err := e.Reserve(context.Background(), "acme.test")
	te, ok := AsThrottleError(err)
	if !ok {
		t.Fatalf("expected ThrottleError, got %v", err)
	}
	if te.DailyCap != 50 || te.SentToday != 50 || te.Domain != "acme.test" {
		t.Fatalf("unexpected throttle fields: %+v", te)
	}
	// RetryAfter is until next UTC midnight: from 12:00 that's 12h.
	if te.RetryAfter != 12*time.Hour {
		t.Fatalf("retry_after: got %v want 12h", te.RetryAfter)
	}
}

func TestReserveThrottleClearsAsRampProgresses(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Day 1 cap is 115; 60 sent would throttle on day 0 (cap 50) but not day 1.
	day1 := start.Add(24*time.Hour + time.Hour)
	e := newTestEnforcer(fakeReader{status: StatusActive, started: &start}, &fakeReserver{sent: 60}, day1)
	if err := e.Reserve(context.Background(), "acme.test"); err != nil {
		t.Fatalf("60 < 115 on day 1 should allow, got %v", err)
	}
}

func TestReserveSkipsCounterWhenRampDone(t *testing.T) {
	// A domain past its ramp is never throttled OR counted again — the cap
	// no longer applies, so the counter must not even be consulted.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(31 * 24 * time.Hour) // day 31 of a 30-day ramp
	c := &fakeReserver{sent: 1000000}
	e := newTestEnforcer(fakeReader{status: StatusActive, started: &start}, c, now)
	if err := e.Reserve(context.Background(), "acme.test"); err != nil {
		t.Fatalf("completed ramp must allow at any volume, got %v", err)
	}
	if c.calls != 0 {
		t.Fatal("completed ramp must not touch the counter")
	}
}

func TestReserveFailsOpenOnErrors(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	boom := errors.New("db down")

	var logged int
	// State read error: allow, log.
	e := newTestEnforcer(fakeReader{err: boom}, &fakeReserver{}, now)
	e.logf = func(string, ...any) { logged++ }
	if err := e.Reserve(context.Background(), "acme.test"); err != nil {
		t.Fatalf("state read error must fail open, got %v", err)
	}
	// Reservation error: allow, log.
	e = newTestEnforcer(fakeReader{status: StatusActive, started: &start}, &fakeReserver{err: boom}, now)
	e.logf = func(string, ...any) { logged++ }
	if err := e.Reserve(context.Background(), "acme.test"); err != nil {
		t.Fatalf("reservation error must fail open, got %v", err)
	}
	if logged != 2 {
		t.Fatalf("fail-open paths must log; got %d log calls, want 2", logged)
	}
}

func TestReserveConsumesOneSlotPerCall(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(6 * time.Hour) // day 0, cap 50
	c := &fakeReserver{sent: 48}
	e := newTestEnforcer(fakeReader{status: StatusActive, started: &start}, c, now)
	// 49th and 50th sends pass, 51st throttles.
	if err := e.Reserve(context.Background(), "acme.test"); err != nil {
		t.Fatalf("send 49: %v", err)
	}
	if err := e.Reserve(context.Background(), "acme.test"); err != nil {
		t.Fatalf("send 50: %v", err)
	}
	if _, ok := AsThrottleError(e.Reserve(context.Background(), "acme.test")); !ok {
		t.Fatal("send 51 should throttle")
	}
}

func TestNilEnforcerAllows(t *testing.T) {
	var e *Enforcer
	if err := e.Reserve(context.Background(), "acme.test"); err != nil {
		t.Fatalf("nil enforcer should allow, got %v", err)
	}
	// nil deps also no-op.
	e2 := NewEnforcer(nil, nil, DefaultSchedule)
	if err := e2.Reserve(context.Background(), "acme.test"); err != nil {
		t.Fatalf("nil-dep enforcer should allow, got %v", err)
	}
}
