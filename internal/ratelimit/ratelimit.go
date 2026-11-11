// Package ratelimit is a small in-memory sliding-window limiter keyed by string.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows at most max attempts per key in any window.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu        sync.Mutex
	hits      map[string][]time.Time
	lastSweep time.Time
}

// New builds a Limiter. now is the clock, so tests can inject one.
func New(max int, window time.Duration, now func() time.Time) *Limiter {
	return &Limiter{max: max, window: window, now: now, hits: map[string][]time.Time{}}
}

// Allow records an attempt for key and reports whether it is within the limit.
// A rejected attempt is not recorded, so it doesn't extend the lockout.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastSweep) >= l.window {
		l.sweep(now)
		l.lastSweep = now
	}

	recent := l.hits[key]
	// Drop this key's expired attempts, since the sweep may not have run.
	cutoff := now.Add(-l.window)
	for len(recent) > 0 && !recent[0].After(cutoff) {
		recent = recent[1:]
	}
	if len(recent) >= l.max {
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

// Full reports whether key is at its limit, so the next [Limiter.Allow] would
// refuse it. It records nothing.
func (l *Limiter) Full(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := l.now().Add(-l.window)
	n := 0
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			n++
		}
	}
	return n >= l.max
}

// sweep drops expired attempts and empty keys. It runs at most once per
// window, so Allow is O(1) amortized and memory stays bounded.
func (l *Limiter) sweep(now time.Time) {
	cutoff := now.Add(-l.window)
	for k, ts := range l.hits {
		i := 0
		for i < len(ts) && !ts[i].After(cutoff) {
			i++
		}
		if i == len(ts) {
			delete(l.hits, k)
		} else if i > 0 {
			l.hits[k] = ts[i:]
		}
	}
}

func (l *Limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.hits)
}
