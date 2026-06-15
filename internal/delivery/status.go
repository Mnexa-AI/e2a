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
	StatusQueued     Status = "queued"     // accepted into the outbound path, not yet relayed
	StatusSent       Status = "sent"       // accepted by the relay (SES) — NON-terminal
	StatusDeferred   Status = "deferred"   // transient delay (SES deliveryDelay); poll, no event
	StatusDelivered  Status = "delivered"  // SES confirmed delivery to the recipient MTA
	StatusBounced    Status = "bounced"    // hard/soft bounce
	StatusComplained Status = "complained" // recipient marked spam (FBL complaint)
	StatusFailed     Status = "failed"     // local send failure (never reached the relay)
)

// rank orders statuses by the decision-9 monotonic precedence
//
//	complained > bounced > delivered > deferred > sent > queued
//
// plus `failed` as a terminal local outcome above all SES feedback. Higher
// rank wins a merge, so out-of-order or duplicate SNS events can never regress
// a terminal status (a late `delivered` never clobbers a `complained`).
var rank = map[Status]int{
	StatusQueued:     0,
	StatusSent:       1,
	StatusDeferred:   2,
	StatusDelivered:  3,
	StatusBounced:    4,
	StatusComplained: 5,
	StatusFailed:     6,
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
