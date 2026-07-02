package warmup

import (
	"context"
	"errors"
	"fmt"
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

// State is a domain's warmup row as the enforcer needs it.
type State struct {
	Status    string
	StartedAt *time.Time
}

// StateReader reads a domain's warmup state. *identity.Store satisfies it.
// Kept as an interface so warmup does not import identity (and its pgx deps)
// and tests can inject a fake.
type StateReader interface {
	GetWarmupState(ctx context.Context, domain string) (status string, startedAt *time.Time, err error)
}

// DailyCounter returns how many outbound messages a domain has already sent
// during the current UTC day. *usage.Store satisfies it.
type DailyCounter interface {
	CountDomainSendsToday(ctx context.Context, domain string) (int, error)
}

// ThrottleError is returned by Enforcer.Check when a domain has reached its
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

// Enforcer decides whether a domain may send one more message under its warmup
// ramp. It is safe for concurrent use (its dependencies are).
type Enforcer struct {
	reader   StateReader
	counter  DailyCounter
	schedule Schedule
	now      func() time.Time // injectable clock for tests; nil => time.Now
}

// NewEnforcer builds the production enforcer. A nil reader OR counter yields a
// no-op enforcer (Check always allows) so wiring warmup is optional — a
// self-host without the sending feature simply leaves it unset.
func NewEnforcer(reader StateReader, counter DailyCounter, schedule Schedule) *Enforcer {
	return &Enforcer{
		reader:   reader,
		counter:  counter,
		schedule: NewSchedule(schedule.StartDaily, schedule.TargetDaily, schedule.RampDays),
	}
}

// Check returns nil if the domain may send another message right now, a
// *ThrottleError if it has hit today's warmup cap, or a plain error if a
// dependency read fails. It no-ops (allows) unless the domain's warmup_status
// is exactly "active": inactive/paused domains, the shared relay domain, and
// self-host deployments without the sending feature all flow at full volume.
//
// Fail-open on read errors: warmup is a reputation optimization, not a
// correctness gate, so a transient DB blip must not block legitimate mail. The
// error is returned for the caller to log; the caller treats a non-throttle
// error as "allow".
func (e *Enforcer) Check(ctx context.Context, domain string) error {
	if e == nil || e.reader == nil || e.counter == nil || domain == "" {
		return nil
	}
	status, startedAt, err := e.reader.GetWarmupState(ctx, domain)
	if err != nil {
		return err
	}
	if status != StatusActive || startedAt == nil {
		return nil
	}
	cap, _ := e.schedule.DailyCap(*startedAt, e.clock())
	sent, err := e.counter.CountDomainSendsToday(ctx, domain)
	if err != nil {
		return err
	}
	if sent >= cap {
		return &ThrottleError{
			Domain:     domain,
			DailyCap:   cap,
			SentToday:  sent,
			RetryAfter: untilNextUTCMidnight(e.clock()),
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

// untilNextUTCMidnight is how long until the per-domain daily counter resets
// (it buckets on the UTC calendar day, matching CountDomainSendsToday).
func untilNextUTCMidnight(now time.Time) time.Duration {
	u := now.UTC()
	next := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	return next.Sub(u)
}
