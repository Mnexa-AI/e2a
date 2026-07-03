// Package sendramp implements per-domain sending ramp-up (domain warm-up): an automatic,
// transparent ramp that raises a newly sending-verified domain's daily
// outbound allowance over a fixed window so a cold domain builds ISP
// reputation instead of blasting full volume on day one — the same behavior
// SES/Postmark/SendGrid apply to new senders.
//
// The package has two halves:
//
//   - Schedule (this file) is the pure ramp math: given a start time and
//     "now", it returns the day's allowed send count. No I/O, no clock — the
//     caller passes the times in, so it is trivially unit-testable.
//   - Enforcer (enforcer.go) binds a Schedule to a domain-state reader and an
//     atomic per-domain daily send counter to reserve send slots.
//
// SendingRamp is scoped PER DOMAIN because mailbox providers track reputation per
// sending domain, not per agent or account.
package sendramp

import "time"

// Schedule is the volume ramp. Daily cap grows linearly from StartDaily on the
// first day to TargetDaily on day RampDays, then stays at TargetDaily.
//
// Linear (not geometric) on purpose: it is predictable, easy to reason about
// from the config numbers alone ("day 7 of a 30-day ramp to 2000 from 50 =
// ~505"), and never overshoots the target. A zero-value Schedule is not valid;
// construct via NewSchedule so the fields are sanitized.
type Schedule struct {
	// StartDaily is the allowance on day 0 (the UTC calendar day ramp-up begins).
	StartDaily int
	// TargetDaily is the full daily allowance reached at the end of the ramp.
	TargetDaily int
	// RampDays is the number of days over which the cap climbs from StartDaily
	// to TargetDaily.
	RampDays int
}

// DefaultSchedule is the built-in ramp — a conservative curve that suits a
// brand-new domain: 50 sends on day one climbing to 2,000/day over 30 days.
// config.Load seeds the `sending_ramp:` section from these numbers so a partially
// set config keeps per-field defaults.
var DefaultSchedule = Schedule{StartDaily: 50, TargetDaily: 2000, RampDays: 30}

// NewSchedule returns a Schedule with the fields clamped to a sane, monotonic
// shape so a mis-set config can never produce a cap that shrinks over time or a
// divide-by-zero:
//   - StartDaily is floored at 1 (a 0 start would block the domain forever).
//   - TargetDaily is raised to StartDaily if it was set below it (the ramp must
//     not descend).
//   - RampDays is floored at 1 (a 0-day ramp means "already warm": day 0 is the
//     target).
func NewSchedule(startDaily, targetDaily, rampDays int) Schedule {
	if startDaily < 1 {
		startDaily = 1
	}
	if rampDays < 1 {
		rampDays = 1
	}
	if targetDaily < startDaily {
		targetDaily = startDaily
	}
	return Schedule{StartDaily: startDaily, TargetDaily: targetDaily, RampDays: rampDays}
}

// DailyCap returns the number of messages the domain may send during the UTC
// calendar day containing now, and whether the ramp has completed (the cap has
// reached TargetDaily). startedAt is when ramp-up began for the domain.
//
// The day index is the number of UTC calendar-day boundaries between startedAt
// and now, so the cap steps up at UTC midnight — the same instant the
// per-domain daily counter resets and the instant ThrottleError.RetryAfter
// points at. (Indexing by 24h-from-start would let a domain sample two counter
// days inside one ramp day and send double its cap.) Day 0 is the partial
// first day, which is conservative: a domain verified at 20:00 UTC gets its
// day-0 allowance for 4 hours, then day 1 begins. A now before startedAt
// (clock skew) is treated as day 0. The cap is linearly interpolated across
// [0, RampDays]:
//
//	cap(d) = StartDaily + (TargetDaily-StartDaily) * d / RampDays   for 0 <= d < RampDays
//	cap(d) = TargetDaily                                            for d >= RampDays
func (s Schedule) DailyCap(startedAt, now time.Time) (cap int, done bool) {
	sched := NewSchedule(s.StartDaily, s.TargetDaily, s.RampDays)
	day := calendarDaysUTC(startedAt, now)
	if day < 0 {
		day = 0
	}
	if day >= sched.RampDays {
		return sched.TargetDaily, true
	}
	span := sched.TargetDaily - sched.StartDaily
	// Integer interpolation; rounds toward zero, which keeps the cap at or
	// below the ideal line (conservative for reputation).
	cap = sched.StartDaily + span*day/sched.RampDays
	return cap, false
}

// calendarDaysUTC counts UTC calendar-day boundaries crossed between from and
// to (to's UTC date minus from's UTC date, in days).
func calendarDaysUTC(from, to time.Time) int {
	f, t := from.UTC(), to.UTC()
	fd := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, time.UTC)
	td := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return int(td.Sub(fd) / (24 * time.Hour))
}
