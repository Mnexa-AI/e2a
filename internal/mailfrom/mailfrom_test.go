package mailfrom

import "testing"

func TestConvention(t *testing.T) {
	if got := Domain("acme.com"); got != "bounce.acme.com" {
		t.Errorf("Domain = %q, want bounce.acme.com", got)
	}
	if got := EnvelopeSender("acme.com"); got != "bounces@bounce.acme.com" {
		t.Errorf("EnvelopeSender = %q, want bounces@bounce.acme.com", got)
	}
	// Subdomain of a subdomain stays well-formed (agent on a deeper domain).
	if got := Domain("mail.acme.co.uk"); got != "bounce.mail.acme.co.uk" {
		t.Errorf("Domain = %q", got)
	}
}
