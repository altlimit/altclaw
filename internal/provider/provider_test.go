package provider

import (
	"context"
	"testing"
	"time"
)

func TestWithRateAndSem_Success(t *testing.T) {
	called := false
	err := withRateAndSem(context.Background(), "test-success", func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("function was not called")
	}
}

func TestWithRateAndSem_FnError(t *testing.T) {
	err := withRateAndSem(context.Background(), "test-fn-err", func() error {
		return context.DeadlineExceeded
	})
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestWithRateAndSem_RateLimited(t *testing.T) {
	key := "test-rate-limited"
	// Set a very low RPM
	SetProviderRPM(key, 2)
	defer func() {
		// Cleanup
		SetProviderRPM(key, 0)
	}()

	ctx := context.Background()

	// First 2 should succeed
	for i := 0; i < 2; i++ {
		err := withRateAndSem(ctx, key, func() error { return nil })
		if err != nil {
			t.Fatalf("request %d should succeed: %v", i, err)
		}
	}

	// Third should be rate limited
	err := withRateAndSem(ctx, key, func() error { return nil })
	if err == nil {
		t.Fatal("expected rate limit error, got nil")
	}
}

func TestAcquireSem_CancelledContext(t *testing.T) {
	// Set concurrency limit (default is 0 = unlimited)
	SetConcurrency(10)
	defer SetConcurrency(0)

	ctx, cancel := context.WithCancel(context.Background())
	key := "test-sem-cancel"

	// Fill the semaphore
	var releases []func()
	for i := 0; i < 10; i++ {
		release, err := acquireSem(ctx, key)
		if err != nil {
			t.Fatalf("acquire %d failed: %v", i, err)
		}
		releases = append(releases, release)
	}

	// Cancel context, then try to acquire
	cancel()

	_, err := acquireSem(ctx, key)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}

	// Cleanup
	for _, r := range releases {
		r()
	}
}

func TestAcquireSem_ReleaseAndReacquire(t *testing.T) {
	SetConcurrency(10)
	defer SetConcurrency(0)

	ctx := context.Background()
	key := "test-sem-release"

	// Fill up
	var releases []func()
	for i := 0; i < 10; i++ {
		release, err := acquireSem(ctx, key)
		if err != nil {
			t.Fatalf("acquire %d failed: %v", i, err)
		}
		releases = append(releases, release)
	}

	// Release one
	releases[0]()

	// Should be able to acquire again
	release, err := acquireSem(ctx, key)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	release()

	// Cleanup remaining
	for _, r := range releases[1:] {
		r()
	}
}

func TestSetProviderRPM_UpdatesLimit(t *testing.T) {
	key := "test-rpm-update"

	// Initially unlimited
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		if err := checkProviderRPM(ctx, key); err != nil {
			t.Fatalf("should be unlimited: %v", err)
		}
	}

	// Set a limit
	SetProviderRPM(key, 5)
	defer SetProviderRPM(key, 0)

	// Should block after 5
	for i := 0; i < 5; i++ {
		if err := checkProviderRPM(ctx, key); err != nil {
			t.Fatalf("request %d should be allowed: %v", i, err)
		}
	}
	if err := checkProviderRPM(ctx, key); err == nil {
		t.Fatal("should be rate limited after 5 requests")
	}
}

func TestCheckProviderRPM_ContextNotUsedForRPM(t *testing.T) {
	// checkProviderRPM accepts context but doesn't use it for cancellation
	// — verify it works with a cancelled context (RPM check is sync)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	cancel()

	// Should still work (RPM check doesn't use context)
	err := checkProviderRPM(ctx, "test-expired-ctx")
	if err != nil {
		t.Fatalf("expired context should not affect RPM check: %v", err)
	}
}
