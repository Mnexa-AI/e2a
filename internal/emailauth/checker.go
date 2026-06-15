package emailauth

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"net/mail"
	"strings"

	"blitiri.com.ar/go/spf"
	"github.com/emersion/go-msgauth/dkim"
	"golang.org/x/net/publicsuffix"
)

// CheckStatus represents the result of an authentication check.
type CheckStatus string

const (
	StatusPass      CheckStatus = "pass"
	StatusFail      CheckStatus = "fail"
	StatusNone      CheckStatus = "none"
	StatusTempError CheckStatus = "temperror"
	StatusPermError CheckStatus = "permerror"
)

// CheckResult holds the outcome of a single authentication check.
type CheckResult struct {
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// Result aggregates SPF, DKIM, and DMARC check results. It is the inbound
// trust primitive surfaced as `auth: {spf,dkim,dmarc}` on inbound messages and
// (Slice 7) enforced on by the inbound policy.
type Result struct {
	SPF   CheckResult `json:"spf"`
	DKIM  CheckResult `json:"dkim"`
	DMARC CheckResult `json:"dmarc"`
}

// DomainAuthenticated returns true if at least one domain-level check passed.
func (r *Result) DomainAuthenticated() bool {
	return r.SPF.Status == StatusPass || r.DKIM.Status == StatusPass
}

// Summary returns a short string describing the results, suitable for an auth header.
func (r *Result) Summary() string {
	parts := []string{
		fmt.Sprintf("spf=%s", r.SPF.Status),
		fmt.Sprintf("dkim=%s", r.DKIM.Status),
		fmt.Sprintf("dmarc=%s", r.DMARC.Status),
	}
	return strings.Join(parts, "; ")
}

// Check runs SPF and DKIM verification on an inbound message.
// remoteIP is the IP address of the SMTP client that connected to us.
// senderEmail is the envelope sender (MAIL FROM).
// rawMessage is the full RFC 2822 message including headers.
func Check(remoteIP net.IP, senderEmail string, rawMessage []byte) *Result {
	result := &Result{}

	// Run SPF and DKIM checks, then derive DMARC from their alignment.
	result.SPF = checkSPF(remoteIP, senderEmail)
	var dkimDomain string
	result.DKIM, dkimDomain = checkDKIM(rawMessage)
	result.DMARC = checkDMARC(rawMessage, senderEmail, result.SPF, result.DKIM, dkimDomain)

	log.Printf("Email auth for %s from %s: SPF=%s DKIM=%s DMARC=%s", senderEmail, remoteIP, result.SPF.Status, result.DKIM.Status, result.DMARC.Status)
	return result
}

// checkDMARC derives a DMARC verdict from the SPF/DKIM results plus identifier
// alignment (RFC 7489): DMARC passes when an authenticated identifier is
// ALIGNED with the From-header domain — i.e. a passing DKIM whose d= aligns, or
// a passing SPF whose envelope (MAIL FROM) domain aligns. We use relaxed
// alignment (organizational-domain match, the DMARC default) and do NOT fetch
// the _dmarc policy record: the policy governs what to DO on failure
// (quarantine/reject — Slice 7's job), not the pass/fail verdict, which is what
// the trust primitive needs. No aligned pass while some auth was attempted →
// fail; nothing attempted / no From domain → none.
func checkDMARC(rawMessage []byte, envelopeSender string, spf, dkim CheckResult, dkimDomain string) CheckResult {
	fromDomain := fromHeaderDomain(rawMessage)
	if fromDomain == "" {
		return CheckResult{Status: StatusNone, Detail: "no From-header domain to align against"}
	}
	if dkim.Status == StatusPass && aligned(dkimDomain, fromDomain) {
		return CheckResult{Status: StatusPass, Detail: fmt.Sprintf("dkim-aligned (d=%s, from=%s)", dkimDomain, fromDomain)}
	}
	if spf.Status == StatusPass && aligned(extractDomain(envelopeSender), fromDomain) {
		return CheckResult{Status: StatusPass, Detail: fmt.Sprintf("spf-aligned (mailfrom=%s, from=%s)", extractDomain(envelopeSender), fromDomain)}
	}
	if spf.Status == StatusNone && dkim.Status == StatusNone {
		return CheckResult{Status: StatusNone, Detail: "no SPF or DKIM to align"}
	}
	return CheckResult{Status: StatusFail, Detail: "no aligned authenticated identifier for " + fromDomain}
}

// aligned reports relaxed (organizational-domain) alignment between two
// domains, the DMARC default. Exact match aligns; otherwise their eTLD+1 must
// match (so mail.example.com aligns with example.com).
//
// Guards (adversarial review): trailing dots are normalized so the absolute
// form acme.com. aligns with acme.com; a domain that is ITSELF a public suffix
// (e.g. github.io, co.uk) never aligns — there is no organization to attribute
// it to, so a bare-suffix From with a matching d= must not earn a pass.
//
// LIMITATION (relaxed alignment, by design): two distinct tenants under a
// non-PSL shared parent (e.g. a.wordpress.com vs b.wordpress.com) share an
// eTLD+1 and therefore align. This is the standard relaxed-DMARC shared-hosting
// gap; strict alignment (a deferred _dmarc-policy fetch) would close it.
func aligned(a, b string) bool {
	a, b = normDomain(a), normDomain(b)
	if a == "" || b == "" {
		return false
	}
	if isPublicSuffix(a) || isPublicSuffix(b) {
		return false
	}
	if a == b {
		return true
	}
	oa, ob := orgDomain(a), orgDomain(b)
	return oa != "" && oa == ob
}

func normDomain(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}

// isPublicSuffix reports whether d is itself a public suffix (ICANN or private),
// i.e. it has no registrable label of its own.
func isPublicSuffix(d string) bool {
	suffix, _ := publicsuffix.PublicSuffix(d)
	return suffix == d
}

func orgDomain(d string) string {
	if e, err := publicsuffix.EffectiveTLDPlusOne(d); err == nil {
		return e
	}
	return d
}

// fromHeaderDomain extracts the domain of the RFC 5322 From header (the
// identifier DMARC aligns against). Empty if absent/unparseable.
func fromHeaderDomain(rawMessage []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(rawMessage))
	if err != nil {
		return ""
	}
	addr, err := mail.ParseAddress(msg.Header.Get("From"))
	if err != nil {
		return ""
	}
	return extractDomain(addr.Address)
}

func checkSPF(remoteIP net.IP, senderEmail string) CheckResult {
	if remoteIP == nil {
		return CheckResult{Status: StatusNone, Detail: "no remote IP available"}
	}

	domain := extractDomain(senderEmail)
	if domain == "" {
		return CheckResult{Status: StatusPermError, Detail: "cannot extract domain from sender"}
	}

	result, err := spf.CheckHostWithSender(remoteIP, domain, senderEmail)
	switch result {
	case spf.Pass:
		return CheckResult{Status: StatusPass, Detail: fmt.Sprintf("domain %s", domain)}
	case spf.Fail, spf.SoftFail:
		detail := fmt.Sprintf("domain %s", domain)
		if err != nil {
			detail += ": " + err.Error()
		}
		return CheckResult{Status: StatusFail, Detail: detail}
	case spf.None:
		return CheckResult{Status: StatusNone, Detail: fmt.Sprintf("no SPF record for %s", domain)}
	case spf.TempError:
		detail := "temporary DNS error"
		if err != nil {
			detail = err.Error()
		}
		return CheckResult{Status: StatusTempError, Detail: detail}
	default:
		return CheckResult{Status: StatusPermError, Detail: "SPF check error"}
	}
}

// checkDKIM verifies DKIM and returns the result plus the signing domain (d=)
// of the passing signature (empty when none passed) — needed for DMARC
// alignment.
func checkDKIM(rawMessage []byte) (CheckResult, string) {
	// RFC 6376 §3.5 defines the `l=` tag (body length limit). Honoring
	// it lets an attacker append arbitrary unsigned content (HTML,
	// prompt-injection payloads) to a legitimately-signed message, and
	// the receiver still sees dkim=pass. Agents that act on
	// `verified=true` headers may then execute the attacker-tacked-on
	// instructions. We refuse any signature carrying `l=` outright.
	// Conservative trade-off: a sender that uses `l=` and a sender
	// that doesn't both fail if their messages reach us — but `l=` is
	// rare in modern signing configs (Gmail, M365, SES all omit it by
	// default).
	if dkimSignatureHasBodyLengthTag(rawMessage) {
		return CheckResult{Status: StatusFail, Detail: "DKIM-Signature includes l= body-length tag (refused: would allow tail-content injection)"}, ""
	}

	verifications, err := dkim.Verify(bytes.NewReader(rawMessage))
	if err != nil {
		return CheckResult{Status: StatusTempError, Detail: err.Error()}, ""
	}

	if len(verifications) == 0 {
		return CheckResult{Status: StatusNone, Detail: "no DKIM signature found"}, ""
	}

	// At least one DKIM signature must pass
	for _, v := range verifications {
		if v.Err == nil {
			return CheckResult{Status: StatusPass, Detail: fmt.Sprintf("domain %s", v.Domain)}, v.Domain
		}
	}

	// All signatures failed
	firstErr := verifications[0].Err
	return CheckResult{Status: StatusFail, Detail: firstErr.Error()}, ""
}

