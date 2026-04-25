package emailauth

import (
	"net"
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
