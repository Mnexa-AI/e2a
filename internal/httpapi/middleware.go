package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
)

// ctxKey is the unexported context-key type for this package.
type ctxKey int

const requestIDKey ctxKey = iota

// requestIDHeader is the canonical observability header (api-v1-redesign
// "HTTP header conventions"): generated per request, returned on every
// response, accepted + propagated when the client supplies it, and echoed
// into the error envelope body.
const requestIDHeader = "X-Request-Id"

// newRequestID returns a short, URL-safe, prefixed request id. crypto/rand
// is used so ids aren't guessable; on the astronomically unlikely read
// failure we fall back to a fixed sentinel rather than panicking a request.
func newRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(b)
}

// RequestIDFromContext returns the request id stamped by the requestID
// middleware, or "" if none is present.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// requestID middleware honors a caller-supplied X-Request-Id (so a trace id
// can flow across services) and otherwise mints one, then stashes it in the
// request context and sets it on the response header. Because it runs at the
// chi root it covers both the new Huma surface and the legacy fallback —
// every response carries a request id.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authChallenge injects the RFC 6750 §3 WWW-Authenticate header on any 401
// response. Every 401 on this Bearer-accepting surface must advertise the
// scheme so clients know how to retry (and OAuth-bearer failures get the §3.1
// error params so MCP clients can trigger the re-flow). The legacy mux set this
// at each 401 site via writeAuthError; the v1 surface rejects through the
// canonical envelope, so we set the header here from one place keyed only on
// the 401 status — leaving 2xx/other responses (e.g. public getInfo) untouched.
// The challenge value comes from the injected builder (agent.API.
// WWWAuthenticateChallenge), so both surfaces emit identical challenges.
func authChallenge(build func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if build == nil {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(&challengeWriter{ResponseWriter: w, r: r, build: build}, r)
		})
	}
}

// challengeWriter sets WWW-Authenticate just before the status line is written,
// but only when that status is 401. Setting it in WriteHeader (and lazily on a
// bare Write, which implies 200) guarantees the header lands before the body —
// headers can't be added once the status is flushed.
type challengeWriter struct {
	http.ResponseWriter
	r       *http.Request
	build   func(r *http.Request) string
	written bool
}

func (cw *challengeWriter) WriteHeader(status int) {
	if !cw.written {
		cw.written = true
		if status == http.StatusUnauthorized {
			if c := cw.build(cw.r); c != "" {
				cw.ResponseWriter.Header().Set("WWW-Authenticate", c)
			}
		}
	}
	cw.ResponseWriter.WriteHeader(status)
}

func (cw *challengeWriter) Write(b []byte) (int, error) {
	if !cw.written {
		cw.WriteHeader(http.StatusOK)
	}
	return cw.ResponseWriter.Write(b)
}

// Hijack forwards to the underlying ResponseWriter so the WebSocket upgrade
// (root.Get("/v1/agents/{address}/ws", …), which this root middleware also
// wraps) can still take over the connection. Without this passthrough the
// WebSocket upgrader's `w.(http.Hijacker)` assertion fails and live-tail breaks.
func (cw *challengeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := cw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("httpapi: underlying ResponseWriter does not support hijacking")
}

// Flush forwards to the underlying ResponseWriter when it supports flushing.
func (cw *challengeWriter) Flush() {
	if f, ok := cw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer for http.ResponseController.
func (cw *challengeWriter) Unwrap() http.ResponseWriter { return cw.ResponseWriter }

// securityHeaders applies the baseline security headers the audit found
// missing (api-v1-redesign "HTTP header conventions"). It deliberately sets
// only the universally-safe `X-Content-Type-Options: nosniff` at this layer;
// HSTS is an edge (Caddy) concern and the stricter CSP/frame headers belong
// on the HTML confirmation pages, which set their own. Applying nosniff
// globally is additive and never breaks a JSON or HTML response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
