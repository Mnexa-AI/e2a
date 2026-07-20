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
