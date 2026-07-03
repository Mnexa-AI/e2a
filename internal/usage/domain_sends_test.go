package usage_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
)

// ReserveDomainSend is the ramp-up numerator: an atomic per-(domain, UTC day)
// slot reservation guarded by the day's cap. It must count per domain per day,
// refuse exactly at the cap, and never jointly overshoot under concurrency.
func TestReserveDomainSend(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := usage.NewStore(pool)

	day := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	const domain = "reserve.example.com"

	// Slots 1..3 of cap 3 are granted with a running count.
	for want := 1; want <= 3; want++ {
		allowed, count, err := store.ReserveDomainSend(ctx, domain, day, 3)
		if err != nil || !allowed || count != want {
			t.Fatalf("slot %d: got allowed=%v count=%d err=%v, want true/%d/nil", want, allowed, count, err, want)
		}
	}
	// Slot 4 refuses and reports the day's total.
	allowed, count, err := store.ReserveDomainSend(ctx, domain, day, 3)
	if err != nil || allowed || count != 3 {
		t.Fatalf("over cap: got allowed=%v count=%d err=%v, want false/3/nil", allowed, count, err)
	}

	// A new UTC day starts a fresh counter.
	if allowed, count, err := store.ReserveDomainSend(ctx, domain, day.Add(24*time.Hour), 3); err != nil || !allowed || count != 1 {
		t.Fatalf("next day: got allowed=%v count=%d err=%v, want true/1/nil", allowed, count, err)
	}
	// Another domain on the same day is independent.
	if allowed, count, err := store.ReserveDomainSend(ctx, "other.example.com", day, 3); err != nil || !allowed || count != 1 {
		t.Fatalf("other domain: got allowed=%v count=%d err=%v, want true/1/nil", allowed, count, err)
	}
	// A raised cap (the ramp stepping up next day) re-admits the same domain+day.
	if allowed, _, err := store.ReserveDomainSend(ctx, domain, day, 10); err != nil || !allowed {
		t.Fatalf("raised cap: got allowed=%v err=%v, want true/nil", allowed, err)
	}
}

// A non-positive cap grants nothing — including the FIRST reservation of a
// (domain, day), which takes the upsert's unconditional INSERT arm and would
// otherwise slip past the DO UPDATE arm's cap guard. Unreachable via the
// enforcer (NewSchedule floors the cap at 1) but the contract must hold for
// any caller.
func TestReserveDomainSendZeroCap(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := usage.NewStore(pool)

	day := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	const domain = "zerocap.example.com"

	// Fresh (domain, day): refused, and no phantom row is created.
	for _, cap := range []int{0, -1} {
		if allowed, count, err := store.ReserveDomainSend(ctx, domain, day, cap); err != nil || allowed || count != 0 {
			t.Fatalf("cap %d fresh: got allowed=%v count=%d err=%v, want false/0/nil", cap, allowed, count, err)
		}
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM domain_send_counters WHERE domain = $1`, domain,
	).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("zero-cap refusal must not create a row: rows=%d err=%v", rows, err)
	}

	// With prior spend, a zero cap still refuses and reports the day's total.
	if allowed, _, err := store.ReserveDomainSend(ctx, domain, day, 5); err != nil || !allowed {
		t.Fatalf("seed spend: allowed=%v err=%v", allowed, err)
	}
	if allowed, count, err := store.ReserveDomainSend(ctx, domain, day, 0); err != nil || allowed || count != 1 {
		t.Fatalf("cap 0 after spend: got allowed=%v count=%d err=%v, want false/1/nil", allowed, count, err)
	}
}

// The reservation must hold under concurrency: N parallel claims against a cap
// admit exactly cap sends, never cap+overshoot — this is the property the old
// read-count-then-compare check lacked.
func TestReserveDomainSendConcurrent(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := usage.NewStore(pool)

	day := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	const domain = "burst.example.com"
	const cap = 10
	const attempts = 40

	var wg sync.WaitGroup
	granted := make(chan bool, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _, err := store.ReserveDomainSend(ctx, domain, day, cap)
			if err != nil {
				t.Errorf("ReserveDomainSend: %v", err)
				return
			}
			granted <- allowed
		}()
	}
	wg.Wait()
	close(granted)

	got := 0
	for a := range granted {
		if a {
			got++
		}
	}
	if got != cap {
		t.Fatalf("concurrent burst: %d of %d claims granted, want exactly %d", got, attempts, cap)
	}
}
