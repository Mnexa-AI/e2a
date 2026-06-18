package httpapi

import (
	"testing"
)

// MED-3 — registering a domain another user already owns must surface as 409
// conflict (code domain_taken), not the 400 domain_unavailable used for
// malformed/other claim failures. The testServer ClaimDomain mock returns
// identity.ErrDomainTaken for "taken.com".
func TestRegisterDomainTakenConflict(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/domains", "good", map[string]any{"domain": "taken.com"})
	if code != 409 || errCode(body) != "domain_taken" {
		t.Fatalf("want 409 domain_taken, got %d %v", code, body)
	}
}
