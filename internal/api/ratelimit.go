package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// limiter is a per-client token bucket. It protects the API under load
// ("yüksək yük") without any external dependency.
type limiter struct {
	mu      sync.Mutex
	rps     float64
	burst   float64
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(rps float64, burst int) *limiter {
	l := &limiter{rps: rps, burst: float64(burst), buckets: map[string]*bucket{}}
	go l.gc()
	return l
}

func (l *limiter) allow(key string) bool {
	if l.rps <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rps
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *limiter) gc() {
	for range time.Tick(time.Minute) {
		l.mu.Lock()
		for k, b := range l.buckets {
			if time.Since(b.last) > 5*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

var _ = strings.TrimSpace
