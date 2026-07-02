// Package warmup implements per-domain sending warmup: an automatic,
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
//   - Enforcer (enforcer.go) binds a Schedule to a domain-state reader and a
//     per-domain daily send counter to decide whether one more send is allowed.
//
// Warmup is scoped PER DOMAIN because mailbox providers track reputation per
// sending domain, not per agent or account.
//
// An EngagementProvider seam (engagement.go) is defined for the follow-up
// warmup-network phase (peer inboxes exchanging + positively engaging with
// mail); the core ramp does not depend on it.
package warmup

import "time"

// Schedule is the volume ramp. Daily cap grows linearly from StartDaily on the
// first day to TargetDaily on day RampDays, then stays at TargetDaily.
//
// Linear (not geometric) on purpose: it is predictable, easy to reason about
// from the config numbers alone ("day 7 of a 30-day ramp to 2000 from 50 =
// ~505"), and never overshoots the target. A zero-value Schedule is not valid;
// construct via NewSchedule so the fields are sanitized.
type Schedule struct {
	// StartDaily is the allowance on day 0 (the first 24h after warmup begins).
	StartDaily int
	// TargetDaily is the full daily allowance reached at the end of the ramp.
	TargetDaily int
	// RampDays is the number of days over which the cap climbs from StartDaily
	// to TargetDaily.
	RampDays int
}

// DefaultSchedule is the built-in ramp used when the operator does not
// configure `warmup:` — a conservative curve that suits a brand-new domain:
// 50 sends on day one climbing to 2,000/day over 30 days.
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
// day containing now, and whether the ramp has completed (the cap has reached
// TargetDaily). startedAt is when warmup began for the domain.
//
// Day index is floor(elapsed / 24h) measured from startedAt, so the cap steps
// up once per 24 hours rather than mid-day. A now before startedAt (clock skew)
// is treated as day 0. The cap is linearly interpolated across [0, RampDays]:
//
//	cap(d) = StartDaily + (TargetDaily-StartDaily) * d / RampDays   for 0 <= d < RampDays
//	cap(d) = TargetDaily                                            for d >= RampDays
func (s Schedule) DailyCap(startedAt, now time.Time) (cap int, done bool) {
	sched := NewSchedule(s.StartDaily, s.TargetDaily, s.RampDays)
	elapsed := now.Sub(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	day := int(elapsed / (24 * time.Hour))
	if day >= sched.RampDays {
		return sched.TargetDaily, true
	}
	span := sched.TargetDaily - sched.StartDaily
	// Integer interpolation; rounds toward zero, which keeps the cap at or
	// below the ideal line (conservative for reputation).
	cap = sched.StartDaily + span*day/sched.RampDays
	return cap, false
}
