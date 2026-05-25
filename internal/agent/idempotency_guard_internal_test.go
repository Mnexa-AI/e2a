package agent

import (
	"net/http/httptest"
	"testing"
)

// shouldCacheResponse is the pure policy function the guard uses to
// decide between Complete (cache) and Release (free for retry).
// Exhaustive matrix.
func TestShouldCacheResponse(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		sideEff   bool
		wantCache bool
	}{
		{"200 + no side effect → cache", 200, false, true},
		{"202 (HITL held) + no side effect → cache", 202, false, true},
		{"302 redirect + no side effect → cache", 302, false, true},
		{"400 + no side effect → release", 400, false, false},
		{"422 + no side effect → release", 422, false, false},
		{"500 + no side effect → release", 500, false, false},
		{"503 + no side effect → release", 503, false, false},

		// The new behavior: once the side effect committed, even error
		// responses must be cached so a retry can't re-trigger the
		// side effect. The 500 here is from a panic recovery /
		// post-send failure; we MUST NOT release.
		{"200 + side effect committed → cache", 200, true, true},
		{"400 + side effect committed → cache (defensive)", 400, true, true},
		{"500 + side effect committed → cache (no double-send)", 500, true, true},
		{"503 + side effect committed → cache (no double-send)", 503, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldCacheResponse(c.status, c.sideEff); got != c.wantCache {
				t.Errorf("shouldCacheResponse(%d, %v) = %v, want %v", c.status, c.sideEff, got, c.wantCache)
			}
		})
	}
}

// markSideEffectCommitted is a no-op when the writer isn't a
// capturingWriter (no-header path or replay path). Verify it doesn't
// panic against a bare http.ResponseWriter and doesn't somehow leave
// state behind.
func TestMarkSideEffectCommitted_NoopOnBareWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	markSideEffectCommitted(rec)
	if rec.Code != 200 {
		t.Errorf("recorder Code = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("recorder Body changed: %q", rec.Body.String())
	}
}

// markSideEffectCommitted DOES flip the flag on the capturing wrapper.
func TestMarkSideEffectCommitted_FlipsFlagOnCapturingWriter(t *testing.T) {
	cw := &capturingWriter{ResponseWriter: httptest.NewRecorder()}
	if cw.sideEffectCommitted {
		t.Fatal("freshly-constructed capturingWriter should have sideEffectCommitted=false")
	}
	markSideEffectCommitted(cw)
	if !cw.sideEffectCommitted {
		t.Error("sideEffectCommitted not set by markSideEffectCommitted")
	}
}
