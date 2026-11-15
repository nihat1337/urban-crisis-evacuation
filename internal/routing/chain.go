package routing

import (
	"context"
	"errors"
	"fmt"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

// Chain is the production Router. Mode:
//
//	auto      – external providers in order (Google Directions if configured,
//	            then OSRM), simulated fallback (default)
//	google    – Google only (errors surface)
//	osrm      – OSRM only (errors surface)
//	simulated – never call an external API
//
// Even in auto mode a blocked road close to the direct corridor forces the
// simulated router, because public routing APIs cannot be told about closures;
// accessible (barrier-free) requests always use the curated graph as well.
type Chain struct {
	Mode            string
	Google          *GoogleDirections // optional
	OSRM            *OSRM             // optional
	Sim             *Simulated
	CorridorMarginM float64
}

func NewChain(mode string, osrm *OSRM, sim *Simulated) *Chain {
	if mode == "" {
		mode = "auto"
	}
	return &Chain{Mode: mode, OSRM: osrm, Sim: sim, CorridorMarginM: 400}
}

func (c *Chain) Name() string { return "chain(" + c.Mode + ")" }

func (c *Chain) Status() Status {
	st := Status{Mode: c.Mode, Provider: "simulated"}
	if c.OSRM != nil && c.Mode != "simulated" && c.Mode != "google" {
		ok, last := c.OSRM.Healthy()
		st.OSRMAvailable = ok
		st.BreakerOpen = !ok
		st.LastError = last
		if ok {
			st.Provider = "osrm"
		}
	}
	if c.Google != nil && c.Mode != "simulated" && c.Mode != "osrm" {
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
			return r, nil
		}
		lastErr = err
		// A provider-specific "no route" answer is final for that provider; an
		// outage (ErrUnavailable) just moves on to the next one. Either way the
		// next provider gets its chance in auto mode.
	}
	if c.Mode != "auto" {
		return domain.Route{}, lastErr
	}
	fb, ferr := c.Sim.Route(ctx, from, to, roads, opts)
	if ferr != nil {
		return domain.Route{}, ferr
	}
	if errors.Is(lastErr, ErrUnavailable) {
		fb.Notes = "xarici marşrut API-si əlçatan deyil — simulyasiya rejimi"
	} else {
		fb.Notes = "xarici API marşrut tapmadı — simulyasiya rejimi"
	}
	return fb, nil
}
