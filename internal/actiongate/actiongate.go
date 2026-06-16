// Package actiongate is the trust-gated outbound action authorization
// (api-v1-redesign decision 10 / Slice 7b). It is the OUTBOUND/action axis,
// orthogonal to the inbound ingestion gate (internal/inboundpolicy): given that
// HITL is enabled, it decides whether a specific outbound action must be HELD
// for human approval (pending_approval) rather than sent automatically.
//
// The gate composes two signals:
//   - untrusted input: the inbound message the action is reacting to is not
//     trustworthy. Today that's the server-owned DMARC verdict (dmarc != pass);
//     the signal is taken as a plain bool so a content-level prompt-injection
//     verdict (or any future provider) can OR into it WITHOUT changing this
//     package — the pluggable seam.
//   - high impact: the action reaches outside the referenced conversation — a
//     recipient on a domain that wasn't a participant of the inbound (a reply to
//     a new party, or a forward to a third party).
//
// Hold iff both hold AND the agent is in high_impact mode. In all mode every
// outbound is held (the pre-7b behavior). This package is a stdlib-only leaf:
// callers pass primitives (the mode, the two signals, address lists) so it
// stays decoupled from emailauth/identity.
package actiongate

import "strings"

// HITL sub-mode values (the agent_identities.hitl_mode column).
const (
	// ModeAll holds every outbound when HITL is enabled (pre-7b behavior, the
	// default so existing HITL agents are unchanged).
	ModeAll = "all"
	// ModeHighImpact holds only a high-impact action taken on untrusted input.
	ModeHighImpact = "high_impact"
)

// Valid reports whether m is a known hitl_mode.
func Valid(m string) bool { return m == ModeAll || m == ModeHighImpact }

// Decision is the gate verdict for one outbound action.
type Decision struct {
	Hold   bool
	Reason string // human-readable; empty when not held
}

// Evaluate decides whether an outbound action is held for approval. The caller
// has already confirmed HITL is enabled; this only chooses based on the mode.
//
//   - mode: the agent's hitl_mode.
//   - hasReferencedInput: the action reacts to a referenced inbound message
//     (reply/forward). A fresh cold send has none — there is no untrusted input
//     to react to, so high_impact mode never holds it.
//   - untrustedInput: that referenced input is untrusted (today: dmarc != pass).
//   - highImpact: the action reaches outside the referenced participants.
func Evaluate(mode string, hasReferencedInput, untrustedInput, highImpact bool) Decision {
	switch mode {
	case ModeHighImpact:
		if hasReferencedInput && untrustedInput && highImpact {
			return Decision{Hold: true, Reason: "high-impact action on unauthenticated inbound (hitl_mode=high_impact)"}
		}
		return Decision{}
	case ModeAll:
		return Decision{Hold: true, Reason: "all outbound held for approval (hitl_mode=all)"}
	default:
		// Unknown mode → fail closed to holding (never silently send when the
		// agent opted into HITL but the mode is unrecognized).
		return Decision{Hold: true, Reason: "unrecognized hitl_mode; holding for approval"}
	}
}

// HighImpact reports whether any outbound recipient targets a domain that was
// NOT a participant of the referenced inbound — i.e. the action reaches a new
// party (a reply to a new domain, or a forward to a third party). A recipient
// with no parseable domain counts as high-impact (fail-closed). Matching is
// case-insensitive on the domain.
func HighImpact(participantAddrs, recipientAddrs []string) bool {
	pd := map[string]bool{}
	for _, a := range participantAddrs {
		if d := domainOf(a); d != "" {
			pd[d] = true
		}
	}
	for _, r := range recipientAddrs {
		d := domainOf(r)
		if d == "" || !pd[d] {
			return true
		}
	}
	return false
}

func domainOf(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[at+1:]))
}
