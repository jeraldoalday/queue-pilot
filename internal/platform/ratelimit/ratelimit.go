// Package ratelimit provides a small in-process token-bucket limiter,
// used to keep QueuePilot's unauthenticated endpoints from being flooded.
//
// It is deliberately per-process and in-memory: the thing being protected
// is a walk-in queue, where the realistic abuse is one person or script
// hammering the join form, not a distributed attack. A shared store would
// add an operational dependency for no benefit at this size. The tradeoff
// is that N app instances allow N times the configured rate, which is
// noted here so it is a known limit rather than a surprise.
package ratelimit

import (
	"sync"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
)

// sweepInterval is how often expired buckets are dropped. Without this the
// map would grow one entry per distinct client forever, which on a public
// endpoint is an unbounded memory leak driven by strangers.
const sweepInterval = 10 * time.Minute

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// Limiter allows `burst` requests immediately and then one more every
// `interval`, independently per key.
type Limiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	burst     float64
	interval  time.Duration
	clock     clock.Clock
	lastSweep time.Time
}

// New returns a Limiter permitting `burst` requests up front and refilling
// one token every `interval`. A burst below 1 is raised to 1: a limiter
// that rejects everything is never what a caller means.
func New(burst int, interval time.Duration, c clock.Clock) *Limiter {
	if burst < 1 {
		burst = 1
	}
	if interval <= 0 {
		interval = time.Second
	}
	return &Limiter{
		buckets:   make(map[string]*bucket),
		burst:     float64(burst),
		interval:  interval,
		clock:     c,
		lastSweep: c.Now(),
	}
}

// Allow reports whether the request identified by key may proceed, and
// consumes a token when it does. An unknown key starts with a full bucket,
// so a first-time visitor is never turned away.
func (l *Limiter) Allow(key string) bool {
	now := l.clock.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst}
		l.buckets[key] = b
	} else {
		// Refill for the time elapsed, capped at burst so an idle client
		// can't bank an unlimited allowance to spend all at once.
		refill := float64(now.Sub(b.lastSeen)) / float64(l.interval)
		if refill > 0 {
			b.tokens = min(l.burst, b.tokens+refill)
		}
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Retry reports how long the key must wait for its next token. Callers use
// it for the Retry-After header so a well-behaved client backs off by the
// right amount instead of guessing.
func (l *Limiter) Retry(key string) time.Duration {
	now := l.clock.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		return 0
	}
	tokens := min(l.burst, b.tokens+float64(now.Sub(b.lastSeen))/float64(l.interval))
	if tokens >= 1 {
		return 0
	}
	return time.Duration((1 - tokens) * float64(l.interval))
}

// sweepLocked drops buckets that have been full for long enough that
// forgetting them changes nothing: a fully refilled bucket is
// indistinguishable from a new one.
func (l *Limiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < sweepInterval {
		return
	}
	l.lastSweep = now

	// The time it takes a bucket to refill completely from empty.
	full := time.Duration(l.burst * float64(l.interval))
	for key, b := range l.buckets {
		if now.Sub(b.lastSeen) >= full {
			delete(l.buckets, key)
		}
	}
}

// Len reports how many keys are currently tracked. It exists for tests and
// for a future metric, not for request handling.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
