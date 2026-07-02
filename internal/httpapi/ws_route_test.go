package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The WS route is the one /v1 route hand-mounted on chi (not Huma), and chi
// returns URL params STILL PERCENT-ENCODED when the request URI was encoded
// (RawPath routing). Every SDK client encodeURIComponent()s the address, so
// without an explicit PathUnescape at the mount the handler received
// "x%40y" and 404'd every real WebSocket client — while the raw-@ form (and
// every Huma REST route, which decodes its own params) worked. Regression:
// both spellings must reach WSHandle as the decoded address.
func TestWSRouteDecodesPercentEncodedAddress(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{"percent-encoded (what SDKs send)", "/v1/agents/tether%40agents.e2a.dev/ws"},
		{"raw @", "/v1/agents/tether@agents.e2a.dev/ws"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			deps := Deps{
				WSHandle: func(w http.ResponseWriter, r *http.Request, address string) {
					got = address
					w.WriteHeader(http.StatusSwitchingProtocols)
				},
			}
			srv := httptest.NewServer(New(deps).Router)
			t.Cleanup(srv.Close)

			resp, err := http.Get(srv.URL + tc.path)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()

			if got != "tether@agents.e2a.dev" {
				t.Fatalf("WSHandle received address %q, want %q", got, "tether@agents.e2a.dev")
			}
		})
	}
}
