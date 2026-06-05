package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

// retryWithBackoff is exercised here at the unit level because the
// caller (MarkSendSucceededWithRetry) is exercised via an integration
// test against Postgres. Unit-testing the retry semantics with a fake
// fn lets us assert exhaustion, recovery, and ctx-cancel behavior
// without timing flakes from real DB calls.

// TestRetryWithBackoff_FirstAttemptSuccessNoBackoff verifies the
// happy path: the first attempt returns nil and no sleep is taken.
// Important because the first attempt has delay=0, but tests must
// confirm the loop short-circuits without iterating further.
func TestRetryWithBackoff_FirstAttemptSuccessNoBackoff(t *testing.T) {
	calls := 0
	start := time.Now()
	err := retryWithBackoff(context.Background(),
		[]time.Duration{0, time.Second, time.Second},
		func(ctx context.Context) error {
			calls++
			return nil
		})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	// Should be well under the first backoff (1s) — no sleep on success.
	if elapsed > 100*time.Millisecond {
		t.Errorf("expected fast return on first-attempt success, took %v", elapsed)
	}
}

// TestRetryWithBackoff_RecoversAfterTransientFailures pins the bug-fix
// invariant: if the closure fails N times then succeeds, the helper
// returns nil and stops calling fn. This is the realistic SES
// MarkSendSucceeded transient case (pool acquisition retry, brief
// statement_timeout) — the retry budget covers the recovery.
func TestRetryWithBackoff_RecoversAfterTransientFailures(t *testing.T) {
	calls := 0
	err := retryWithBackoff(context.Background(),
		// Tight backoffs for the test — actual production schedule
		// is 0, 100ms, 500ms, 1.5s, 5s.
		[]time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond},
		func(ctx context.Context) error {
			calls++
			if calls < 3 {
				return errors.New("transient")
			}
			return nil
		})
	if err != nil {
		t.Fatalf("expected nil after recovery, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected exactly 3 calls (2 fails + 1 success), got %d", calls)
	}
}

// TestRetryWithBackoff_ExhaustsAndReturnsLastError verifies that when
// every attempt fails, the helper returns the LAST error (not a
// wrapped or generic one) so the caller can log it for diagnosis.
// Asserts the count matches the backoff slice length.
func TestRetryWithBackoff_ExhaustsAndReturnsLastError(t *testing.T) {
	calls := 0
	finalErr := errors.New("attempt 4 final error")
	backoffs := []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	err := retryWithBackoff(context.Background(), backoffs,
		func(ctx context.Context) error {
			calls++
			if calls == len(backoffs) {
				return finalErr
			}
			return errors.New("earlier")
		})
	if err == nil {
		t.Fatal("expected error after exhaustion, got nil")
	}
	if !errors.Is(err, finalErr) {
		// errors.Is may not match if not wrapped; check by string too.
		if err.Error() != finalErr.Error() {
			t.Errorf("expected last error %q, got %q", finalErr.Error(), err.Error())
		}
	}
	if calls != len(backoffs) {
		t.Errorf("expected %d calls, got %d", len(backoffs), calls)
	}
}

// TestRetryWithBackoff_RespectsContextCancellation verifies that
// ctx cancellation breaks the retry loop mid-sleep. Important for
// the production usage: MarkSendSucceededWithRetry uses
// context.WithTimeout(15s), and we want the loop to bail when the
// budget is exhausted rather than running an extra round of fn.
func TestRetryWithBackoff_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retryWithBackoff(ctx,
		[]time.Duration{0, 1 * time.Second, 1 * time.Second},
		func(ctx context.Context) error {
			calls++
			if calls == 1 {
				// Cancel from inside the first attempt so the
				// subsequent sleep aborts.
				cancel()
			}
			return errors.New("transient")
		})
	if err == nil {
		t.Fatal("expected error after ctx cancel, got nil")
	}
	// Only the first call should have happened; the sleep aborts on
	// ctx.Done before the second attempt.
	if calls != 1 {
		t.Errorf("expected 1 call before ctx cancel, got %d", calls)
	}
}

// TestRetryWithBackoff_EmptyBackoffsRunsZeroAttempts is a defensive
// edge case. Empty backoff slice means zero iterations; the helper
// returns nil without calling fn at all. Not a realistic usage but
// pins the boundary so future refactors don't accidentally start
// returning the zero value of error in confusing ways.
func TestRetryWithBackoff_EmptyBackoffsRunsZeroAttempts(t *testing.T) {
	calls := 0
	err := retryWithBackoff(context.Background(), nil,
		func(ctx context.Context) error {
			calls++
			return errors.New("should never run")
		})
	if err != nil {
		t.Errorf("expected nil with empty backoffs, got %v", err)
	}
	if calls != 0 {
		t.Errorf("expected 0 calls, got %d", calls)
	}
}
