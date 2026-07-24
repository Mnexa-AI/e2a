package httpapi

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
)

// fakeRequestMetrics records every HTTPRequest call for assertion.
type fakeRequestMetrics struct {
	mu    sync.Mutex
	calls []recordedHTTPRequest
}

type recordedHTTPRequest struct {
	method, route, statusClass string
	seconds                    float64
}

func (f *fakeRequestMetrics) HTTPRequest(method, route, statusClass string, seconds float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, recordedHTTPRequest{method, route, statusClass, seconds})
}

func (f *fakeRequestMetrics) single(t *testing.T) recordedHTTPRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 {
		t.Fatalf("expected exactly 1 HTTPRequest call, got %d: %+v", len(f.calls), f.calls)
	}
	return f.calls[0]
}

// metricsTestRouter builds a minimal chi router with the metrics middleware —
// the middleware only needs chi's RouteContext, not the full httpapi.New()
// dependency set.
func metricsTestRouter(m RequestMetrics) chi.Router {
	r := chi.NewRouter()
	r.Use(requestMetrics(m))
	return r
}

func TestMetricsMiddlewareMatchedRouteRecordsPattern(t *testing.T) {
	rec := &fakeRequestMetrics{}
	r := metricsTestRouter(rec)
	r.Get("/v1/agents/{email}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// The raw path carries an email address — the recorded route must be the
	// chi pattern, never the path (cardinality + PII).
	req := httptest.NewRequest(http.MethodGet, "/v1/agents/someone%40x.dev", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	got := rec.single(t)
	if got.method != http.MethodGet {
		t.Errorf("method = %q, want %q", got.method, http.MethodGet)
	}
	if got.route != "/v1/agents/{email}" {
		t.Errorf("route = %q, want %q", got.route, "/v1/agents/{email}")
	}
	if got.statusClass != "2xx" {
		t.Errorf("statusClass = %q, want %q", got.statusClass, "2xx")
	}
	if got.seconds <= 0 {
		t.Errorf("seconds = %v, want > 0", got.seconds)
	}
}

func TestMetricsMiddlewareImplicit200(t *testing.T) {
	rec := &fakeRequestMetrics{}
	r := metricsTestRouter(rec)
	// Handler returns without writing a status or body: net/http sends 200.
	r.Get("/v1/health", func(http.ResponseWriter, *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if got := rec.single(t); got.statusClass != "2xx" {
		t.Errorf("statusClass = %q, want %q", got.statusClass, "2xx")
	}
}

func TestMetricsMiddleware500RecordsServerErrorClass(t *testing.T) {
	rec := &fakeRequestMetrics{}
	r := metricsTestRouter(rec)
	r.Get("/v1/boom", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/boom", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	got := rec.single(t)
	if got.statusClass != "5xx" {
		t.Errorf("statusClass = %q, want %q", got.statusClass, "5xx")
	}
	if got.route != "/v1/boom" {
		t.Errorf("route = %q, want %q", got.route, "/v1/boom")
	}
}

func TestMetricsMiddlewareUnmatchedRouteRecordsLegacy(t *testing.T) {
	rec := &fakeRequestMetrics{}
	r := metricsTestRouter(rec)
	r.Get("/v1/agents", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// An unmatched path falls through to chi's NotFound (in production,
	// routeNotFound → the legacy gorilla mux). The route label must be the
	// "/legacy" bucket, never the raw path.
	req := httptest.NewRequest(http.MethodGet, "/api/some/old/path", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	got := rec.single(t)
	if got.route != "/legacy" {
		t.Errorf("route = %q, want %q", got.route, "/legacy")
	}
	if got.statusClass != "4xx" {
		t.Errorf("statusClass = %q, want %q", got.statusClass, "4xx")
	}
}

func TestMetricsMiddlewareWriterPassthrough(t *testing.T) {
	rec := &fakeRequestMetrics{}
	r := metricsTestRouter(rec)
	r.Get("/v1/stream", func(w http.ResponseWriter, _ *http.Request) {
		// The wrapper must keep the optional interfaces visible: the WS route
		// asserts http.Hijacker, streaming asserts http.Flusher, and
		// http.ResponseController needs Unwrap().
		if _, ok := w.(http.Hijacker); !ok {
			t.Error("wrapper does not implement http.Hijacker")
		}
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapper does not implement http.Flusher")
		}
		if _, ok := w.(interface{ Unwrap() http.ResponseWriter }); !ok {
			t.Error("wrapper does not implement Unwrap()")
		}
		w.WriteHeader(http.StatusOK)
		f.Flush()
	})

	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/v1/stream", nil))

	if !rw.Flushed {
		t.Error("Flush did not reach the underlying ResponseWriter")
	}
	if got := rec.single(t); got.statusClass != "2xx" {
		t.Errorf("statusClass = %q, want %q", got.statusClass, "2xx")
	}
}

func TestMetricsMiddlewareHijackCountsAsUpgrade(t *testing.T) {
	rec := &fakeRequestMetrics{}
	r := metricsTestRouter(rec)
	// hijackableRecorder (middleware_test.go) stands in for the real server
	// connection under a WebSocket upgrade.
	r.Get("/v1/agents/{email}/ws", func(w http.ResponseWriter, _ *http.Request) {
		if _, _, err := w.(http.Hijacker).Hijack(); err != nil {
			t.Fatalf("Hijack: %v", err)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/agents/a%40x.dev/ws", nil)
	r.ServeHTTP(&hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}, req)

	got := rec.single(t)
	// The 101 status line goes to the raw conn after hijack, so the wrapper
	// never sees WriteHeader; a hijacked request counts as the upgrade class.
	if got.statusClass != "1xx" {
		t.Errorf("statusClass = %q, want %q", got.statusClass, "1xx")
	}
	if got.route != "/v1/agents/{email}/ws" {
		t.Errorf("route = %q, want %q", got.route, "/v1/agents/{email}/ws")
	}
	// A hijacked handler's runtime is the connection lifetime, not a request
	// latency — the middleware must pass a negative duration ("count only,
	// no histogram sample") or hours-long WS connections pin the HTTP p99.
	if got.seconds >= 0 {
		t.Errorf("seconds = %v, want negative (no duration sample for hijacked connections)", got.seconds)
	}
}

func TestMetricsMiddlewareNilMetricsDoesNotPanic(t *testing.T) {
	r := chi.NewRouter()
	r.Use(requestMetrics(nil))
	r.Get("/v1/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/v1/ok", nil))
	if rw.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rw.Code)
	}
}
