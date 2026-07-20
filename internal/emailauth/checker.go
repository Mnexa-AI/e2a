package emailauth

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"net/mail"
	"strings"

	"blitiri.com.ar/go/spf"
	"github.com/emersion/go-msgauth/dkim"
	"golang.org/x/net/publicsuffix"
)

var defaultDMARCEvaluator = newDMARCEvaluator(netTXTResolver{resolver: net.DefaultResolver})

// CheckStatus is retained while legacy callers migrate to Status.
type CheckStatus = Status

// CheckResult holds the outcome of a single authentication check.
type CheckResult struct {
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// AuthVerdict aggregates SPF, DKIM, and DMARC check results. It is the inbound
// trust primitive surfaced as `auth: {spf,dkim,dmarc}` on inbound messages and
// (Slice 7) enforced on by the inbound policy.
type AuthVerdict struct {
	SPF   CheckResult `json:"spf"`
	DKIM  CheckResult `json:"dkim"`
	DMARC CheckResult `json:"dmarc"`
}

// DomainAuthenticated returns true if at least one domain-level check passed.
func (r *AuthVerdict) DomainAuthenticated() bool {
	return r.SPF.Status == StatusPass || r.DKIM.Status == StatusPass
}

// Summary returns a short string describing the results, suitable for an auth header.
func (r *AuthVerdict) Summary() string {
	parts := []string{
		fmt.Sprintf("spf=%s", r.SPF.Status),
		fmt.Sprintf("dkim=%s", r.DKIM.Status),
		fmt.Sprintf("dmarc=%s", r.DMARC.Status),
	}
	return strings.Join(parts, "; ")
}

// Check is the compatibility entry point used while relay and persistence
// callers migrate to Authentication. New code should call CheckAuthentication.
// remoteIP is the IP address of the SMTP client that connected to us.
// senderEmail is the envelope sender (MAIL FROM).
// rawMessage is the full RFC 2822 message including headers.
func Check(remoteIP net.IP, senderEmail string, rawMessage []byte) *AuthVerdict {
	authentication := CheckAuthentication(context.Background(), remoteIP, senderEmail, rawMessage)
	return &AuthVerdict{
		SPF:   CheckResult{Status: authentication.SPF.Status, Detail: authentication.SPF.Detail},
		DKIM:  aggregateLegacyDKIM(authentication.DKIM),
		DMARC: CheckResult{Status: authentication.DMARC.Status, Detail: authentication.DMARC.Detail},
	}
}

// CheckAuthentication evaluates and retains the complete SPF, DKIM, and DMARC
// evidence for an inbound message.
func CheckAuthentication(ctx context.Context, remoteIP net.IP, envelopeFrom string, rawMessage []byte) *Authentication {
	return checkWithEvaluator(ctx, defaultDMARCEvaluator, remoteIP, envelopeFrom, rawMessage)
}

func checkWithResolver(ctx context.Context, resolver TXTResolver, remoteIP net.IP, envelopeFrom string, rawMessage []byte) *Authentication {
	return checkWithEvaluator(ctx, newDMARCEvaluator(resolver), remoteIP, envelopeFrom, rawMessage)
}

func checkWithEvaluator(ctx context.Context, evaluator *dmarcEvaluator, remoteIP net.IP, envelopeFrom string, rawMessage []byte) *Authentication {
	authentication := &Authentication{
		SPF:  checkSPF(remoteIP, envelopeFrom),
		DKIM: checkDKIM(rawMessage),
	}
	evaluator.evaluateAuthentication(ctx, fromHeaderDomain(rawMessage), authentication)
	log.Printf("Email auth for %s from %s: SPF=%s DKIM=%d DMARC=%s", envelopeFrom, remoteIP, authentication.SPF.Status, len(authentication.DKIM), authentication.DMARC.Status)
	return authentication

}

func aggregateLegacyDKIM(results []DKIMResult) CheckResult {
	if len(results) == 0 {
		return CheckResult{Status: StatusNone, Detail: "no DKIM signature found"}
	}
	for _, result := range results {
		if result.Status == StatusPass {
			return CheckResult{Status: result.Status, Detail: result.Detail}
		}
	}
	return CheckResult{Status: results[0].Status, Detail: results[0].Detail}
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

func checkSPF(remoteIP net.IP, senderEmail string) SPFResult {
	if remoteIP == nil {
		return SPFResult{Status: StatusNone, Detail: "no remote IP available"}
	}

	domain := extractDomain(senderEmail)
	if domain == "" {
		return SPFResult{Status: StatusPermError, Detail: "cannot extract domain from sender"}
	}

	result, err := spf.CheckHostWithSender(remoteIP, domain, senderEmail)
	return mapSPFResult(result, err, domain)
}

func mapSPFResult(result spf.Result, err error, domain string) SPFResult {
	domain = normDomain(domain)
	mapped := SPFResult{Domain: stringPtr(domain)}
	switch result {
	case spf.Pass:
		mapped.Status, mapped.Detail = StatusPass, fmt.Sprintf("domain %s", domain)
	case spf.Fail:
		mapped.Status, mapped.Detail = StatusFail, fmt.Sprintf("domain %s", domain)
	case spf.SoftFail:
		mapped.Status, mapped.Detail = StatusSoftFail, fmt.Sprintf("domain %s", domain)
	case spf.Neutral:
		mapped.Status, mapped.Detail = StatusNeutral, fmt.Sprintf("domain %s", domain)
	case spf.None:
		mapped.Status, mapped.Detail = StatusNone, fmt.Sprintf("no SPF record for %s", domain)
	case spf.TempError:
		mapped.Status, mapped.Detail = StatusTempError, "temporary DNS error"
	case spf.PermError:
		mapped.Status, mapped.Detail = StatusPermError, "SPF record could not be evaluated"
	default:
		mapped.Status, mapped.Detail = StatusPermError, "unknown SPF result"
	}
	if err != nil {
		detail := fmt.Sprintf("domain %s", domain)
		if mapped.Detail != "" {
			detail = mapped.Detail
		}
		mapped.Detail = detail + ": " + err.Error()
	}
	return mapped
}

// checkDKIM returns one result per DKIM-Signature field. A body-length-limited
// signature is refused individually so it cannot invalidate an independent,
// safe signature on the same message.
func checkDKIM(rawMessage []byte) []DKIMResult {
	tags := dkimSignatureTags(rawMessage)
	verifications, err := dkim.Verify(bytes.NewReader(rawMessage))
	return mapDKIMResults(verifications, tags, err)
}

func mapDKIMResults(verifications []*dkim.Verification, tags []map[string]string, verifyErr error) []DKIMResult {
	count := len(verifications)
	if len(tags) > count {
		count = len(tags)
	}
	results := make([]DKIMResult, 0, count)
	for i := 0; i < count; i++ {
		var verification *dkim.Verification
		if i < len(verifications) {
			verification = verifications[i]
		}
		var signatureTags map[string]string
		if i < len(tags) {
			signatureTags = tags[i]
		}
		result := DKIMResult{}
		if domain := normDomain(signatureTags["d"]); domain != "" {
			result.Domain = stringPtr(domain)
		}
		if selector := strings.TrimSpace(signatureTags["s"]); selector != "" {
			result.Selector = stringPtr(selector)
		}
		if verification != nil && verification.Domain != "" {
			result.Domain = stringPtr(normDomain(verification.Domain))
		}
		if _, limited := signatureTags["l"]; limited {
			result.Status = StatusPolicy
			result.Detail = "DKIM-Signature includes l= body-length tag (refused: would allow tail-content injection)"
			results = append(results, result)
			continue
		}
		resultErr := verifyErr
		if verification != nil {
			resultErr = verification.Err
		}
		switch {
		case resultErr == nil && verification != nil:
			result.Status = StatusPass
			if result.Domain != nil {
				result.Detail = "domain " + *result.Domain
			}
		case dkim.IsTempFail(resultErr):
			result.Status, result.Detail = StatusTempError, resultErr.Error()
		case dkim.IsPermFail(resultErr):
			result.Status, result.Detail = StatusPermError, resultErr.Error()
		default:
			result.Status = StatusFail
			if resultErr != nil {
				result.Detail = resultErr.Error()
			} else {
				result.Detail = "DKIM signature could not be evaluated"
			}
		}
		results = append(results, result)
	}
	return results
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
	for _, tags := range dkimSignatureTags(rawMessage) {
		if _, ok := tags["l"]; ok {
			return true
		}
	}
	return false
}

// dkimSignatureTags returns tag maps in header order so they can be correlated
// with go-msgauth's ordered verification results. Header folding is unfolded
// before parsing.
func dkimSignatureTags(rawMessage []byte) []map[string]string {
	// Read the header block: lines up to (but not including) the first
	// empty line. RFC 5322 line ending is CRLF but we tolerate LF.
	scanner := bufio.NewScanner(bytes.NewReader(rawMessage))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var signatures []map[string]string
	var currentHeader strings.Builder
	inDKIM := false

	flush := func() {
		if !inDKIM {
			return
		}
		signatures = append(signatures, parseTagValues(currentHeader.String()))
		currentHeader.Reset()
		inDKIM = false
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			return signatures
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
		flush()
		// Case-insensitive prefix match per RFC 5322.
		if colon := strings.IndexByte(line, ':'); colon > 0 {
			name := strings.TrimSpace(line[:colon])
			if strings.EqualFold(name, "DKIM-Signature") {
				inDKIM = true
				currentHeader.WriteString(strings.TrimLeft(line[colon+1:], " \t"))
			}
		}
	}
	flush()
	return signatures
}

// tagValueContainsKey reports whether a DKIM tag-value string (the
// part after `DKIM-Signature:`) carries the given key. RFC 6376 §3.2
// format: tag = name "=" value, entries separated by `;`, with
// optional FWS around the `=` and trimming on both sides. We don't
// care about the value here, only the presence of the key.
func tagValueContainsKey(s, key string) bool {
	_, ok := parseTagValues(s)[key]
	return ok
}

func parseTagValues(s string) map[string]string {
	values := make(map[string]string)
	for _, entry := range strings.Split(s, ";") {
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(entry[:eq]))
		if name != "" {
			values[name] = strings.TrimSpace(entry[eq+1:])
		}
	}
	return values
}

func extractDomain(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
