package warmup

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

func (f fakeReader) GetWarmupState(context.Context, string) (string, *time.Time, error) {
	return f.status, f.started, f.err
}

type fakeCounter struct {
	sent int
	err  error
}

func (f fakeCounter) CountDomainSendsToday(context.Context, string) (int, error) {
	return f.sent, f.err
}

func newTestEnforcer(r StateReader, c DailyCounter, now time.Time) *Enforcer {
	e := NewEnforcer(r, c, Schedule{StartDaily: 50, TargetDaily: 2000, RampDays: 30})
	e.now = func() time.Time { return now }
	return e
}

func ptr(t time.Time) *time.Time { return &t }

func TestCheckNoopWhenNotActive(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	started := now.Add(-time.Hour)
	for _, status := range []string{StatusInactive, StatusPaused, "", "unknown"} {
		e := newTestEnforcer(fakeReader{status: status, started: &started}, fakeCounter{sent: 100000}, now)
		if err := e.Check(context.Background(), "acme.test"); err != nil {
			t.Fatalf("status %q: expected allow, got %v", status, err)
		}
	}
}

func TestCheckNoopWhenStartedAtNil(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e := newTestEnforcer(fakeReader{status: StatusActive, started: nil}, fakeCounter{sent: 999}, now)
	if err := e.Check(context.Background(), "acme.test"); err != nil {
		t.Fatalf("nil started_at should allow, got %v", err)
	}
}

func TestCheckAllowsUnderCap(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(12 * time.Hour) // day 0, cap = 50
	e := newTestEnforcer(fakeReader{status: StatusActive, started: &start}, fakeCounter{sent: 49}, now)
	if err := e.Check(context.Background(), "acme.test"); err != nil {
		t.Fatalf("49 < 50 should allow, got %v", err)
	}
}

func TestCheckThrottlesAtCap(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(12 * time.Hour) // day 0, cap = 50
	e := newTestEnforcer(fakeReader{status: StatusActive, started: &start}, fakeCounter{sent: 50}, now)
	err := e.Check(context.Background(), "acme.test")
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

func TestCheckThrottleClearsAsRampProgresses(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Day 1 cap is 115; 60 sent would throttle on day 0 (cap 50) but not day 1.
	day1 := start.Add(24*time.Hour + time.Hour)
	e := newTestEnforcer(fakeReader{status: StatusActive, started: &start}, fakeCounter{sent: 60}, day1)
	if err := e.Check(context.Background(), "acme.test"); err != nil {
		t.Fatalf("60 < 115 on day 1 should allow, got %v", err)
	}
}

func TestCheckFailOpenOnReadError(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	boom := errors.New("db down")
	e := newTestEnforcer(fakeReader{err: boom}, fakeCounter{}, now)
	if err := e.Check(context.Background(), "acme.test"); !errors.Is(err, boom) {
		t.Fatalf("state read error should propagate for logging, got %v", err)
	}
	// A propagated error is NOT a ThrottleError, so the handler allows the send.
	if _, ok := AsThrottleError(e.Check(context.Background(), "acme.test")); ok {
		t.Fatal("read error must not surface as a throttle")
	}
}

func TestNilEnforcerAllows(t *testing.T) {
	var e *Enforcer
	if err := e.Check(context.Background(), "acme.test"); err != nil {
		t.Fatalf("nil enforcer should allow, got %v", err)
	}
	// nil deps also no-op.
	e2 := NewEnforcer(nil, nil, DefaultSchedule)
	if err := e2.Check(context.Background(), "acme.test"); err != nil {
		t.Fatalf("nil-dep enforcer should allow, got %v", err)
	}
}
