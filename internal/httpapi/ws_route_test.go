package httpapi

import (
	"encoding/json"
	"io"
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

// TestWSHandshakeError_HonorsClientRequestID exercises the PRODUCTION request-id
// path (not the WriteError fallback that mints its own id). Mounted on the real
// New(deps).Router, the chi-root requestID middleware honors a caller-supplied
// X-Request-Id and stashes it in context; a WS handshake rejection routed
// through WriteError must then echo THAT id in both the JSON envelope's
// request_id and the X-Request-Id response header — so a client's trace id flows
// end-to-end through a failed WS handshake exactly as it does through REST.
func TestWSHandshakeError_HonorsClientRequestID(t *testing.T) {
	const clientReqID = "req_client_supplied_123"

	deps := Deps{
		WSHandle: func(w http.ResponseWriter, r *http.Request, _ string) {
			// A handshake rejection (before the WebSocket upgrade) uses the same
			// raw error envelope every /v1 endpoint does.
			WriteError(w, r, http.StatusNotFound, "not_found", "agent not found")
		},
	}
	srv := httptest.NewServer(New(deps).Router)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/agents/bot%40agents.e2a.dev/ws", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Request-Id", clientReqID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
	if h := resp.Header.Get("X-Request-Id"); h != clientReqID {
		t.Fatalf("X-Request-Id header: got %q, want the client-supplied %q", h, clientReqID)
	}

	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("body is not the JSON envelope: %v (body=%s)", err, body)
	}
	if env.Error.Code != "not_found" {
		t.Fatalf("code: got %q, want not_found", env.Error.Code)
	}
	if env.Error.RequestID != clientReqID {
		t.Fatalf("envelope request_id: got %q, want the client-supplied %q", env.Error.RequestID, clientReqID)
	}
}
