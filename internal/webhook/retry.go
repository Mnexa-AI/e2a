package webhook

import (
	"time"
)

// retryBackoffs is the per-attempt delay schedule for failed
// deliveries. Total envelope ~72h spread over 8 attempts (slice 5
// extension from the original 5-attempt / ~4h schedule). Matches the
// industry-standard ~24-72h retry window — Svix defaults to ~24h
// across ~15 attempts; Stripe goes to 72h. We picked Stripe's window.
//
// Schedule lookup is gracefully bounded by len(retryBackoffs) — see
// nextRetryAt. In-flight rows with the legacy max_attempts=5 cap
// terminate after the 4h entry; new rows get max_attempts=8 from the
// updated migration 027 default.
var retryBackoffs = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	4 * time.Hour,
	8 * time.Hour,
	16 * time.Hour,
	24 * time.Hour,
}

func nextRetryAt(attempt int) (time.Time, bool) {
	if attempt >= len(retryBackoffs) {
		return time.Time{}, false
	}
	return time.Now().Add(retryBackoffs[attempt]), true
}
