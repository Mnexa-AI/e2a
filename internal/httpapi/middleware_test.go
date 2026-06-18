package httpapi

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// hijackableRecorder is a ResponseRecorder that also satisfies http.Hijacker,
// so we can assert challengeWriter forwards the call to the underlying writer.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, nil
}

// TestChallengeWriter_ForwardsHijack guards the regression where the auth
// challenge middleware wraps the chi root ResponseWriter — including on the
// /v1/agents/{address}/ws upgrade route. If challengeWriter does not forward
// Hijack, the gorilla upgrader's `w.(http.Hijacker)` assertion fails and the
// WebSocket live-tail silently breaks.
func TestChallengeWriter_ForwardsHijack(t *testing.T) {
	var _ http.Hijacker = (*challengeWriter)(nil) // compile-time: must implement Hijacker

	rec := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	cw := &challengeWriter{
		ResponseWriter: rec,
		r:              httptest.NewRequest(http.MethodGet, "/v1/agents/x/ws", nil),
		build:          func(*http.Request) string { return "Bearer" },
	}
	hj, ok := interface{}(cw).(http.Hijacker)
	if !ok {
		t.Fatal("challengeWriter must implement http.Hijacker")
	}
	if _, _, err := hj.Hijack(); err != nil {
		t.Fatalf("Hijack returned error: %v", err)
	}
	if !rec.hijacked {
		t.Fatal("Hijack did not delegate to the underlying ResponseWriter")
	}
}

// TestChallengeWriter_SetsHeaderOnlyOn401 pins the WWW-Authenticate behavior:
// present on a 401, absent on a 2xx (e.g. public getInfo).
func TestChallengeWriter_SetsHeaderOnlyOn401(t *testing.T) {
	build := func(*http.Request) string { return `Bearer realm="e2a"` }

	rec401 := httptest.NewRecorder()
	cw401 := &challengeWriter{ResponseWriter: rec401, r: httptest.NewRequest(http.MethodGet, "/v1/agents", nil), build: build}
	cw401.WriteHeader(http.StatusUnauthorized)
	if got := rec401.Header().Get("WWW-Authenticate"); got != `Bearer realm="e2a"` {
		t.Fatalf("401 WWW-Authenticate = %q, want the challenge", got)
	}

	rec200 := httptest.NewRecorder()
	cw200 := &challengeWriter{ResponseWriter: rec200, r: httptest.NewRequest(http.MethodGet, "/v1/info", nil), build: build}
	cw200.WriteHeader(http.StatusOK)
	if got := rec200.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("2xx must not set WWW-Authenticate, got %q", got)
	}
}
