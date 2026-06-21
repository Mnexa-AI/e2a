// Package mailfrom defines the custom MAIL FROM subdomain convention shared by
// sender-identity provisioning (which configures it in SES and emits its DNS
// records) and the outbound sender (which uses it as the aligned Return-Path).
//
// One source of truth so three things always agree: what e2a tells SES the
// MAIL FROM domain is, the MX/SPF records e2a asks the customer to publish, and
// the envelope sender the relay actually uses. When a domain is sending-verified
// the Return-Path becomes `bounces@bounce.<domain>` — aligned to the From
// org-domain — so SPF aligns and the "via e2a" / "mailed-by" label disappears.
package mailfrom

// Label is the subdomain prefix for the custom MAIL FROM domain. A fixed
// convention for v1 (a per-deployment config knob is a trivial future addition —
// thread one string to NewSender + the SES provider).
const Label = "bounce"

// Domain returns the custom MAIL FROM domain for a sending domain, e.g.
// Domain("acme.com") == "bounce.acme.com".
func Domain(domain string) string { return Label + "." + domain }

// EnvelopeSender returns the SMTP MAIL FROM (Return-Path) address used for a
// verified domain's outbound mail, e.g. "bounces@bounce.acme.com". The local
// part is arbitrary for SES (bounces route via the subdomain's MX to SES's
// feedback handler); `bounces` is conventional and human-legible.
func EnvelopeSender(domain string) string { return "bounces@" + Domain(domain) }
