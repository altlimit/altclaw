package util

import (
	"fmt"
	"sync"
	"time"
)

// SlidingWindowLimiter is a generic per-key sliding-window rate limiter.
// It tracks timestamps of recent events and rejects new events if the count
// within the window exceeds the configured limit.
//
// Thread-safe: uses per-key mutexes via sync.Map for minimal contention.
type SlidingWindowLimiter struct {
	keys sync.Map // key → *limiterState
}

type limiterState struct {
	mu         sync.Mutex
	timestamps []int64 // unix nanoseconds
}

// NewSlidingWindowLimiter creates a new rate limiter.
func NewSlidingWindowLimiter() *SlidingWindowLimiter {
	return &SlidingWindowLimiter{}
}

// Allow checks whether a new event for `key` would exceed `limit` events
// per minute. If allowed, records the timestamp and returns nil.
// If the limit is exceeded, returns an error.
// A limit of 0 or negative means unlimited.
func (l *SlidingWindowLimiter) Allow(key string, limit int64) error {
	if limit <= 0 {
		return nil
	}

	v, _ := l.keys.LoadOrStore(key, &limiterState{})
	state := v.(*limiterState)
	state.mu.Lock()
	defer state.mu.Unlock()

	now := time.Now().UnixNano()
	window := now - int64(60*time.Second)

	// Evict expired timestamps in-place
	valid := state.timestamps[:0]
	for _, t := range state.timestamps {
		if t > window {
			valid = append(valid, t)
		}
	}
	state.timestamps = valid

	if int64(len(valid)) >= limit {
		return fmt.Errorf("rate limit of %d req/min exceeded for %q", limit, key)
	}

	state.timestamps = append(state.timestamps, now)
	return nil
}