// dkimSignatureHasBodyLengthTag reports whether any DKIM-Signature
// header in rawMessage carries an `l=` tag. The tag is a semicolon-
// separated tag-value entry per RFC 6376 §3.5; whitespace and folding
// can occur freely, so we reassemble each multi-line header before
// parsing.
//
// We err on the side of refusal: any `l=` (whether the value matches
// the actual body length or not) is treated as suspicious because the
// attacker model is "extend the body past the signed length" — even
// `l=<actual_length>` becomes exploitable the moment a downstream
// MTA or replay re-frames the message.
func dkimSignatureHasBodyLengthTag(rawMessage []byte) bool {
	// Read the header block: lines up to (but not including) the first
	// empty line. RFC 5322 line ending is CRLF but we tolerate LF.
	scanner := bufio.NewScanner(bytes.NewReader(rawMessage))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentHeader strings.Builder
	inDKIM := false

	flushAndCheck := func() bool {
		if !inDKIM {
			return false
		}
		hit := tagValueContainsKey(currentHeader.String(), "l")
		currentHeader.Reset()
		inDKIM = false
		return hit
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// End of header block.
			if flushAndCheck() {
				return true
			}
			return false
		}
		// Folded continuation: starts with SP or HTAB.
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if inDKIM {
				currentHeader.WriteByte(' ')
				currentHeader.WriteString(strings.TrimLeft(line, " \t"))
			}
			continue
		}
		// New header line — finalize the previous one first.
		if flushAndCheck() {
			return true
		}
		// Case-insensitive prefix match per RFC 5322.
		if colon := strings.IndexByte(line, ':'); colon > 0 {
			name := strings.TrimSpace(line[:colon])
			if strings.EqualFold(name, "DKIM-Signature") {
				inDKIM = true
				currentHeader.WriteString(strings.TrimLeft(line[colon+1:], " \t"))
			}
		}
	}
	// EOF without a blank line — flush whatever's pending.
	if flushAndCheck() {
		return true
	}
	return false
}

// tagValueContainsKey reports whether a DKIM tag-value string (the
// part after `DKIM-Signature:`) carries the given key. RFC 6376 §3.2
// format: tag = name "=" value, entries separated by `;`, with
// optional FWS around the `=` and trimming on both sides. We don't
// care about the value here, only the presence of the key.
func tagValueContainsKey(s, key string) bool {
	for _, entry := range strings.Split(s, ";") {
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue
		}
		name := strings.TrimSpace(entry[:eq])
		if name == key {
			return true
		}
	}
	return false
}

func extractDomain(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
