package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
