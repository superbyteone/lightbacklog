package mcp

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// tokenLimiter caps how many MCP calls a single token may make per second, so a leaked or
// malfunctioning credential can't overload an instance that's reachable from outside the
// local host. Idle entries are swept out periodically, piggybacked on Allow calls rather than
// a background goroutine, matching the prune-on-read style of ratelimit.FailLimiter.
type tokenLimiter struct {
	mu        sync.Mutex
	rps       rate.Limit
	burst     int
	entries   map[string]*limiterEntry
	lastSweep time.Time
}

type limiterEntry struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

const limiterIdleTimeout = 10 * time.Minute

func newTokenLimiter(rps float64, burst int) *tokenLimiter {
	return &tokenLimiter{rps: rate.Limit(rps), burst: burst, entries: map[string]*limiterEntry{}, lastSweep: time.Now()}
}

// allow reports whether tokenID may make another call right now.
func (tl *tokenLimiter) allow(tokenID string) bool {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	now := time.Now()
	if now.Sub(tl.lastSweep) > limiterIdleTimeout {
		for k, e := range tl.entries {
			if now.Sub(e.lastSeen) > limiterIdleTimeout {
				delete(tl.entries, k)
			}
		}
		tl.lastSweep = now
	}
	e, ok := tl.entries[tokenID]
	if !ok {
		e = &limiterEntry{lim: rate.NewLimiter(tl.rps, tl.burst)}
		tl.entries[tokenID] = e
	}
	e.lastSeen = now
	return e.lim.Allow()
}
