package emailauth

import (
	"context"
	"errors"
	"testing"
)

type fakeTXTResolver struct {
	records map[string][]string
	errors  map[string]error
	lookups []string
}

func (f *fakeTXTResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	f.lookups = append(f.lookups, name)
	if err := f.errors[name]; err != nil {
		return nil, err
	}
	return f.records[name], nil
}

type temporaryDNSError struct{}

func (temporaryDNSError) Error() string   { return "temporary DNS failure" }
func (temporaryDNSError) Temporary() bool { return true }

func TestEvaluateDMARCAlignedDKIM(t *testing.T) {
	r := &fakeTXTResolver{records: map[string][]string{
		"_dmarc.example.com": {"v=DMARC1; p=reject"},
	}}
	dkimDomain, selector := "example.com", "s1"
	got := evaluateDMARC(context.Background(), r, "example.com",
		SPFResult{Status: StatusNone},
		[]DKIMResult{{Status: StatusPass, Domain: &dkimDomain, Selector: &selector}},
	)
	if got.Status != StatusPass || len(got.AlignedBy) != 1 || got.AlignedBy[0] != AlignedByDKIM {
		t.Fatalf("got %+v", got)
	}
	if got.Policy == nil || *got.Policy != DMARCPolicyReject {
		t.Fatalf("policy = %v", got.Policy)
	}
}

func TestEvaluateDMARCRejectsBarePublicSuffixAuthor(t *testing.T) {
	r := &fakeTXTResolver{records: map[string][]string{
		"_dmarc.github.io": {"v=DMARC1; p=reject"},
	}}
	domain := "github.io"
	got := evaluateDMARC(context.Background(), r, domain, SPFResult{Status: StatusNone}, []DKIMResult{{Status: StatusPass, Domain: &domain}})
	if got.Status == StatusPass {
		t.Fatalf("bare public suffix authenticated: %+v", got)
	}
}

func TestEvaluateDMARCDoesNotAlignPrivateSuffixTenants(t *testing.T) {
	r := &fakeTXTResolver{records: map[string][]string{
		"_dmarc.github.io": {"v=DMARC1; p=reject"},
	}}
	dkimDomain := "attacker.github.io"
	got := evaluateDMARC(context.Background(), r, "victim.github.io", SPFResult{Status: StatusNone}, []DKIMResult{{Status: StatusPass, Domain: &dkimDomain}})
	if got.Status != StatusFail {
		t.Fatalf("cross-tenant DMARC status = %q, want fail: %+v", got.Status, got)
	}
	for _, lookup := range r.lookups {
		if lookup == "_dmarc.io" {
			t.Fatalf("DMARC tree walk crossed the private public-suffix boundary: %v", r.lookups)
		}
	}
}

func TestEvaluateDMARCParentPolicyAndAlignmentModes(t *testing.T) {
	tests := []struct {
		name       string
		record     string
		spfDomain  string
		dkimDomain string
		want       Status
		wantPolicy DMARCPolicy
	}{
		{"relaxed SPF aligns", "v=DMARC1; p=reject; sp=quarantine", "bounce.example.com", "", StatusPass, DMARCPolicyQuarantine},
		{"strict SPF does not align", "v=DMARC1; p=reject; sp=quarantine; aspf=s", "bounce.example.com", "", StatusFail, DMARCPolicyQuarantine},
		{"relaxed DKIM aligns", "v=DMARC1; p=none", "", "sign.example.com", StatusPass, DMARCPolicyNone},
		{"strict DKIM does not align", "v=DMARC1; p=none; adkim=s", "", "sign.example.com", StatusFail, DMARCPolicyNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeTXTResolver{records: map[string][]string{
				"_dmarc.example.com": {tc.record},
			}}
			spf := SPFResult{Status: StatusNone}
			if tc.spfDomain != "" {
				spf = SPFResult{Status: StatusPass, Domain: &tc.spfDomain}
			}
			var dkimResults []DKIMResult
			if tc.dkimDomain != "" {
				dkimResults = []DKIMResult{{Status: StatusPass, Domain: &tc.dkimDomain}}
			}
			got := evaluateDMARC(context.Background(), r, "mail.example.com", spf, dkimResults)
			if got.Status != tc.want {
				t.Fatalf("status = %q, want %q (%+v)", got.Status, tc.want, got)
			}
			if got.Policy == nil || *got.Policy != tc.wantPolicy {
				t.Fatalf("policy = %v, want %q", got.Policy, tc.wantPolicy)
			}
		})
	}
}

