package identity

import "strings"

// NormalizeEmail returns the canonical lookup form of an email address:
// lower-cased, with surrounding whitespace stripped. Every external-input
// email used as a lookup key (path vars, form fields, OAuth consent
// choices, WebSocket subscriptions) must funnel through this so case
// variants ("Alice@x.com" vs "alice@x.com") resolve to the same row.
//
// Per RFC 5321 §2.4 the local-part is technically case-sensitive — a
// small number of providers (most famously ProtonMail) preserve case.
// We collapse it anyway because consistency across HTTP path, JSON body,
// SMTP envelope, and dashboard URL is more important than spec purity
// for the agent-inbox use case. Document this if a user complains.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
