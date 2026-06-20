package selftest

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

// Delivery is one webhook callback captured by an HTTPSink.
type Delivery struct {
	Headers http.Header
	Body    []byte
}

// HTTPSink is an http.Handler that captures webhook deliveries and lets a
// scenario await one matching a predicate. The shipped prober mounts it at
// /sink on its internal server; the in-process tests mount it on an
// httptest.Server. It is safe for concurrent use.
type HTTPSink struct {
	mu      sync.Mutex
	got     []Delivery
	waiters []chan struct{}
}

// NewHTTPSink returns an empty sink.
func NewHTTPSink() *HTTPSink { return &HTTPSink{} }

// ServeHTTP records the delivery and returns 200. Body read failures are
// recorded as empty deliveries (the awaiting scenario will simply not match
// and time out, surfacing the problem) rather than 500s.
func (s *HTTPSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	s.mu.Lock()
	s.got = append(s.got, Delivery{Headers: r.Header.Clone(), Body: body})
	for _, ch := range s.waiters {
		close(ch)
	}
	s.waiters = nil
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// Await returns the first delivery (new or already-buffered) for which match
// returns true, or an error if ctx/timeout elapses first. It scans the buffer
// on every new delivery so out-of-order arrivals are handled.
func (s *HTTPSink) Await(ctx context.Context, match func(Delivery) bool, timeout time.Duration) (*Delivery, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	scanned := 0
	for {
		s.mu.Lock()
		for ; scanned < len(s.got); scanned++ {
			if match(s.got[scanned]) {
				d := s.got[scanned]
				s.mu.Unlock()
				return &d, nil
			}
		}
		ch := make(chan struct{})
		s.waiters = append(s.waiters, ch)
		s.mu.Unlock()

		select {
		case <-ch:
			// new delivery arrived → loop and rescan from `scanned`
		case <-deadline.C:
			return nil, context.DeadlineExceeded
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
