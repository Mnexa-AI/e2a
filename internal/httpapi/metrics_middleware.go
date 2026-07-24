package httpapi

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// RequestMetrics is the narrow slice of telemetry.Metrics the HTTP layer
// emits (same consumer-interface convention as janitor.Metrics). Satisfied
// by telemetry.NoOp / *telemetry.Log / the Prometheus backend.
type RequestMetrics interface {
	// HTTPRequest records one served request: route is the chi route
	// pattern (never a raw path), statusClass is "1xx".."5xx".
	HTTPRequest(method, route, statusClass string, seconds float64)
}

// requestMetrics returns the root middleware behind the availability +
// latency SLIs (docs/observability.md): one HTTPRequest sample per served
// request. Registered right after requestID so the timing covers the rest of
// the chain. A nil dep returns a pass-through, so callers can wire it
// unconditionally.
func requestMetrics(m RequestMetrics) func(http.Handler) http.Handler {
	if m == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w}
			start := time.Now()
			// The sample is emitted from a defer so a PANICKING handler still
			// lands in the availability SLI: there is no recover middleware in
			// this chain, so an on-return emit would drop panics from both
			// numerator and denominator — a crash-looping endpoint would look
			// MORE available. A panic records as 5xx and is re-raised, leaving
			// upstream behavior (net/http's connection teardown) unchanged.
			defer func() {
				panicked := recover()
				seconds := time.Since(start).Seconds()
				// A hijacked connection's handler runtime is the CONNECTION
				// lifetime (the WS handler blocks in its read loop until
				// disconnect) — hours, not a request latency. Count the
				// request, skip the duration sample (negative = no-observe).
				if sw.hijacked {
					seconds = -1
				}

				// The route pattern must be read AFTER serving — chi
				// populates it once it has matched. An empty pattern means
				// chi never matched (routeNotFound answered directly or fell
				// through to the legacy gorilla mux); those collapse into the
				// "/legacy" bucket. The raw path is never used as the label:
				// it has unbounded cardinality and /v1 paths embed email
				// addresses.
				route := "/legacy"
				if rc := chi.RouteContext(r.Context()); rc != nil {
					if p := rc.RoutePattern(); p != "" {
						route = p
					}
				}
				class := sw.statusClass()
				if panicked != nil {
					class = "5xx" // the connection dies mid-response; no status line reaches the client
				}
				m.HTTPRequest(r.Method, route, class, seconds)
				if panicked != nil {
					panic(panicked)
				}
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

// statusWriter captures the response status class for the metrics middleware.
// Like challengeWriter above it must keep Hijacker/Flusher/Unwrap visible:
// the WS route (/v1/agents/{email}/ws) is served under this middleware and
// websocket.Accept asserts http.Hijacker on the writer it receives.
type statusWriter struct {
	http.ResponseWriter
	status   int  // first status written; 0 = handler never wrote one
	hijacked bool // connection taken over (WebSocket upgrade)
}

func (sw *statusWriter) WriteHeader(status int) {
	if sw.status == 0 {
		sw.status = status
	}
	sw.ResponseWriter.WriteHeader(status)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK // bare Write implies 200
	}
	return sw.ResponseWriter.Write(b)
}

// statusClass maps the captured status to "1xx".."5xx". A hijacked
// connection counts as "1xx": the 101 status line goes to the raw conn
// after the takeover, so the wrapper never sees a WriteHeader. A handler
// that returned without writing anything counts as the implicit 200.
func (sw *statusWriter) statusClass() string {
	if sw.hijacked {
		return "1xx"
	}
	if sw.status == 0 {
		return "2xx"
	}
	return strconv.Itoa(sw.status/100) + "xx"
}

// Hijack forwards to the underlying ResponseWriter so the WebSocket upgrade
// can take over the connection, and remembers that it did.
func (sw *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := sw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("httpapi: underlying ResponseWriter does not support hijacking")
	}
	conn, rw, err := hj.Hijack()
	if err == nil {
		sw.hijacked = true
	}
	return conn, rw, err
}

// Flush forwards to the underlying ResponseWriter when it supports flushing.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer for http.ResponseController.
func (sw *statusWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }
