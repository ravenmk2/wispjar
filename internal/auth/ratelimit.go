package auth

import (
	"sync"
	"time"
)

const (
	maxLoginFails     = 5
	loginLockDuration = 5 * time.Minute
)

type loginEntry struct {
	fails       int
	lastFail    time.Time
	lockedUntil time.Time
}

type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: make(map[string]*loginEntry)}
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return true
	}
	return !time.Now().Before(e.lockedUntil)
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	e, ok := l.entries[key]
	if !ok {
		e = &loginEntry{}
		l.entries[key] = e
	}
	if now.After(e.lockedUntil) && now.Sub(e.lastFail) > loginLockDuration {
		e.fails = 0
	}
	e.fails++
	e.lastFail = now
	if e.fails >= maxLoginFails {
		e.lockedUntil = now.Add(loginLockDuration)
		e.fails = 0
	}
}

func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
