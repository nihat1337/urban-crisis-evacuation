package routing

import (
	"fmt"
	"sync"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

// RouteCache keeps the last successful external routes so that during an
// outage the citizen still gets the real street route they were served
// before ("son keşlənmiş marşrutlar"). Keys are rounded to ~11 m.
type RouteCache struct {
	mu   sync.RWMutex
	ttl  time.Duration
	max  int
	data map[string]cached
}

type cached struct {
	route domain.Route
	at    time.Time
}

func NewRouteCache(ttl time.Duration, max int) *RouteCache {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if max <= 0 {
		max = 5000
	}
	return &RouteCache{ttl: ttl, max: max, data: map[string]cached{}}
}

func cacheKey(from, to domain.Point, opts Options) string {
	return fmt.Sprintf("%.4f,%.4f>%.4f,%.4f|a=%t", from.Lat, from.Lng, to.Lat, to.Lng, opts.Accessible)
}

func (c *RouteCache) Put(from, to domain.Point, opts Options, r domain.Route) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data) >= c.max {
		// Cheap eviction: drop the oldest entry.
		var oldestK string
		var oldest time.Time
		for k, v := range c.data {
			if oldestK == "" || v.at.Before(oldest) {
				oldestK, oldest = k, v.at
			}
		}
		delete(c.data, oldestK)
	}
	c.data[cacheKey(from, to, opts)] = cached{route: r, at: time.Now()}
}

func (c *RouteCache) Get(from, to domain.Point, opts Options) (domain.Route, time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.data[cacheKey(from, to, opts)]
	if !ok || time.Since(v.at) > c.ttl {
		return domain.Route{}, time.Time{}, false
	}
	return v.route, v.at, true
}

func (c *RouteCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}
