package warmup

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// Status values persisted in domains.warmup_status. Mirrors the CHECK
// constraint in migration 050. Kept as string constants (not a typed enum) so
// the identity store, which owns the column, does not import this package.
const (
	StatusInactive = "inactive" // no warmup in effect; enforcer no-ops
	StatusActive   = "active"   // ramp running; daily cap applies
	StatusPaused   = "paused"   // ramp suspended; enforcer no-ops
)

// StateReader reads a domain's warmup state. *identity.Store satisfies it.
// Kept as an interface so warmup does not import identity (and its pgx deps)
// and tests can inject a fake. domain must be the stored (normalized) form —
// callers pass AgentIdentity.Domain, which is.
type StateReader interface {
	GetWarmupState(ctx context.Context, domain string) (status string, startedAt *time.Time, err error)
}

// DailyReserver atomically reserves one send slot for the domain on the given
// UTC calendar day, refusing once count would exceed cap: the
// increment-if-below-cap runs as a single statement, so concurrent sends
// serialize on the counter row and can never jointly overshoot the cap
// (unlike a read-count-then-compare check). allowed=false means the day's cap
// is spent; count is the day's running total either way. *usage.Store
// satisfies it (domain_send_counters, migration 050).
//
// A reserved slot is consumed even if the send later fails downstream —
// deliberately conservative: an error burst can only slow a warming domain
// down, never push it over its ramp.
type DailyReserver interface {
	ReserveDomainSend(ctx context.Context, domain string, day time.Time, cap int) (allowed bool, count int, err error)
}

// ThrottleError is returned by Enforcer.Reserve when a domain has reached its
// warmup daily cap. Handlers map it to HTTP 429 with the details below so the
// caller can pace itself. It is a distinct type (not a limits error) because
// warmup is a temporary, self-clearing throttle, not a plan cap.
type ThrottleError struct {
	Domain     string
	DailyCap   int
	SentToday  int
	RetryAfter time.Duration // until the next UTC midnight, when the day's counter resets
}

func (e *ThrottleError) Error() string {
	return fmt.Sprintf("warmup: domain %s reached its daily warmup cap (%d/%d)", e.Domain, e.SentToday, e.DailyCap)
}

// AsThrottleError reports whether err is a *ThrottleError and returns it.
func AsThrottleError(err error) (*ThrottleError, bool) {
	var te *ThrottleError
	if errors.As(err, &te) {
		return te, true
	}
	return nil, false
}

// Enforcer reserves warmup send slots for domains. It is safe for concurrent
// use (its dependencies are; the reservation itself is atomic in SQL).
type Enforcer struct {
	reader   StateReader
	counter  DailyReserver
	schedule Schedule
	now      func() time.Time              // injectable clock for tests; nil => time.Now
	logf     func(format string, v ...any) // injectable logger for tests; nil => log.Printf
}

// NewEnforcer builds the production enforcer. A nil reader OR counter yields a
// no-op enforcer (Reserve always allows) so wiring warmup is optional — a
// self-host without the sending feature simply leaves it unset.
func NewEnforcer(reader StateReader, counter DailyReserver, schedule Schedule) *Enforcer {
	return &Enforcer{
		reader:   reader,
		counter:  counter,
		schedule: NewSchedule(schedule.StartDaily, schedule.TargetDaily, schedule.RampDays),
	}
}

// Reserve claims one send slot for the domain under its warmup ramp. It
// returns nil when the send may proceed and a *ThrottleError when the domain
// has spent today's cap — those are the only two outcomes. It no-ops (allows,
// reserving nothing) unless the domain's warmup_status is exactly "active"
// with a ramp anchor: inactive/paused domains, the shared relay domain,
// ramp-completed domains, and self-host deployments without the sending
// feature all flow at full volume. domain must be the stored (normalized)
// form; AgentIdentity.Domain is.
//
// Fail-open on dependency errors: warmup is a reputation optimization, not a
// correctness gate, so a transient DB blip must not block legitimate mail.
// The error is logged HERE (single owner of the policy) — a persistently
// failing read is visible in the logs rather than silently disabling the
// ramp.
func (e *Enforcer) Reserve(ctx context.Context, domain string) error {
	if e == nil || e.reader == nil || e.counter == nil || domain == "" {
		return nil
	}
	status, startedAt, err := e.reader.GetWarmupState(ctx, domain)
	if err != nil {
		e.printf("[warmup] state read failed for %s (allowing send): %v", domain, err)
		return nil
	}
	if status != StatusActive || startedAt == nil {
		return nil
	}
	now := e.clock()
	cap, done := e.schedule.DailyCap(*startedAt, now)
	if done {
		// Ramp completed: the domain has built its reputation and is never
		// throttled (or counted) again, per the feature contract. warmup_status
		// stays 'active' — "completed" is derived, not stored.
		return nil
	}
	day := now.UTC().Truncate(24 * time.Hour)
	allowed, count, err := e.counter.ReserveDomainSend(ctx, domain, day, cap)
	if err != nil {
		e.printf("[warmup] slot reservation failed for %s (allowing send): %v", domain, err)
		return nil
	}
	if !allowed {
		return &ThrottleError{
			Domain:     domain,
			DailyCap:   cap,
			SentToday:  count,
			RetryAfter: untilNextUTCMidnight(now),
		}
	}
	return nil
}

func (e *Enforcer) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

func (e *Enforcer) printf(format string, v ...any) {
	if e.logf != nil {
		e.logf(format, v...)
		return
	}
	log.Printf(format, v...)
}

// untilNextUTCMidnight is how long until the per-domain daily counter resets
// and the ramp's day index advances (both bucket on the UTC calendar day).
func untilNextUTCMidnight(now time.Time) time.Duration {
	u := now.UTC()
	next := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	return next.Sub(u)
}
