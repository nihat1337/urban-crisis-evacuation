package routing

import (
	"sync"
	"time"
)

// breaker is a minimal circuit breaker shared by the external providers:
// after maxFailures consecutive errors the provider is skipped for cooldown.
type breaker struct {
	mu          sync.Mutex
	maxFailures int
	cooldown    time.Duration
	failures    int
	openUntil   time.Time
	lastErr     string
}

func newBreaker(maxFailures int, cooldown time.Duration) breaker {
	return breaker{maxFailures: maxFailures, cooldown: cooldown}
}

// healthy reports whether calls are currently allowed, plus the last error seen.
func (b *breaker) healthy() (bool, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().After(b.openUntil), b.lastErr
}

func (b *breaker) fail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.lastErr = err.Error()
	if b.failures >= b.maxFailures {
		b.openUntil = time.Now().Add(b.cooldown)
		b.failures = 0
	}
}

func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.lastErr = ""
}
