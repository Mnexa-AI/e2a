// Package sendramp implements durable, per-domain recipient-volume ramping for
// the asynchronous outbound delivery worker.
package sendramp

// Schedule is snapshotted when a domain first sends through its verified
// identity. Progress is measured in UTC days that reach the provider-accepted
// volume threshold, so idle or token sends cannot age into full volume.
type Schedule struct {
	StartDaily  int
	TargetDaily int
	RampDays    int
}

var DefaultSchedule = Schedule{StartDaily: 50, TargetDaily: 2000, RampDays: 30}

const MinimumStartDaily = 50

func NewSchedule(startDaily, targetDaily, rampDays int) Schedule {
	if startDaily < MinimumStartDaily {
		startDaily = MinimumStartDaily
	}
	if targetDaily < startDaily {
		targetDaily = startDaily
	}
	if rampDays < 1 {
		rampDays = 1
	}
	return Schedule{StartDaily: startDaily, TargetDaily: targetDaily, RampDays: rampDays}
}

// Qualifies reports whether provider-accepted recipient volume reached half of
// the day's snapshotted allowance, rounded up.
func Qualifies(confirmed, limit int) bool {
	return limit >= MinimumStartDaily && confirmed >= (limit+1)/2
}

// CapForActiveDay returns the recipient allowance for a zero-based qualified-day
// index. The target is reached on the final configured ramp day.
func (s Schedule) CapForActiveDay(activeDay int) int {
	s = NewSchedule(s.StartDaily, s.TargetDaily, s.RampDays)
	if activeDay <= 0 {
		if s.RampDays == 1 {
			return s.TargetDaily
		}
		return s.StartDaily
	}
	if activeDay >= s.RampDays-1 {
		return s.TargetDaily
	}
	return s.StartDaily + (s.TargetDaily-s.StartDaily)*activeDay/(s.RampDays-1)
}
