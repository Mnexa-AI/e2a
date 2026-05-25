package ratelimit

import (
	"sync"
	"time"
)

// Limiter is an in-memory sliding-window rate limiter keyed by string.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	window  time.Duration
	max     int
}

func New(window time.Duration, max int) *Limiter {
	l := &Limiter{
		buckets: make(map[string][]time.Time),
		window:  window,
		max:     max,
	}
	go l.cleanupLoop()
	return l
}

// Allow returns true if the key has not exceeded the rate limit.
// If allowed, the attempt is recorded.
func (l *Limiter) Allow(key string) bool {
	ok, _ := l.AllowWithRetryAfter(key)
	return ok
}

// AllowWithRetryAfter behaves like Allow but additionally returns the
// duration the caller must wait before the next attempt could succeed
// (rounded up to the next whole second so callers can use it for the
// Retry-After response header). When the request is allowed, the
// returned duration is zero. The returned duration is conservative
// (>= one second when blocked) so that a Retry-After header always
// communicates a meaningful, RFC 7231 §7.1.3-compatible delay.
func (l *Limiter) AllowWithRetryAfter(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	// Prune expired entries in place.
	valid := l.buckets[key][:0]
	for _, t := range l.buckets[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= l.max {
		l.buckets[key] = valid
		// The oldest in-window hit drops off the window at oldest+window,
		// so that's when one more slot opens up.
		retryAfter := valid[0].Add(l.window).Sub(now)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		// Round up to the next whole second for header use.
		if retryAfter%time.Second != 0 {
			retryAfter = retryAfter.Truncate(time.Second) + time.Second
		}
		return false, retryAfter
	}

	l.buckets[key] = append(valid, now)
	return true, 0
}

func (l *Limiter) cleanupLoop() {
	ticker := time.NewTicker(l.window)
	defer ticker.Stop()
	for range ticker.C {
		l.Cleanup()
	}
}

// Cleanup removes expired entries for all keys. Call periodically to prevent memory growth.
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-l.window)
	for key, times := range l.buckets {
		valid := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		if len(valid) == 0 {
			delete(l.buckets, key)
		} else {
			l.buckets[key] = valid
		}
	}
}
