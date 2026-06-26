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

// Fail-closed flag reasons.
const (
	// unresolvableSenderReason: the From has no per-agent identity — shared-relay
	// "via e2a" mail (#299). See senderResolvable in the relay.
	unresolvableSenderReason = "sender has no resolvable per-agent identity (shared relay), so it cannot match a per-agent inbound gate"
	// unauthenticatedSenderReason: require_authenticated is set and the From is not
	// DMARC-aligned-authenticated, so it may be spoofed and is not trusted (#318).
	unauthenticatedSenderReason = "sender From identity is not DMARC-authenticated and the gate requires authentication"
)

// Request is the per-message input to EvaluateIngestion. A struct (not positional
// args) because the gate composes several orthogonal predicates.
type Request struct {
	// Policy is the agent's inbound_policy (unknown/empty → treated as Open).
	Policy string
	// Allowlist is the agent's inbound_allowlist (addresses for Allowlist, domains
	// for Domain); matching is case-insensitive.
	Allowlist []string
	// SenderEmail is the message's From identity (the authenticated-from, not
	// Reply-To).
	SenderEmail string
	// SenderResolvable: false for mail relayed under the shared "via e2a" address,
	// which authenticates but carries no per-agent identity (#299). An unresolvable
	// sender can never satisfy a per-agent allowlist/domain gate.
	SenderResolvable bool
	// SenderAuthenticated: whether SenderEmail is DMARC-aligned-authenticated
	// (RFC 7489). Only consulted when RequireAuth is set.
	SenderAuthenticated bool
	// RequireAuth is the agent's opt-in inbound_require_auth flag (#318). When set,
	// an unauthenticated From is flagged regardless of policy — the composable,
	// additive anti-spoofing posture. Off by default (backward-compatible).
	RequireAuth bool
}

// EvaluateIngestion applies the agent's ingestion policy to an inbound message.
//
// Flagged is fail-closed for the gating postures: an empty/garbage sender, an
// empty allowlist, an unresolvable sender, or (when RequireAuth is set) an
// unauthenticated sender flags everything. Open never flags on policy alone, but
// RequireAuth still applies (it is an additive precondition across all policies).
func EvaluateIngestion(r Request) Decision {
	// Additive anti-spoofing precondition (#318): an opted-in agent never trusts an
	// unauthenticated From, on any policy. Open + require_auth ≈ the old
	// verified_only posture; allowlist/domain + require_auth additionally checks the
	// list for authenticated mail below.
	if r.RequireAuth && !r.SenderAuthenticated {
		return Decision{Flagged: true, Reason: unauthenticatedSenderReason}
	}
	switch r.Policy {
	case Allowlist:
		if !r.SenderResolvable {
			return Decision{Flagged: true, Reason: unresolvableSenderReason}
		}
		if containsFold(r.Allowlist, strings.TrimSpace(r.SenderEmail)) {
			return Decision{}
		}
		return Decision{Flagged: true, Reason: "sender not on the agent's inbound allowlist"}
	case Domain:
		if !r.SenderResolvable {
			return Decision{Flagged: true, Reason: unresolvableSenderReason}
		}
		dom := domainOf(r.SenderEmail)
		if dom != "" && containsFold(r.Allowlist, dom) {
			return Decision{}
		}
		return Decision{Flagged: true, Reason: "sender domain not on the agent's inbound allowlist"}
	default: // Open or unknown → never flag on policy (RequireAuth handled above)
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
