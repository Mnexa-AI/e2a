// Package inboundpolicy evaluates a per-agent inbound trust policy
// (api-v1-redesign decision 10 / Slice 7). It is gateway-ENFORCED, not advisory
// guidance an agent author can skip.
//
// Two orthogonal axes compose (decision 10 says the postures "compose", which a
// single enum can't express):
//   - Ingestion gate (this package): inbound_policy ∈ {open, allowlist, domain}.
//     Decides, on arrival, whether a message is trusted or FLAGGED. Flagged
//     messages are still delivered (never dropped) + emit email.flagged so
//     nothing disappears and operators get a signal. (A DMARC-alignment
//     "verified_only" posture was removed pre-GA; it may return as an additive
//     policy later.)
//   - Action gate (the protection policy): holds suspicious outbound as
//     pending_review, configured via the agent's outbound gate action / content
//     scan (the old hitl_enabled flag was retired).
//
// This package is a stdlib-only leaf: it takes primitives (policy, allowlist,
// sender, the DMARC verdict string) so callers (relay, store) don't couple to
// emailauth/identity.
package inboundpolicy

import "strings"

// Ingestion policy values (the inbound_policy column).
const (
	// Open accepts all inbound; no flagging. The default.
	Open = "open"
	// Allowlist accepts only senders whose exact address is on inbound_allowlist;
	// others are flagged (a TRUST gate — known senders).
	Allowlist = "allowlist"
	// Domain accepts only senders whose domain is on inbound_allowlist; others
	// are flagged (a TRUST gate — known domains).
	Domain = "domain"
)

// Valid reports whether p is a known ingestion policy.
func Valid(p string) bool {
	switch p {
	case Open, Allowlist, Domain:
		return true
	}
	return false
}

// Decision is the ingestion verdict for one inbound message.
type Decision struct {
	Flagged bool
	Reason  string // human-readable; empty when not flagged
}

// unresolvableSenderReason is the fail-closed flag reason for a sender with no
// specific authenticated identity (shared-relay "via e2a" mail) under a gating
// policy — see senderResolvable in the relay and issue #299.
const unresolvableSenderReason = "sender has no resolvable per-agent identity (shared relay), so it cannot match a per-agent inbound gate"

// EvaluateIngestion applies the agent's ingestion policy to an inbound message.
//   - policy: the agent's inbound_policy (unknown/empty → treated as Open).
//   - allowlist: the agent's inbound_allowlist (addresses for Allowlist,
//     domains for Domain); matching is case-insensitive.
//   - senderEmail: the message's display sender (From identity).
//   - senderResolvable: whether senderEmail maps to a SPECIFIC authenticated
//     sender. False for mail relayed under the shared "via e2a" address, which
//     authenticates (DMARC passes for the relay domain) but carries no per-agent
//     identity (#299). An unresolvable sender can never legitimately satisfy a
//     per-agent allowlist/domain gate, so it is treated as a non-match. Open is
//     unaffected — open means open.
//
// Flagged is fail-closed for the gating postures: an empty/garbage sender, an
// empty allowlist, or an unresolvable sender flags everything (you opted into a
// gate but the sender cannot be matched).
func EvaluateIngestion(policy string, allowlist []string, senderEmail string, senderResolvable bool) Decision {
	switch policy {
	case Allowlist:
		if !senderResolvable {
			return Decision{Flagged: true, Reason: unresolvableSenderReason}
		}
		if containsFold(allowlist, strings.TrimSpace(senderEmail)) {
			return Decision{}
		}
		return Decision{Flagged: true, Reason: "sender not on the agent's inbound allowlist"}
	case Domain:
		if !senderResolvable {
			return Decision{Flagged: true, Reason: unresolvableSenderReason}
		}
		dom := domainOf(senderEmail)
		if dom != "" && containsFold(allowlist, dom) {
			return Decision{}
		}
		return Decision{Flagged: true, Reason: "sender domain not on the agent's inbound allowlist"}
	default: // Open or unknown → never flag
		return Decision{}
	}
}

func containsFold(list []string, v string) bool {
	if v == "" {
		return false
	}
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), v) {
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
	return strings.TrimSpace(email[at+1:])
}
