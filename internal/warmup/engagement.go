package warmup

import "context"

// EngagementProvider is the seam for the follow-up warmup-network phase: a
// system where peer inboxes exchange mail with a warming domain and positively
// engage with it (open, reply, mark "not spam", move out of the Promotions
// tab) to manufacture the positive-signal history mailbox providers reward.
//
// The core volume ramp (Schedule + Enforcer) does NOT depend on this — it is
// defined now so the network can be wired in without reshaping the package:
// the ramp governs how fast REAL outbound volume grows; an engagement provider
// would run alongside it, generating synthetic peer traffic during the same
// active window. A future Enforcer/worker will call StartWarmup when a domain
// enters the ramp and StopWarmup when it completes or is paused.
//
// Implementations must be safe for concurrent use and idempotent (StartWarmup
// on an already-enrolled domain, StopWarmup on an absent one, are no-ops).
type EngagementProvider interface {
	// StartWarmup enrolls a domain in the engagement network.
	StartWarmup(ctx context.Context, domain string) error
	// StopWarmup removes a domain from the engagement network.
	StopWarmup(ctx context.Context, domain string) error
}

// NoopEngagementProvider is the default when no network is configured: warmup
// runs as a pure volume ramp with no synthetic engagement traffic. Selecting it
// explicitly (rather than a nil check at every call site) keeps callers simple.
type NoopEngagementProvider struct{}

func (NoopEngagementProvider) StartWarmup(context.Context, string) error { return nil }
func (NoopEngagementProvider) StopWarmup(context.Context, string) error  { return nil }

// Compile-time check.
var _ EngagementProvider = NoopEngagementProvider{}
