package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// listeningAddr returns a TCP address with a socket in the listen state (closed
// at test end) — enough for the /selftest smtp dial check to succeed.
func listeningAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

func decodeSelftest(t *testing.T, body []byte) (string, map[string]any) {
	t.Helper()
	var out struct {
		Status string         `json:"status"`
		Checks map[string]any `json:"checks"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	return out.Status, out.Checks
}

func TestSelftestHandler_AllPass(t *testing.T) {
	pool := migratedTestDB(t)
	h := selftestHandler(pool, listeningAddr(t), "" /* no auth */, true /* dev */)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/selftest", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/health+json" {
		t.Errorf("content-type = %q, want application/health+json", ct)
	}
	status, checks := decodeSelftest(t, rec.Body.Bytes())
	if status != "pass" {
		t.Errorf("status = %q, want pass", status)
	}
	for _, k := range []string{"database:reachable", "smtp:listening", "migrations:applied"} {
		if _, ok := checks[k]; !ok {
			t.Errorf("missing check %q", k)
		}
	}
}

func TestSelftestHandler_SMTPDown(t *testing.T) {
	pool := migratedTestDB(t)
	// 127.0.0.1:1 — nothing listening → dial fails.
	h := selftestHandler(pool, "127.0.0.1:1", "", true /* dev */)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/selftest", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	status, _ := decodeSelftest(t, rec.Body.Bytes())
	if status != "fail" {
		t.Errorf("status = %q, want fail", status)
	}
}

func TestSelftestHandler_AuthGate(t *testing.T) {
	pool := migratedTestDB(t)
	const secret = "topsecret"
	h := selftestHandler(pool, listeningAddr(t), secret, false /* prod */)

	// No bearer → 401.
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/selftest", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", rec.Code)
	}

	// Correct bearer → 200.
	rec2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/selftest", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	h(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("authed status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
}

// TestSelftestHandler_ProdNoSecretFailsClosed guards the adversarial-review
// finding: with no internal secret configured, /selftest must NOT serve
// diagnostics unauthenticated in production — it fails closed (503), matching
// the /api/internal/* convention. In development the same config stays open.
func TestSelftestHandler_ProdNoSecretFailsClosed(t *testing.T) {
	pool := migratedTestDB(t)
	addr := listeningAddr(t)

	prod := selftestHandler(pool, addr, "" /* no secret */, false /* prod */)
	rec := httptest.NewRecorder()
	prod(rec, httptest.NewRequest(http.MethodGet, "/selftest", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("prod + no secret: status = %d, want 503 (fail closed)", rec.Code)
	}

	dev := selftestHandler(pool, addr, "" /* no secret */, true /* dev */)
	rec2 := httptest.NewRecorder()
	dev(rec2, httptest.NewRequest(http.MethodGet, "/selftest", nil))
	if rec2.Code != http.StatusOK {
		t.Errorf("dev + no secret: status = %d, want 200 (open in dev)", rec2.Code)
	}
}
