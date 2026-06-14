package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestExportUserData(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/v1/users/me/export", nil)
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
	code, body := sendJSON(t, "DELETE", srv.URL+"/v1/users/me", "good", nil)
	if code != 400 || errCode(body) != "confirmation_required" {
		t.Fatalf("want 400 confirmation_required, got %d %v", code, body)
	}
}

func TestDeleteAccountConfirmed(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/users/me?confirm=DELETE", "good", nil)
	if code != 200 {
		t.Fatalf("want 200, got %d", code)
	}
}

func TestDeleteAccountUnauthorized(t *testing.T) {
	srv := testServer(t)
	code, _ := sendJSON(t, "DELETE", srv.URL+"/v1/users/me?confirm=DELETE", "", nil)
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
}
