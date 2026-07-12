package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestExportUserData(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/account/export", nil)
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("expected attachment Content-Disposition, got %q", cd)
	}
}

func TestDeleteAccountRequiresConfirm(t *testing.T) {
	srv := testServer(t)
	// The confirm guard is now modeled as a required enum:[DELETE] query param,
	// so Huma rejects a missing/wrong value with 422 before the handler runs.
	code, body := sendJSON(t, "DELETE", srv.URL+"/v1/account", "good", nil)
	if code != 422 || errCode(body) != "invalid_request" {
		t.Fatalf("want 422 invalid_request, got %d %v", code, body)
	}
	code, body = sendJSON(t, "DELETE", srv.URL+"/v1/account?confirm=nope", "good", nil)
	if code != 422 || errCode(body) != "invalid_request" {
		t.Fatalf("want 422 invalid_request for wrong value, got %d %v", code, body)
	}
}

func TestDeleteAccountConfirmed(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/account?confirm=DELETE", "good", nil)
	if code != 200 {
		t.Fatalf("want 200, got %d", code)
	}
}

func TestDeleteAccountUnauthorized(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/account?confirm=DELETE", "", nil)
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
}
