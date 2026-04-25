package ratelimit

import (
	"testing"
	"time"
)

func TestAllow(t *testing.T) {
	l := New(1*time.Second, 3)

	for i := 0; i < 3; i++ {
		if !l.Allow("key1") {
			t.Fatalf("expected Allow on attempt %d", i+1)
		}
	}

	if l.Allow("key1") {
		t.Fatal("expected rate limit to deny 4th attempt")
	}

	// Different key should still be allowed
	if !l.Allow("key2") {
		t.Fatal("expected Allow for different key")
	}
}

func TestWindowExpiry(t *testing.T) {
	l := New(50*time.Millisecond, 2)

	l.Allow("k")
	l.Allow("k")

	if l.Allow("k") {
		t.Fatal("expected denial at limit")
	}

	time.Sleep(60 * time.Millisecond)

	if !l.Allow("k") {
		t.Fatal("expected Allow after window expired")
	}
}

func TestCleanup(t *testing.T) {
	l := New(50*time.Millisecond, 10)

	l.Allow("a")
	l.Allow("b")

	time.Sleep(60 * time.Millisecond)
	l.Cleanup()

	l.mu.Lock()
	count := len(l.buckets)
	l.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 buckets after cleanup, got %d", count)
	}
}

func TestCleanupLoopRunsAutomatically(t *testing.T) {
	// Use a very short window so the cleanup ticker fires quickly
	l := New(50*time.Millisecond, 10)

	l.Allow("x")
	l.Allow("y")

	l.mu.Lock()
	if len(l.buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(l.buckets))
	}
	l.mu.Unlock()

	// Wait for entries to expire and cleanup loop to fire
	time.Sleep(120 * time.Millisecond)

	l.mu.Lock()
	count := len(l.buckets)
	l.mu.Unlock()

	if count != 0 {
		t.Errorf("expected cleanup loop to remove expired buckets, got %d", count)
	}
}

func TestCleanupLoopKeepsActiveEntries(t *testing.T) {
	l := New(100*time.Millisecond, 10)

	l.Allow("active")

	// Wait less than the window so entries are still valid
	time.Sleep(50 * time.Millisecond)

	l.mu.Lock()
	count := len(l.buckets)
	l.mu.Unlock()

	if count != 1 {
		t.Errorf("expected active bucket to remain, got %d buckets", count)
	}
}
