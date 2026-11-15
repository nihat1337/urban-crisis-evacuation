package routing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

// Chain is the production Router. Mode:
//
//	auto      – external providers in order (Google Routes if configured,
//	            then OSRM), then the local backup (default)
//	google    – Google only (errors surface)
//	osrm      – OSRM only (errors surface)
//	simulated – never call an external API
//
// Resilience (jury scenario "external map/routing API down: HTTP 503 or
// > 5 s"): every provider has a per-request timeout and a circuit breaker;
// when all providers fail the Chain serves, in order, the last cached route
// for the same origin/destination and then a route over the local road graph
// (static coordinate database). The result is flagged Offline so the UI can
// show "oflayn / ehtiyat marşrut rejimi". Nothing in this path can panic or
// return an error for a reachable pair of points.
type Chain struct {
	Mode            string
	Google          *GoogleDirections // optional
	OSRM            *OSRM             // optional
	Sim             *Simulated
	Cache           *RouteCache
	CorridorMarginM float64
	Timeout         time.Duration // used for the simulated "timeout" outage

	mu    sync.RWMutex
	chaos *faultRouter // non-nil while an outage is simulated
}

func NewChain(mode string, osrm *OSRM, sim *Simulated) *Chain {
	if mode == "" {
		mode = "auto"
	}
	return &Chain{Mode: mode, OSRM: osrm, Sim: sim, Cache: NewRouteCache(24*time.Hour, 5000), CorridorMarginM: 400, Timeout: 5 * time.Second}
}

func (c *Chain) Name() string { return "chain(" + c.Mode + ")" }

// SetChaos simulates an external outage: "503", "timeout" or "" (off).
func (c *Chain) SetChaos(mode string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch mode {
	case "503", "timeout":
		c.chaos = newFaultRouter(mode, c.Timeout)
	default:
		c.chaos = nil
	}
}

func (c *Chain) Chaos() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.chaos == nil {
		return ""
	}
	return c.chaos.Mode
}

func (c *Chain) Status() Status {
	st := Status{Mode: c.Mode, Provider: "simulated", Chaos: c.Chaos()}
	if c.Cache != nil {
		st.CacheSize = c.Cache.Len()
	}
	configured := 0
	if c.OSRM != nil && c.Mode != "simulated" && c.Mode != "google" {
		configured++
		ok, last := c.OSRM.Healthy()
		st.OSRMAvailable = ok
		st.BreakerOpen = !ok
		st.LastError = last
		if ok {
			st.Provider = "osrm"
		}
	}
	if c.Google != nil && c.Mode != "simulated" && c.Mode != "osrm" {
		configured++
		ok, last := c.Google.Healthy()
		st.GoogleAvailable = ok
		st.GoogleConfigured = true
		if !ok && last != "" {
			st.LastError = last
		}
		if ok {
			st.Provider = "google"
		}
	}
	if st.Chaos != "" {
		st.GoogleAvailable, st.OSRMAvailable, st.BreakerOpen = false, false, true
		st.Provider = "simulated"
		c.mu.RLock()
		if _, last := c.chaos.healthy(); last != "" {
			st.LastError = last
		} else {
			st.LastError = "simulated outage (" + st.Chaos + ")"
		}
		c.mu.RUnlock()
	}
	// Degraded = external routing is configured but nothing external is usable.
	st.Degraded = configured > 0 && !st.GoogleAvailable && !st.OSRMAvailable
	return st
}

func (c *Chain) corridorBlocked(from, to domain.Point, roads []domain.Road) (bool, string) {
	for _, r := range roads {
		if r.Status != domain.RoadBlocked {
			continue
		}
		mid := domain.Point{Lat: (r.From.Lat + r.To.Lat) / 2, Lng: (r.From.Lng + r.To.Lng) / 2}
		for _, p := range []domain.Point{r.From, mid, r.To} {
			if segmentDistM(p, from, to) <= c.CorridorMarginM {
				return true, r.Name
			}
		}
	}
	return false, ""
}

// external returns the ordered list of external providers for the current mode.
func (c *Chain) external() []Router {
	c.mu.RLock()
	chaos := c.chaos
	c.mu.RUnlock()
	var out []Router
	switch c.Mode {
	case "google":
		if c.Google != nil {
			out = append(out, c.Google)
		}
	case "osrm":
		if c.OSRM != nil {
			out = append(out, c.OSRM)
		}
	case "simulated":
	default: // auto
		if c.Google != nil {
			out = append(out, c.Google)
		}
		if c.OSRM != nil {
			out = append(out, c.OSRM)
		}
	}
	if chaos != nil && len(out) > 0 {
		return []Router{chaos} // the outage replaces every external provider
	}
	return out
}

func (c *Chain) Route(ctx context.Context, from, to domain.Point, roads []domain.Road, opts Options) (domain.Route, error) {
	// Accessible routes always come from the curated graph: public routing APIs
	// know nothing about ramps, stairs or barriers.
	if opts.Accessible {
		r, err := c.Sim.Route(ctx, from, to, roads, opts)
		if err == nil && r.Source == "simulated" && r.Notes == "" {
			r.Notes = "♿ yalnız panduslu və maneəsiz yollar"
		}
		return r, err
	}
	providers := c.external()
	if len(providers) == 0 {
		return c.Sim.Route(ctx, from, to, roads, opts)
	}
	if blocked, name := c.corridorBlocked(from, to, roads); blocked {
		r, err := c.Sim.Route(ctx, from, to, roads, opts)
		r.Detour = true
		r.Notes = fmt.Sprintf("%s bağlıdır — alternativ marşrut", name)
		return r, err
	}
	var lastErr error
	for _, p := range providers {
		r, err := p.Route(ctx, from, to, roads, opts)
		if err == nil {
			if c.Cache != nil {
				c.Cache.Put(from, to, opts, r)
			}
			return r, nil
		}
		lastErr = err
	}
	if c.Mode != "auto" {
		return domain.Route{}, lastErr
	}
	return c.backup(ctx, from, to, roads, opts, lastErr)
}

// backup is the offline path: last cached route, then the local road graph.
func (c *Chain) backup(ctx context.Context, from, to domain.Point, roads []domain.Road, opts Options, cause error) (domain.Route, error) {
	reason := "xarici marşrut API-si marşrut tapmadı"
	if errors.Is(cause, ErrUnavailable) {
		reason = "xarici marşrut API-si əlçatan deyil (503 / timeout)"
	}
	if c.Cache != nil {
		if r, at, ok := c.Cache.Get(from, to, opts); ok {
			r.Source = "cache"
			r.Offline = true
			r.OfflineReason = reason
			t := at
			r.CachedAt = &t
			r.Notes = fmt.Sprintf("ehtiyat rejim: son keşlənmiş marşrut (%s)", at.Local().Format("15:04"))
			return r, nil
		}
	}
	r, err := c.Sim.Route(ctx, from, to, roads, opts)
	if err != nil {
		// The simulated router never errors today, but never let an outage escape.
		r = straightLine(from, to, c.Sim.SpeedMps, "düz xətt")
	}
	r.Offline = true
	r.OfflineReason = reason
	r.Notes = "ehtiyat rejim: lokal yol qrafı (statik koordinat bazası)"
	return r, nil
}
