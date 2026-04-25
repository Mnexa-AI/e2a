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
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	// Prune expired entries
	valid := l.buckets[key][:0]
	for _, t := range l.buckets[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= l.max {
		l.buckets[key] = valid
		return false
	}

	l.buckets[key] = append(valid, now)
	return true
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