func TestEvaluateDMARCDiscoveryErrors(t *testing.T) {
	tests := []struct {
		name     string
		resolver *fakeTXTResolver
		want     Status
	}{
		{"no record", &fakeTXTResolver{records: map[string][]string{}}, StatusNone},
		{"temporary DNS failure", &fakeTXTResolver{errors: map[string]error{"_dmarc.example.com": temporaryDNSError{}}}, StatusTempError},
		{"malformed record", &fakeTXTResolver{records: map[string][]string{"_dmarc.example.com": {"v=DMARC1; p=reject; p=none"}}}, StatusPermError},
		{"multiple records are discarded", &fakeTXTResolver{records: map[string][]string{"_dmarc.example.com": {"v=DMARC1; p=reject", "v=DMARC1; p=none"}}}, StatusNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateDMARC(context.Background(), tc.resolver, "example.com", SPFResult{Status: StatusNone}, nil)
			if got.Status != tc.want {
				t.Fatalf("status = %q, want %q (%+v)", got.Status, tc.want, got)
			}
		})
	}
}

func TestDiscoverDMARCUsesBoundedRFC9989TreeWalk(t *testing.T) {
	r := &fakeTXTResolver{records: map[string][]string{
		"_dmarc.example.com": {"v=DMARC1; p=reject"},
	}}
	discovery := discoverDMARCRecord(context.Background(), r, "a.b.c.d.e.f.g.h.i.j.example.com")
	if discovery.err != nil || discovery.record == nil {
		t.Fatalf("discovery = %+v", discovery)
	}
	if discovery.domain != "example.com" {
		t.Fatalf("domain = %q", discovery.domain)
	}
	if len(r.lookups) != dmarcMaxQueries {
		t.Fatalf("lookups = %d (%v), want %d", len(r.lookups), r.lookups, dmarcMaxQueries)
	}
	if r.lookups[1] != "_dmarc.f.g.h.i.j.example.com" {
		t.Fatalf("anti-abuse skip = %q", r.lookups[1])
	}
}

func TestDiscoverDMARCPSDStopsWalkAndSelectsChildOrganization(t *testing.T) {
	r := &fakeTXTResolver{records: map[string][]string{
		"_dmarc.bank.example": {"v=DMARC1; p=reject; psd=y"},
	}}
	discovery := discoverDMARCRecord(context.Background(), r, "giant.bank.example")
	if discovery.err != nil || discovery.record == nil {
		t.Fatalf("discovery = %+v", discovery)
	}
	if discovery.domain != "bank.example" || discovery.organizationalDomain != "giant.bank.example" {
		t.Fatalf("discovery = %+v", discovery)
	}
	if got := r.lookups[len(r.lookups)-1]; got != "_dmarc.bank.example" {
		t.Fatalf("walk did not stop at psd=y: %v", r.lookups)
	}
}

func TestParseDMARCRecord(t *testing.T) {
	record, err := parseDMARCRecord("v=DMARC1; p=reject; sp=quarantine; aspf=s; adkim=r")
	if err != nil {
		t.Fatal(err)
	}
	if record.Policy != DMARCPolicyReject || record.SubdomainPolicy == nil || *record.SubdomainPolicy != DMARCPolicyQuarantine || !record.SPFStrict || record.DKIMStrict {
		t.Fatalf("record = %+v", record)
	}
	if _, err := parseDMARCRecord("p=reject; v=DMARC1"); err == nil {
		t.Fatal("expected v tag ordering error")
	}
	if _, err := parseDMARCRecord("v=DMARC1; p=reject; p=none"); !errors.Is(err, errInvalidDMARCRecord) {
		t.Fatalf("duplicate p error = %v", err)
	}
}
