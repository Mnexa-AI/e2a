package outboundsend

import "github.com/tokencanopy/e2a/internal/delivery"

// Metrics is the narrow slice of telemetry.Metrics the outbound send pipeline
// emits (the janitor.Metrics pattern): injectable so tests assert emission
// with a fake, satisfied by every telemetry backend. Label values are
// normalized by the backend — never pass message ids or addresses.
type Metrics interface {
	// OutboundQueueWait is the enqueue→worker-pickup latency of one send
	// attempt (River attempted_at − created_at).
	OutboundQueueWait(seconds float64)
	// OutboundTerminal records one terminal outcome for an outbound message.
	// outcome ∈ {sent, failed_suppressed, failed_provider,
	// failed_local_retries, deferred_terminal}.
	OutboundTerminal(outcome string)
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
	terminalDeferred           = "deferred_terminal"

	attemptSuccess          = "success"
	attemptTemporaryFailure = "temporary_failure"
	attemptPermanentFailure = "permanent_failure"
)

// noopMetrics is the nil-safe default: a worker built without WithMetrics
// records nothing instead of nil-panicking mid-send.
type noopMetrics struct{}

func (noopMetrics) OutboundQueueWait(float64)       {}
func (noopMetrics) OutboundTerminal(string)         {}
func (noopMetrics) OutboundAttempt(string, float64) {}

// terminalOutcome maps a MarkFailed call's provenance to the OutboundTerminal
// label: suppression holds blocked recipients, a provider-confirmed rejection
// carries provenance 'provider', everything else is a local give-up
// (retries/horizon exhausted or a local policy cancel). MarkFailed is the
// GUARDED terminal write — a row holding provider-accept evidence settles as
// sent instead — so this labels the intended outcome; that rare
// evidence-settle correction is invisible here and negligible at SLI
// granularity.
func terminalOutcome(source delivery.FailureSource, blockedRecipients []string) string {
	switch {
	case len(blockedRecipients) > 0:
		return terminalFailedSuppressed
	case source == delivery.FailureSourceProvider:
		return terminalFailedProvider
	default:
		return terminalFailedLocalRetries
	}
}
