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
//	complained > bounced > delivered > deferred > sent > sending > accepted
//
// plus `failed` as a terminal local outcome above all SES feedback. Higher
// rank wins a merge, so out-of-order or duplicate SNS events can never regress
// a terminal status (a late `delivered` never clobbers a `complained`).
// `accepted`/`sending` sit below `sent` so the async pre-send states never win
// over provider feedback. (async-send-contract.md §3.1.)
var rank = map[Status]int{
	StatusAccepted:   0,
	StatusQueued:     0, // legacy alias, equal rank to accepted
	StatusSending:    1,
	StatusSent:       2,
	StatusDeferred:   3,
	StatusDelivered:  4,
	StatusBounced:    5,
	StatusComplained: 6,
	StatusFailed:     7,
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
