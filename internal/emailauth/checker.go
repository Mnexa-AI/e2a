package emailauth

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"strings"

	"blitiri.com.ar/go/spf"
	"github.com/emersion/go-msgauth/dkim"
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

// Result aggregates SPF and DKIM check results.
type Result struct {
	SPF  CheckResult `json:"spf"`
	DKIM CheckResult `json:"dkim"`
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
	}
	return strings.Join(parts, "; ")
}

// Check runs SPF and DKIM verification on an inbound message.
// remoteIP is the IP address of the SMTP client that connected to us.
// senderEmail is the envelope sender (MAIL FROM).
// rawMessage is the full RFC 2822 message including headers.
func Check(remoteIP net.IP, senderEmail string, rawMessage []byte) *Result {
	result := &Result{}

	// Run SPF and DKIM checks
	result.SPF = checkSPF(remoteIP, senderEmail)
	result.DKIM = checkDKIM(rawMessage)

	log.Printf("Email auth for %s from %s: SPF=%s DKIM=%s", senderEmail, remoteIP, result.SPF.Status, result.DKIM.Status)
	return result
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

func checkDKIM(rawMessage []byte) CheckResult {
	verifications, err := dkim.Verify(bytes.NewReader(rawMessage))
	if err != nil {
		return CheckResult{Status: StatusTempError, Detail: err.Error()}
	}

	if len(verifications) == 0 {
		return CheckResult{Status: StatusNone, Detail: "no DKIM signature found"}
	}

	// At least one DKIM signature must pass
	for _, v := range verifications {
		if v.Err == nil {
			return CheckResult{Status: StatusPass, Detail: fmt.Sprintf("domain %s", v.Domain)}
		}
	}

	// All signatures failed
	firstErr := verifications[0].Err
	return CheckResult{Status: StatusFail, Detail: firstErr.Error()}
}

func extractDomain(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
