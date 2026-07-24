package outboundsend

import (
	"time"

	"github.com/tokencanopy/e2a/internal/delivery"
	"github.com/tokencanopy/e2a/internal/messagelifecycle"
)

// Metrics is the narrow slice of telemetry.Metrics the outbound send pipeline
// emits (the janitor.Metrics pattern): injectable so tests assert emission
// with a fake, satisfied by every telemetry backend. Label values are
// normalized by the backend — never pass message ids or addresses.
type Metrics interface {
	// OutboundQueueWait is the enqueue→worker-pickup latency of one send
	// attempt (River attempted_at − scheduled_at — due→pickup, never
	// cumulative message age).
	OutboundQueueWait(seconds float64)
	// OutboundTerminal records one terminal outcome for an outbound message.
	// outcome ∈ {sent, failed_suppressed, failed_provider,
	// failed_local_retries, failed_cancelled}.
	OutboundTerminal(outcome string)
	// OutboundTerminalLatency records acceptance→terminal latency for one
	// outbound message (the terminal write's occurred_at −
	// messages.created_at). Observed exactly once per message, co-located
	// with OutboundTerminal so the two share their exactly-once contract.
	OutboundTerminalLatency(seconds float64)
	// OutboundAttempt records one submission attempt to the upstream relay.
	// outcome ∈ {success, temporary_failure, permanent_failure}.
	OutboundAttempt(outcome string, seconds float64)
}

// The telemetry.Metrics label enums, pinned as constants so the worker and
// the terminal reconciler cannot drift apart on spelling.
const (
	terminalSent               = "sent"
	terminalFailedSuppressed   = "failed_suppressed"
	terminalFailedProvider     = "failed_provider"
	terminalFailedLocalRetries = "failed_local_retries"
	terminalFailedCancelled    = "failed_cancelled"

	attemptSuccess          = "success"
	attemptTemporaryFailure = "temporary_failure"
	attemptPermanentFailure = "permanent_failure"
)

// noopMetrics is the nil-safe default: a worker built without WithMetrics
// records nothing instead of nil-panicking mid-send.
type noopMetrics struct{}

func (noopMetrics) OutboundQueueWait(float64)       {}
func (noopMetrics) OutboundTerminal(string)         {}
func (noopMetrics) OutboundTerminalLatency(float64) {}
func (noopMetrics) OutboundAttempt(string, float64) {}

// observeTerminalLatency records the acceptance→terminal latency for one
// settled message. Call it ONLY where OutboundTerminal is emitted, with the
// same occurred_at the terminal write used — the two instruments share one
// exactly-once contract. A zero accepted_at (hand-built row) or a
// non-positive delta (clock skew) records no sample — the terminal is still
// counted, but no honest duration exists (same discipline as the
// queue-wait guard).
func observeTerminalLatency(m Metrics, acceptedAt, occurredAt time.Time) {
	if acceptedAt.IsZero() || occurredAt.IsZero() {
		return
	}
	if d := occurredAt.Sub(acceptedAt); d > 0 {
		m.OutboundTerminalLatency(d.Seconds())
	}
}

// terminalOutcome maps a MarkFailed call's provenance to the OutboundTerminal
// label: suppression holds blocked recipients; a policy cancel without them
// (a cancelled job settled by the reconciler) is failed_cancelled — NOT
// failed_local_retries, so cancellation volume can't mask a real
// retries-exhausted regression in the alerting signal; a provider-confirmed
// rejection carries provenance 'provider'; everything else is a local
// give-up (retries/horizon exhausted). MarkFailed is the GUARDED terminal
// write — a row holding provider-accept evidence settles as sent instead —
// so this labels the intended outcome; that rare evidence-settle correction
// is invisible here and negligible at SLI granularity.
func terminalOutcome(source delivery.FailureSource, reason messagelifecycle.ReasonCode, blockedRecipients []string) string {
	switch {
	case len(blockedRecipients) > 0:
		return terminalFailedSuppressed
	case reason == messagelifecycle.ReasonSubmissionCancelled:
		return terminalFailedCancelled
	case source == delivery.FailureSourceProvider:
		return terminalFailedProvider
	default:
		return terminalFailedLocalRetries
	}
}
