package main

import (
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPServerUsesSafeConnectionDefaults(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	srv := newHTTPServer(":8080", handler)

	if srv.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", srv.Addr)
	}
	if srv.Handler == nil {
		t.Fatal("Handler must be preserved")
	}
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %s, want 10s", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %s, want 120s", srv.IdleTimeout)
	}
	if srv.ReadTimeout != 0 {
		t.Errorf("ReadTimeout = %s, want zero so large permitted uploads and WebSockets are not given a whole-request deadline", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %s, want zero so long-lived WebSockets are not given a write deadline", srv.WriteTimeout)
	}
}
