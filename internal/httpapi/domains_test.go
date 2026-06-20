package httpapi

import (
	"net/http"
	"testing"
)

func TestListDomains(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/domains", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	doms, _ := body["items"].([]any)
	if len(doms) != 1 {
		t.Fatalf("want 1 domain, got %d", len(doms))
	}
	d := doms[0].(map[string]any)
	dns, _ := d["dns_records"].(map[string]any)
	mx, _ := dns["mx"].(map[string]any)
	if mx["value"] != "mx.e2a.dev" {
		t.Fatalf("unexpected MX record: %v", dns)
	}
}

func TestGetDomain(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/domains/acme.com", "good")
	if code != 200 || body["domain"] != "acme.com" || body["verified"] != true {
		t.Fatalf("unexpected get domain: %d %v", code, body)
	}
}

func TestGetDomainNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/domains/unknown.com", "good")
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestRegisterDomain(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/domains", "good", map[string]any{"domain": "new.com"})
	if code != 201 || body["domain"] != "new.com" {
		t.Fatalf("want 201 new.com, got %d %v", code, body)
	}
}

func TestRegisterDomainReserved(t *testing.T) {
	srv := testServer(t)
	// SharedDomain is "agents.e2a.dev" in the test server.
	code, body := postJSON(t, srv.URL+"/v1/domains", "good", map[string]any{"domain": "agents.e2a.dev"})
	if code != 400 || errCode(body) != "reserved_domain" {
		t.Fatalf("want 400 reserved_domain, got %d %v", code, body)
	}
}

func TestRegisterDomainInvalid(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/domains", "good", map[string]any{"domain": "nodot"})
	if code != 400 || errCode(body) != "invalid_domain" {
		t.Fatalf("want 400 invalid_domain, got %d %v", code, body)
	}
}

func TestRegisterDomainOverCap(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/domains", "overcap", map[string]any{"domain": "new.com"})
	if code != 402 || errCode(body) != "limit_exceeded" {
		t.Fatalf("want 402 limit_exceeded, got %d %v", code, body)
	}
}

func TestUpdateDomainSetPrimary(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/domains/acme.com", "good", map[string]any{"is_primary": true})
	if code != 200 || body["domain"] != "acme.com" {
		t.Fatalf("want 200, got %d %v", code, body)
	}
}

func TestUpdateDomainDemoteRejected(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/domains/acme.com", "good", map[string]any{"is_primary": false})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400, got %d %v", code, body)
	}
}

func TestUpdateDomainNoFields(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "PATCH", srv.URL+"/v1/domains/acme.com", "good", map[string]any{})
	if code != 400 || errCode(body) != "invalid_request" {
		t.Fatalf("want 400, got %d %v", code, body)
	}
}

func TestUpdateDomainNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "PATCH", srv.URL+"/v1/domains/missing.com", "good", map[string]any{"is_primary": true})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestDeleteDomain(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("DELETE", srv.URL+"/v1/domains/acme.com?confirm=DELETE", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
}

func TestDeleteDomainRequiresConfirm(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "DELETE", srv.URL+"/v1/domains/acme.com", "good", nil)
	if code != 400 || errCode(body) != "confirmation_required" {
		t.Fatalf("want 400 confirmation_required, got %d %v", code, body)
	}
}

func TestDeleteDomainWithAgents(t *testing.T) {
	srv := testServer(t)
	code, body := sendJSON(t, "DELETE", srv.URL+"/v1/domains/busy.com?confirm=DELETE", "good", nil)
	if code != 400 || errCode(body) != "domain_has_agents" {
		t.Fatalf("want 400 domain_has_agents, got %d %v", code, body)
	}
}

func TestDeleteDomainNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/domains/unknown.com?confirm=DELETE", "good", nil)
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestVerifyDomainAlreadyVerified(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/domains/acme.com/verify", "good", nil)
	if code != 200 || body["verified"] != true {
		t.Fatalf("want 200 verified, got %d %v", code, body)
	}
}

func TestVerifyDomainTXTMissing(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/domains/pending.com/verify", "good", nil)
	if code != 412 || body["verified"] != false {
		t.Fatalf("want 412 not-verified, got %d %v", code, body)
	}
	if body["mx"] != "missing" {
		t.Fatalf("expected diagnostic in 412 body, got %v", body)
	}
}

func TestVerifyDomainSuccess(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/domains/fresh.com/verify", "good", nil)
	if code != 200 || body["verified"] != true {
		t.Fatalf("want 200 verified, got %d %v", code, body)
	}
}

func TestVerifyDomainNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+"/v1/domains/unknown.com/verify", "good", nil)
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}
