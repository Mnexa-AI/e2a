package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestV1RouterErrorsUseCanonicalEnvelope(t *testing.T) {
	legacyCalls := 0
	s := New(Deps{Legacy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		legacyCalls++
		w.WriteHeader(http.StatusTeapot)
	})})

	tests := []struct {
		name   string
		method string
		path   string
		status int
		code   string
	}{
		{name: "not found", method: http.MethodGet, path: "/v1/not-a-route", status: http.StatusNotFound, code: "not_found"},
		{name: "method not allowed", method: http.MethodPost, path: "/v1/info", status: http.StatusMethodNotAllowed, code: "method_not_allowed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%q", rr.Code, tc.status, rr.Body.String())
			}
			if got := rr.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			var env ErrorEnvelope
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode envelope: %v; body=%q", err, rr.Body.String())
			}
			if env.Err.Code != tc.code {
				t.Fatalf("error.code = %q, want %q", env.Err.Code, tc.code)
			}
			if env.Err.RequestID == "" || env.Err.RequestID != rr.Header().Get(requestIDHeader) {
				t.Fatalf("request id mismatch: body=%q header=%q", env.Err.RequestID, rr.Header().Get(requestIDHeader))
			}
		})
	}
	if legacyCalls != 0 {
		t.Fatalf("legacy handler called %d times for /v1 errors", legacyCalls)
	}
}

func TestLegacyFallbackRemainsOutsideV1(t *testing.T) {
	legacyCalls := 0
	s := New(Deps{Legacy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		legacyCalls++
		w.WriteHeader(http.StatusTeapot)
	})})

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, httptest.NewRequest(method, "/legacy-miss", nil))
		if rr.Code != http.StatusTeapot {
			t.Fatalf("%s status = %d, want %d", method, rr.Code, http.StatusTeapot)
		}
	}
	if legacyCalls != 2 {
		t.Fatalf("legacy handler called %d times, want 2", legacyCalls)
	}
}
