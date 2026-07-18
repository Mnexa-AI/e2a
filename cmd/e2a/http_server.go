package main

import (
	"net/http"
	"time"
)

const (
	httpReadHeaderTimeout = 10 * time.Second
	httpIdleTimeout       = 120 * time.Second
)

// newHTTPServer applies connection-level denial-of-service safeguards without
// imposing a whole-request deadline. The latter would also apply to permitted
// large attachment uploads and can leak onto hijacked WebSocket connections.
// Request body sizes remain bounded per operation by the HTTP API.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}
