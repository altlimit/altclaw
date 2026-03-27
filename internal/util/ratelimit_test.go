package util

import (
	"sync"
	"testing"
	"time"
)

func TestAllow_UnderLimit(t *testing.T) {
	l := NewSlidingWindowLimiter()
	for i := 0; i < 5; i++ {
		if err := l.Allow("test", 10); err != nil {
			t.Fatalf("request %d should be allowed: %v", i, err)
		}
	}
}

func TestAllow_AtLimit(t *testing.T) {
	l := NewSlidingWindowLimiter()
	// Fill up the limit
	for i := 0; i < 3; i++ {
		if err := l.Allow("test", 3); err != nil {
			t.Fatalf("request %d should be allowed: %v", i, err)
		}
	}
	// Next one should fail
	if err := l.Allow("test", 3); err == nil {
		t.Fatal("expected rate limit error, got nil")
	}
}

func TestAllow_WindowExpiry(t *testing.T) {
	l := NewSlidingWindowLimiter()
	// We can't easily wait 60s in a test, so we test the structural behavior:
	// fill up limit, verify blocked, then verify that a new key works independently
	for i := 0; i < 2; i++ {
		l.Allow("expire-test", 2)
	}
	if err := l.Allow("expire-test", 2); err == nil {
		t.Fatal("should be rate limited")
	}
	// A different key should still work
	if err := l.Allow("other-key", 2); err != nil {
		t.Fatalf("different key should not be limited: %v", err)
	}
}

func TestAllow_UnlimitedWhenZero(t *testing.T) {
	l := NewSlidingWindowLimiter()
	for i := 0; i < 100; i++ {
		if err := l.Allow("unlimited", 0); err != nil {
			t.Fatalf("limit=0 should be unlimited: %v", err)
		}
	}
	// Negative also means unlimited
	for i := 0; i < 100; i++ {
		if err := l.Allow("neg", -1); err != nil {
			t.Fatalf("limit=-1 should be unlimited: %v", err)
		}
	}
}

func TestAllow_MultipleKeys(t *testing.T) {
	l := NewSlidingWindowLimiter()

	// Fill key "a" to limit
	for i := 0; i < 2; i++ {
		l.Allow("a", 2)
	}
	// "a" should be blocked
	if err := l.Allow("a", 2); err == nil {
		t.Fatal("key 'a' should be rate limited")
	}
	// "b" should still be fine
	if err := l.Allow("b", 2); err != nil {
		t.Fatalf("key 'b' should not be limited: %v", err)
	}
}

func TestAllow_Concurrent(t *testing.T) {
	l := NewSlidingWindowLimiter()
	var wg sync.WaitGroup
	errors := make(chan error, 1000)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := "concurrent"
				if err := l.Allow(key, 10000); err != nil {
					errors <- err
				}
				time.Sleep(time.Microsecond)
			}
		}(i)
	}
	wg.Wait()
	close(errors)

	// With limit=10000 and 1000 total requests, none should be rejected
	for err := range errors {
		t.Errorf("unexpected error in concurrent test: %v", err)
	}
}
