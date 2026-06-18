package agent_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestLegacyAPIv1Retired is an over-the-wire guard (full chi+mux apiserver via
// testutil.TestServer) that the legacy `/api/v1` bearer surface is gone: every
// retired route now 404s (no route → chi NotFound → legacy mux NotFound), and
// the WS path moved to /v1. Route removal returns 404 before any auth check, so
// no credential is needed.
func TestLegacyAPIv1Retired(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool)
	defer ts.HTTPServer.Close()

	retired := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/pending"},
		{http.MethodGet, "/api/v1/pending?status=pending_approval"},
		{http.MethodGet, "/api/v1/messages/msg_abc"},
		{http.MethodGet, "/api/v1/users/me/signing-secrets"},
		{http.MethodPost, "/api/v1/users/me/signing-secrets"},
		{http.MethodPost, "/api/v1/webhooks/wh_abc/redeliver-since"},
		{http.MethodPatch, "/api/v1/agents/bot@x.test/messages/msg_abc"},
		{http.MethodGet, "/api/v1/agents/bot@x.test/ws"},
	}
	for _, r := range retired {
		req, _ := http.NewRequest(r.method, ts.HTTPServer.URL+r.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", r.method, r.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404 (route retired)", r.method, r.path, resp.StatusCode)
		}
	}
}

// TestV1UnauthorizedAdvertisesBearer guards the gap the legacy-deletion exposed:
// the legacy mux handlers were the only code that emitted the RFC 6750 §3
// WWW-Authenticate challenge on 401. The v1 surface must now advertise the
// Bearer scheme itself (via the authChallenge middleware) so clients know how
// to retry. A no-credential GET of a bearer-protected /v1 endpoint must 401 and
// carry the challenge.
func TestV1UnauthorizedAdvertisesBearer(t *testing.T) {
	pool := testutil.TestDB(t)
	ts := testutil.TestServer(t, pool)
	defer ts.HTTPServer.Close()

	resp, err := http.Get(ts.HTTPServer.URL + "/v1/agents")
	if err != nil {
		t.Fatalf("GET /v1/agents: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /v1/agents (no auth): status = %d, want 401", resp.StatusCode)
	}
	if wa := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(strings.ToLower(wa), "bearer") {
		t.Errorf("401 must advertise the Bearer scheme per RFC 6750 §3, got WWW-Authenticate = %q", wa)
	}
}
