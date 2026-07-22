// Package delivery implements outbound delivery feedback (decision 9 /
// Slice 4b): the async delivery lifecycle SES reports per message and per
// recipient, plus a per-tenant suppression list. The SES notifications are
// ingested over SNS (see sns.go / consumer.go); the AWS surface stays at the
// edge so the transition/suppression logic is testable without AWS.
package delivery

// Status is the outbound delivery lifecycle of a message (or a single
// recipient of it). It maps 1:1 onto messages.delivery_status /
// message_recipients.status.
type Status string

const (
	// StatusAccepted is the async-pipeline entry state (async-send-contract.md
	// §3.1): durably persisted + queued for submission, no network I/O yet.
	// Replaces the legacy StatusQueued, which was never emitted in production.
	StatusAccepted Status = "accepted"
	// StatusSending means a worker holds the lease and is submitting to SES.
	StatusSending    Status = "sending"
	StatusQueued     Status = "queued"     // DEPRECATED legacy alias for accepted; kept so any historical row stays Valid()
	StatusSent       Status = "sent"       // accepted by the relay (SES) — NON-terminal
	StatusDeferred   Status = "deferred"   // transient delay (SES deliveryDelay); poll, no event
	StatusDelivered  Status = "delivered"  // SES confirmed delivery to the recipient MTA
	StatusBounced    Status = "bounced"    // hard/soft bounce
	StatusComplained Status = "complained" // recipient marked spam (FBL complaint)
	StatusFailed     Status = "failed"     // terminal send failure (retries exhausted / permanent reject)
)

// rank orders statuses by the decision-9 monotonic precedence
//
//	complained > bounced > failed > delivered > deferred > sent > sending > accepted
//
// Higher rank wins a merge, so out-of-order or duplicate SNS events can never
// regress a terminal status (a late `delivered` never clobbers a `complained`).
// `accepted`/`sending` sit below `sent` so the async pre-send states never win
// over provider feedback. (async-send-contract.md §3.1.)
//
// `failed` sits ABOVE `delivered` — plain-Merge delivery feedback must never
// silently erase a failure (the §3.1 correction is the explicit, provenance-
// gated exception in ResolveMessageRollup, never the default merge) — but
// BELOW `bounced`/`complained`: those are compliance-critical provider
// dispositions that prove the provider handled the message, and a failure
// write (e.g. an SES Reject for a duplicate submit of the same message) must
// not clobber a recorded bounce or complaint.
var rank = map[Status]int{
	StatusAccepted:   0,
	StatusQueued:     0, // legacy alias, equal rank to accepted
	StatusSending:    1,
	StatusSent:       2,
	StatusDeferred:   3,
	StatusDelivered:  4,
	StatusFailed:     5,
	StatusBounced:    6,
	StatusComplained: 7,
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	_, ok := rank[s]
	return ok
}

// Terminal reports whether s is a final state that should never transition
// further. (`sent`/`queued`/`deferred` are non-terminal; the rest are final.)
func (s Status) Terminal() bool {
	switch s {
	case StatusDelivered, StatusBounced, StatusComplained, StatusFailed:
		return true
	}
	return false
}

// Merge returns the status that should result from observing `incoming` while
// currently at `current`, applying the monotonic precedence: the higher-ranked
// status wins, so a transition only ever moves "up". An unknown current (e.g.
// empty) is treated as the lowest rank so any valid incoming status applies.
func Merge(current, incoming Status) Status {
	if !incoming.Valid() {
		return current
	}
	if rank[incoming] > rank[current] {
		return incoming
	}
	return current
}

// FailureSource is the provenance of a terminal `failed`: who established it.
// It is recorded on the message row (messages.delivery_failure_source) when
// `failed` is written and gates the async-send-contract §3.1 correction rule —
// only a locally inferred failure may be corrected by later provider evidence.
type FailureSource string

const (
	// FailureSourceLocal marks a failure e2a inferred without the provider
	// confirming a rejection: retries exhausted on ambiguous errors, the
	// outage retry horizon elapsed, the terminal reconciler swept a dead job,
	// or a queued send was canceled by trash. These are exactly the failures
	// the SMTP-accept↔mark-sent crash window can falsify, so they remain
	// correctable by authoritatively correlated provider-accept evidence.
	FailureSourceLocal FailureSource = "local"
	// FailureSourceProvider marks a failure the provider itself confirmed
	// (permanent SMTP 5xx on submit, SES Reject notification). The provider
	// told us it did not accept this submission — never corrected.
	FailureSourceProvider FailureSource = "provider"
)

// Correctable reports whether a stored `failed` with this provenance may be
// corrected by authoritatively correlated provider feedback (§3.1). An
// unknown/empty provenance (rows failed before provenance existed) is treated
// as locally inferred: those legacy rows are precisely the falsely-failed
// crash-window rows the correction rule exists for, and correction still
// requires authoritative per-message correlation plus envelope membership, so
// unrelated feedback can never revive a genuine failure through this default.
func (fs FailureSource) Correctable() bool { return fs != FailureSourceProvider }

// ProvesProviderAcceptance reports whether s is provider feedback that implies
// the provider accepted the submission: `sent` and every per-recipient outcome
// that can only follow an accepted submission. `failed` is excluded (an SES
// Reject explicitly means the submission was NOT accepted), as are the local
// pre-send states.
func (s Status) ProvesProviderAcceptance() bool {
	switch s {
	case StatusSent, StatusDeferred, StatusDelivered, StatusBounced, StatusComplained:
		return true
	}
	return false
}

// ResolveMessageRollup decides the messages.delivery_status that results from
// re-rolling up per-recipient provider feedback while the row currently holds
// `current` with failure provenance `source`. It implements the
// async-send-contract §3.1 correction exception:
//
//   - a non-failed current keeps today's semantics — the recipient rollup is
//     authoritative;
//   - a locally inferred (or legacy unknown-provenance) `failed` is CORRECTED
//     by a rollup that proves the provider accepted the submission (the
//     falsely-declared terminal failure from a final-attempt crash);
//   - a provider-confirmed `failed` is never revived by sent/delivered, but a
//     stronger bounced/complained provider disposition still advances it.
//
// Callers must only feed it rollups computed from authoritatively correlated
// feedback (provider_message_id match or the SNS-verified X-E2A-Message-ID
// header echo) for the message's own recipients — correlation strength is the
// caller's gate, provenance is this function's.
func ResolveMessageRollup(current Status, source FailureSource, rollup Status) Status {
	if current != StatusFailed {
		return rollup
	}
	if rank[rollup] > rank[StatusFailed] {
		return rollup
	}
	if source.Correctable() && rollup.ProvesProviderAcceptance() {
		return rollup
	}
	return StatusFailed
}
