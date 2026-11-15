package routing

import (
	"context"
	"fmt"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

// Chain is the production Router. Mode:
//
//	auto      – OSRM first, simulated fallback (default)
//	osrm      – OSRM only (errors surface)
//	simulated – never call the external API
//
// Even in auto mode a blocked road close to the direct corridor forces the
// simulated router, because the public OSRM API cannot be told about closures.
type Chain struct {
	Mode            string
	OSRM            *OSRM
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
	st := Status{Mode: c.Mode}
	if c.OSRM != nil {
		ok, last := c.OSRM.Healthy()
		st.OSRMAvailable = ok && c.Mode != "simulated"
		st.BreakerOpen = !ok
		st.LastError = last
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

func (c *Chain) Route(ctx context.Context, from, to domain.Point, roads []domain.Road) (domain.Route, error) {
	if c.Mode == "simulated" || c.OSRM == nil {
		return c.Sim.Route(ctx, from, to, roads)
	}
	if blocked, name := c.corridorBlocked(from, to, roads); blocked {
		r, err := c.Sim.Route(ctx, from, to, roads)
		r.Detour = true
		r.Notes = fmt.Sprintf("%s bağlıdır — alternativ marşrut", name)
		return r, err
	}
	r, err := c.OSRM.Route(ctx, from, to, roads)
	if err == nil {
		return r, nil
	}
	if c.Mode == "osrm" {
		return domain.Route{}, err
	}
	fb, ferr := c.Sim.Route(ctx, from, to, roads)
	if ferr != nil {
		return domain.Route{}, ferr
	}
	fb.Notes = "xarici marşrut API-si əlçatan deyil — simulyasiya rejimi"
	return fb, nil
}
