// Package ratelimit holds small in-process limiters shared by the REST and MCP handlers.
package ratelimit

import (
	"sync"
	"time"
)

// FailLimiter blocks a key after too many recent failures (in-memory; resets on restart).
// It counts failed attempts, not overall traffic — use it for auth backoff (bad passwords,
// bad tokens), not for capping the rate of successful requests.
type FailLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func NewFailLimiter(limit int, window time.Duration) *FailLimiter {
	return &FailLimiter{limit: limit, window: window, hits: map[string][]time.Time{}}
}

func (l *FailLimiter) prune(key string, now time.Time) []time.Time {
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = kept
	}
	return kept
}

// Blocked reports whether key is over the limit and how long until it recovers.
func (l *FailLimiter) Blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	h := l.prune(key, now)
	if len(h) >= l.limit {
		return true, l.window - now.Sub(h[0])
	}
	return false, 0
}

func (l *FailLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[key] = append(l.hits[key], time.Now())
}

func (l *FailLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
