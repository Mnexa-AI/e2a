// Package senderidentity manages the per-domain SES sending identity that
// lets outbound mail use the agent's OWN address as the From header
// (decision 4 / Slice 4). Verification is asynchronous: a domain moves
// none → pending → verified|failed, driven by a River-backed provision
// job + a periodic reconciler. The own-address From is used ONLY when the
// domain reaches `verified` (fail-closed); every other state falls back to
// the relay From, so the whole subsystem is behavior-neutral until a
// Provider actually verifies a domain.
//
// The Provider abstraction keeps the AWS SES SDK at the edge: the workers,
// store, and handlers speak this interface, and tests use the in-memory
// fake. The real sesv2 implementation (ses.go) is exercised only against
// live AWS; everything else is unit/integration tested with the fake.
package senderidentity

import (
	"context"
	"errors"
)

// Status is the verification state of a domain's sending identity. It maps
// 1:1 onto the domains.sending_status column.
type Status string

const (
	// StatusNone — no sending identity registered. Default for every
	// domain; self-host / SES-not-configured deployments stay here, which
	// keeps outbound on the relay From (fail-closed).
	StatusNone Status = "none"
	// StatusPending — identity registered with SES (BYODKIM), awaiting
	// asynchronous verification. The reconciler polls until it resolves.
	StatusPending Status = "pending"
	// StatusVerified — SES confirmed the identity; own-address From is now
	// used for this domain's agents.
	StatusVerified Status = "verified"
	// StatusFailed — verification failed, or `pending` exceeded its TTL.
	// Carries an actionable reason; outbound stays on the relay From.
	StatusFailed Status = "failed"
)

// Valid reports whether s is one of the four known states.
func (s Status) Valid() bool {
	switch s {
	case StatusNone, StatusPending, StatusVerified, StatusFailed:
		return true
	}
	return false
}

// DNSRecord is a single record the customer must publish for the sending
// identity. With BYODKIM the customer already published the per-domain DKIM
// record during register/verify, so this is usually empty; it exists for a
// future custom MAIL FROM subdomain (SPF alignment) and to surface anything
// SES reports as still-required.
type DNSRecord struct {
	Type  string `json:"type"`  // "TXT" | "CNAME" | "MX"
	Name  string `json:"name"`  // record host
	Value string `json:"value"` // record value
}

// Result is what a Provider reports for a domain.
type Result struct {
	Status     Status      `json:"status"`
	Error      string      `json:"error,omitempty"`
	DNSRecords []DNSRecord `json:"dns_records,omitempty"`
}

// ErrIdentityNotFound is what Status returns when the provider has no
// identity for the domain (e.g. it was deprovisioned out of band). Callers
// treat it as "drop back to none/failed", never as a hard error.
var ErrIdentityNotFound = errors.New("senderidentity: identity not found")

// Provider registers, polls, and removes the upstream (SES) sending
// identity for a domain. Implementations MUST be idempotent: Provision on an
// already-registered domain is a no-op success, and Deprovision treats a
// missing identity as success.
type Provider interface {
	// Provision registers a BYODKIM sending identity for domain, supplying
	// the per-domain DKIM selector + PKCS#1 DER private key that e2a
	// already generated (so DKIM d= aligns with the From domain). Returns
	// the initial Result — typically StatusPending — or an error to retry.
	Provision(ctx context.Context, domain, dkimSelector string, dkimPrivateKeyDER []byte) (Result, error)

	// Status polls the current verification state from the provider.
	// Returns ErrIdentityNotFound if no identity exists for domain.
	Status(ctx context.Context, domain string) (Result, error)

	// Deprovision removes the sending identity. A missing identity MUST be
	// reported as success (idempotent teardown).
	Deprovision(ctx context.Context, domain string) error

	// List returns the domains for which e2a currently has a sending
	// identity at the provider. Used by the orphan reaper to alert on
	// identities with no backing live domain row. SES caps each page; the
	// implementation paginates and returns the full set.
	List(ctx context.Context) ([]string, error)
}
