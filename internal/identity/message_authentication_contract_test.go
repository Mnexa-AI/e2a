package identity

import (
	"encoding/json"
	"testing"
)

func TestMessageJSONUsesNullForUnavailableAuthenticationFields(t *testing.T) {
	b, err := json.Marshal(Message{Direction: "outbound"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"header_from", "envelope_from", "verified_domain", "authentication"} {
		value, ok := got[field]
		if !ok {
			t.Errorf("%s must be present", field)
		} else if value != nil {
			t.Errorf("%s = %#v, want null", field, value)
		}
	}
}

func TestUnmarshalAuthenticationFailsClosedForInboundSMTPWithoutEvidence(t *testing.T) {
	m := Message{Direction: "inbound", Method: "smtp"}
	if err := unmarshalAuthentication(nil, nil, &m); err != nil {
		t.Fatal(err)
	}
	if m.Authentication == nil {
		t.Fatal("inbound SMTP authentication must be present")
	}
	if got := m.Authentication.DMARC.Status; got != "permerror" {
		t.Fatalf("DMARC status = %q, want permerror", got)
	}
	if got := m.Authentication.SPF.Status; got != "permerror" {
		t.Fatalf("SPF status = %q, want permerror when evidence is unavailable", got)
	}
}

func TestUnmarshalAuthenticationPreservesLegacySPFEvidence(t *testing.T) {
	m := Message{Direction: "inbound", Method: "smtp"}
	legacy := []byte(`{"spf":{"status":"pass","detail":"legacy SPF pass"},"dkim":{"status":"pass"},"dmarc":{"status":"pass"}}`)
	if err := unmarshalAuthentication(nil, legacy, &m); err != nil {
		t.Fatal(err)
	}
	if m.Authentication == nil {
		t.Fatal("legacy inbound SMTP authentication must be present")
	}
	if got := m.Authentication.SPF.Status; got != "pass" {
		t.Fatalf("SPF status = %q, want preserved legacy pass", got)
	}
	if got := m.Authentication.SPF.Detail; got != "legacy SPF pass" {
		t.Fatalf("SPF detail = %q, want preserved legacy diagnostic", got)
	}
	if m.Authentication.SPF.Aligned != nil {
		t.Fatalf("legacy SPF alignment = %#v, want unknown", m.Authentication.SPF.Aligned)
	}
	if got := m.Authentication.DMARC.Status; got != "permerror" {
		t.Fatalf("DMARC status = %q, want fail-closed permerror", got)
	}
	if m.Authentication.Passed() {
		t.Fatal("legacy authentication must never be promoted to a pass")
	}
}

func TestUnmarshalAuthenticationRejectsUnknownLegacySPFStatus(t *testing.T) {
	m := Message{Direction: "inbound", Method: "smtp"}
	legacy := []byte(`{"spf":{"status":"future-value"},"dkim":{"status":"none"},"dmarc":{"status":"none"}}`)
	if err := unmarshalAuthentication(nil, legacy, &m); err != nil {
		t.Fatal(err)
	}
	if got := m.Authentication.SPF.Status; got != "permerror" {
		t.Fatalf("SPF status = %q, want closed-enum permerror", got)
	}
}

func TestUnmarshalAuthenticationLeavesProviderlessInboundNull(t *testing.T) {
	m := Message{Direction: "inbound", Method: "loopback"}
	if err := unmarshalAuthentication(nil, nil, &m); err != nil {
		t.Fatal(err)
	}
	if m.Authentication != nil {
		t.Fatalf("providerless authentication = %#v, want nil", m.Authentication)
	}
}
