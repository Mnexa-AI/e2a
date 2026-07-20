package emailauth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"blitiri.com.ar/go/spf"
	"github.com/emersion/go-msgauth/dkim"
)

func TestAuthenticationJSONShape(t *testing.T) {
	spfDomain, dkimDomain, selector := "mail.example.com", "example.com", "s1"
	policy := DMARCPolicyReject
	spfAligned, dkimAligned := true, true
	a := Authentication{
		SPF:  SPFResult{Status: StatusPass, Domain: &spfDomain, Aligned: &spfAligned},
		DKIM: []DKIMResult{{Status: StatusPass, Domain: &dkimDomain, Selector: &selector, Aligned: &dkimAligned}},
		DMARC: DMARCResult{
			Status:    StatusPass,
			Domain:    &dkimDomain,
			Policy:    &policy,
			AlignedBy: []AlignmentMechanism{AlignedBySPF, AlignedByDKIM},
		},
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["spf"] == nil || got["dkim"] == nil || got["dmarc"] == nil {
		t.Fatalf("shape = %s", b)
	}
}

func TestNonPassAlignmentIsNull(t *testing.T) {
	r := SPFResult{Status: StatusFail}
	if r.Aligned != nil {
		t.Fatalf("aligned = %v, want nil", *r.Aligned)
	}
}

func TestMapSPFResultPreservesSoftFailAndNeutral(t *testing.T) {
	for _, tc := range []struct {
		input spf.Result
		want  Status
	}{{spf.SoftFail, StatusSoftFail}, {spf.Neutral, StatusNeutral}} {
		got := mapSPFResult(tc.input, nil, "example.com")
		if got.Status != tc.want {
			t.Fatalf("mapSPFResult(%q) = %q, want %q", tc.input, got.Status, tc.want)
		}
	}
}

func TestDKIMResultsRetainEverySignatureAndRefuseOnlyLengthLimitedSignature(t *testing.T) {
	verifications := []*dkim.Verification{
		{Domain: "safe.example", Err: nil},
		{Domain: "limited.example", Err: nil},
		{Domain: "failed.example", Err: errors.New("bad signature")},
	}
	tags := []map[string]string{
		{"d": "safe.example", "s": "safe"},
		{"d": "limited.example", "s": "limited", "l": "10"},
		{"d": "failed.example", "s": "failed"},
	}
	got := mapDKIMResults(verifications, tags, nil)
	if len(got) != 3 {
		t.Fatalf("len(results) = %d, want 3: %+v", len(got), got)
	}
	if got[0].Status != StatusPass || got[1].Status != StatusPolicy || got[2].Status != StatusFail {
		t.Fatalf("statuses = %q, %q, %q", got[0].Status, got[1].Status, got[2].Status)
	}
	if got[0].Selector == nil || *got[0].Selector != "safe" {
		t.Fatalf("selector = %v", got[0].Selector)
	}
}

func TestAuthenticationPassRequiresDMARCAlignment(t *testing.T) {
	r := &fakeTXTResolver{records: map[string][]string{
		"_dmarc.trusted.com": {"v=DMARC1; p=reject"},
	}}
	spfDomain := "evil.com"
	authentication := &Authentication{
		SPF:  SPFResult{Status: StatusPass, Domain: &spfDomain},
		DKIM: []DKIMResult{},
	}
	newDMARCEvaluator(r).evaluateAuthentication(context.Background(), "trusted.com", authentication)
	if authentication.DMARC.Status != StatusFail || authentication.Passed() {
		t.Fatalf("authentication = %+v", authentication)
	}
	if authentication.SPF.Aligned == nil || *authentication.SPF.Aligned {
		t.Fatalf("SPF alignment = %v, want false", authentication.SPF.Aligned)
	}
}

func TestAuthenticationAlignedDKIMPassesDMARC(t *testing.T) {
	r := &fakeTXTResolver{records: map[string][]string{
		"_dmarc.example.com": {"v=DMARC1; p=reject"},
	}}
	domain, selector := "mail.example.com", "s1"
	authentication := &Authentication{
		SPF:  SPFResult{Status: StatusNone},
		DKIM: []DKIMResult{{Status: StatusPass, Domain: &domain, Selector: &selector}},
	}
	newDMARCEvaluator(r).evaluateAuthentication(context.Background(), "example.com", authentication)
	if !authentication.Passed() {
		t.Fatalf("authentication = %+v", authentication)
	}
	if authentication.DKIM[0].Aligned == nil || !*authentication.DKIM[0].Aligned {
		t.Fatalf("DKIM alignment = %v, want true", authentication.DKIM[0].Aligned)
	}
}

func TestCheckNoRemoteIP(t *testing.T) {
	result := Check(nil, "alice@example.com", []byte("From: alice@example.com\r\n\r\nHello"))
	if result.SPF.Status != StatusNone {
		t.Errorf("SPF status = %q, want %q", result.SPF.Status, StatusNone)
	}
	if result.DKIM.Status != StatusNone {
		t.Errorf("DKIM status = %q, want %q", result.DKIM.Status, StatusNone)
	}
	if result.DomainAuthenticated() {
		t.Error("expected DomainAuthenticated() = false with no IP and no DKIM")
	}
}

func TestCheckNoSPFRecord(t *testing.T) {
	// Use a domain that almost certainly has no SPF record
	result := Check(net.ParseIP("127.0.0.1"), "alice@this-domain-does-not-exist-e2a-test.invalid", []byte("From: alice@test.com\r\n\r\nHello"))
	// Should be none or permerror, not pass
	if result.SPF.Status == StatusPass {
		t.Errorf("SPF should not pass for nonexistent domain")
	}
}

func TestCheckNoDKIMSignature(t *testing.T) {
	msg := []byte("From: alice@example.com\r\nTo: bot@agent.com\r\nSubject: Test\r\n\r\nHello")
	result := Check(net.ParseIP("127.0.0.1"), "alice@example.com", msg)
	if result.DKIM.Status != StatusNone {
		t.Errorf("DKIM status = %q, want %q for message without DKIM signature", result.DKIM.Status, StatusNone)
	}
}

func TestDomainAuthenticatedSPFPass(t *testing.T) {
	r := &AuthVerdict{
		SPF:  CheckResult{Status: StatusPass},
		DKIM: CheckResult{Status: StatusNone},
	}
	if !r.DomainAuthenticated() {
		t.Error("expected DomainAuthenticated() = true when SPF passes")
	}
}

func TestDomainAuthenticatedDKIMPass(t *testing.T) {
	r := &AuthVerdict{
		SPF:  CheckResult{Status: StatusNone},
		DKIM: CheckResult{Status: StatusPass},
	}
	if !r.DomainAuthenticated() {
		t.Error("expected DomainAuthenticated() = true when DKIM passes")
	}
}

func TestDomainAuthenticatedBothFail(t *testing.T) {
	r := &AuthVerdict{
		SPF:  CheckResult{Status: StatusFail},
		DKIM: CheckResult{Status: StatusFail},
	}
	if r.DomainAuthenticated() {
		t.Error("expected DomainAuthenticated() = false when both fail")
	}
}

func TestSummaryFormat(t *testing.T) {
	r := &AuthVerdict{
		SPF:   CheckResult{Status: StatusPass},
		DKIM:  CheckResult{Status: StatusNone},
		DMARC: CheckResult{Status: StatusFail},
	}
	summary := r.Summary()
	if summary != "spf=pass; dkim=none; dmarc=fail" {
		t.Errorf("summary = %q, want %q", summary, "spf=pass; dkim=none; dmarc=fail")
	}
}

// Regression for the DKIM `l=` tail-content injection vector. Honoring
// the length tag lets an attacker append arbitrary unsigned bytes
// after the signed portion of a legitimate signature; receivers that
// trust `dkim=pass` then process attacker-controlled content as if it
// were authenticated. checkDKIM must refuse the signature outright.
func TestCheckDKIMLengthTagRefused(t *testing.T) {
	// Minimal but realistic DKIM-Signature header carrying `l=`. The
	// signature value is junk — we never reach signature verification
	// because the l= refusal short-circuits earlier.
	msg := []byte("DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed; d=example.com;\r\n" +
		" s=selector1; t=1700000000; l=10;\r\n" +
		" h=From:To:Subject;\r\n" +
		" bh=base64bodyhash==;\r\n" +
		" b=base64signature==\r\n" +
		"From: alice@example.com\r\n" +
		"To: bot@e2a.dev\r\n" +
		"Subject: Hi\r\n" +
		"\r\n" +
		"AAAAAAAAAA[attacker-appended-content-past-signed-length]")

	results := checkDKIM(msg)
	if len(results) != 1 || results[0].Status != StatusPolicy {
		t.Fatalf("DKIM results = %+v, want one policy refusal", results)
	}
	if results[0].Detail == "" {
		t.Error("expected non-empty Detail explaining the l= refusal")
	}
}

func TestCheckDKIMNoLengthTagFallsThroughToVerify(t *testing.T) {
	// Same shape but without l=. We don't have a real signing key here,
	// so the underlying Verify will fail with a verification error
	// rather than the l= refusal. Asserting we move past the l= gate
	// (status != fail-with-l=-detail) — the actual verify result is a
	// fail or temperror, not the l= sentinel.
	msg := []byte("DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed; d=example.com;\r\n" +
		" s=selector1; t=1700000000;\r\n" +
		" h=From:To:Subject;\r\n" +
		" bh=base64bodyhash==;\r\n" +
		" b=base64signature==\r\n" +
		"From: alice@example.com\r\n" +
		"To: bot@e2a.dev\r\n" +
		"Subject: Hi\r\n" +
		"\r\n" +
		"body")

	results := checkDKIM(msg)
	if len(results) != 1 {
		t.Fatalf("DKIM results = %+v, want one", results)
	}
	if results[0].Status == StatusPolicy && strings.Contains(results[0].Detail, "l= body-length tag") {
		t.Errorf("DKIM refused with l= detail despite no l= tag in header: %q", results[0].Detail)
	}
}

func TestDKIMSignatureHasBodyLengthTag(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{
			name: "no DKIM header",
			msg:  "From: a@x\r\n\r\nbody",
			want: false,
		},
		{
			name: "DKIM without l=",
			msg:  "DKIM-Signature: v=1; a=rsa-sha256; d=example.com; s=s1; h=From; bh=x; b=y\r\nFrom: a@x\r\n\r\nbody",
			want: false,
		},
		{
			name: "DKIM with l= inline",
			msg:  "DKIM-Signature: v=1; l=42; d=example.com; s=s1; h=From; bh=x; b=y\r\nFrom: a@x\r\n\r\nbody",
			want: true,
		},
		{
			name: "DKIM with l= on folded continuation",
			msg:  "DKIM-Signature: v=1; a=rsa-sha256;\r\n l=200;\r\n d=example.com\r\nFrom: a@x\r\n\r\nbody",
			want: true,
		},
		{
			name: "DKIM with l= and surrounding whitespace",
			msg:  "DKIM-Signature: v=1; a=rsa-sha256;  l =  10 ; d=example.com\r\nFrom: a@x\r\n\r\nbody",
			want: true,
		},
		{
			name: "l appears as substring of another tag (lang, length-not-l)",
			msg:  "DKIM-Signature: v=1; lang=en; d=example.com; s=s1\r\nFrom: a@x\r\n\r\nbody",
			want: false,
		},
		{
			name: "two DKIM headers, second has l=",
			msg:  "DKIM-Signature: v=1; d=safe.com\r\nDKIM-Signature: v=1; l=5; d=bad.com\r\nFrom: a@x\r\n\r\nbody",
			want: true,
		},
		{
			name: "case-insensitive header name",
			msg:  "dkim-signature: v=1; l=10\r\nFrom: a@x\r\n\r\nbody",
			want: true,
		},
		{
			name: "LF-only line endings tolerated",
			msg:  "DKIM-Signature: v=1; l=10\nFrom: a@x\n\nbody",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dkimSignatureHasBodyLengthTag([]byte(tt.msg))
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		email  string
		domain string
	}{
		{"alice@gmail.com", "gmail.com"},
		{"bob@sub.example.com", "sub.example.com"},
		{"nope", ""},
	}
	for _, tt := range tests {
		got := extractDomain(tt.email)
		if got != tt.domain {
			t.Errorf("extractDomain(%q) = %q, want %q", tt.email, got, tt.domain)
		}
	}
}

// TestCheckDMARC covers the alignment verdict (relaxed org-domain) without
// needing live SPF/DKIM: it drives checkDMARC directly with crafted inputs.
func TestCheckDMARC(t *testing.T) {
	from := func(d string) []byte {
		return []byte("From: Sender <s@" + d + ">\r\nTo: a@b.com\r\nSubject: x\r\n\r\nhi")
	}
	pass := CheckResult{Status: StatusPass}
	none := CheckResult{Status: StatusNone}

	tests := []struct {
		name       string
		raw        []byte
		envelope   string
		spf, dkim  CheckResult
		dkimDomain string
		wantStatus CheckStatus
	}{
		{"dkim aligned exact", from("acme.com"), "bounce@x.com", none, pass, "acme.com", StatusPass},
		{"dkim aligned subdomain (relaxed org)", from("acme.com"), "bounce@x.com", none, pass, "mail.acme.com", StatusPass},
		{"dkim pass but unaligned", from("acme.com"), "bounce@x.com", none, pass, "evil.com", StatusFail},
		{"spf aligned", from("acme.com"), "notify@acme.com", pass, none, "", StatusPass},
		{"spf pass but unaligned envelope", from("acme.com"), "bounce@sendgrid.net", pass, none, "", StatusFail},
		{"spf aligned via org domain", from("acme.com"), "bounce@mail.acme.com", pass, none, "", StatusPass},
		{"neither aligned → fail", from("acme.com"), "x@other.com", pass, pass, "other.com", StatusFail},
		{"nothing attempted → none", from("acme.com"), "x@other.com", none, none, "", StatusNone},
		{"no From domain → none", []byte("To: a@b.com\r\n\r\nhi"), "x@acme.com", pass, none, "", StatusNone},
		// Hardening (adversarial review): a From that is itself a public suffix
		// must NOT self-align even with a matching d= (no org to attribute to).
		{"public-suffix From does not self-align", from("github.io"), "x@y.com", none, pass, "github.io", StatusFail},
		// Trailing-dot (absolute form) From: net/mail rejects it → conservative
		// none (not a spoof). normDomain still strips trailing dots on any
		// identifier that does reach alignment (e.g. a d= value).
		{"trailing-dot From → conservative none", from("acme.com."), "x@y.com", none, pass, "acme.com", StatusNone},
		// Documented relaxed-alignment limitation: distinct tenants under a
		// non-PSL shared parent align (a.wordpress.com ~ b.wordpress.com).
		{"shared non-PSL parent aligns (known relaxed gap)", from("a.wordpress.com"), "x@y.com", none, pass, "b.wordpress.com", StatusPass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkDMARC(tc.raw, tc.envelope, tc.spf, tc.dkim, tc.dkimDomain)
			if got.Status != tc.wantStatus {
				t.Errorf("DMARC = %q (%s), want %q", got.Status, got.Detail, tc.wantStatus)
			}
		})
	}
}
