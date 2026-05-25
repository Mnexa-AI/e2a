package emailauth

import (
	"net"
	"strings"
	"testing"
)

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
	r := &Result{
		SPF:  CheckResult{Status: StatusPass},
		DKIM: CheckResult{Status: StatusNone},
	}
	if !r.DomainAuthenticated() {
		t.Error("expected DomainAuthenticated() = true when SPF passes")
	}
}

func TestDomainAuthenticatedDKIMPass(t *testing.T) {
	r := &Result{
		SPF:  CheckResult{Status: StatusNone},
		DKIM: CheckResult{Status: StatusPass},
	}
	if !r.DomainAuthenticated() {
		t.Error("expected DomainAuthenticated() = true when DKIM passes")
	}
}

func TestDomainAuthenticatedBothFail(t *testing.T) {
	r := &Result{
		SPF:  CheckResult{Status: StatusFail},
		DKIM: CheckResult{Status: StatusFail},
	}
	if r.DomainAuthenticated() {
		t.Error("expected DomainAuthenticated() = false when both fail")
	}
}

func TestSummaryFormat(t *testing.T) {
	r := &Result{
		SPF:  CheckResult{Status: StatusPass},
		DKIM: CheckResult{Status: StatusNone},
	}
	summary := r.Summary()
	if summary != "spf=pass; dkim=none" {
		t.Errorf("summary = %q, want %q", summary, "spf=pass; dkim=none")
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

	result := checkDKIM(msg)
	if result.Status != StatusFail {
		t.Errorf("DKIM status = %q, want %q (l= must trigger refusal)", result.Status, StatusFail)
	}
	if result.Detail == "" {
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

	result := checkDKIM(msg)
	if result.Status == StatusFail && strings.Contains(result.Detail, "l= body-length tag") {
		t.Errorf("DKIM refused with l= detail despite no l= tag in header: %q", result.Detail)
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
