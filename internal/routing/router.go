// Package routing computes evacuation routes. The Router interface hides
// whether the route came from an external API (OSRM) or from the built-in
// simulated road graph, so the external dependency can disappear at runtime
// without the rest of the system noticing.
package routing

import (
	"context"
	"errors"
	"math"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

var ErrUnavailable = errors.New("routing provider unavailable")

type Router interface {
	Name() string
	// Route computes a path from -> to taking the current road statuses into account.
	Route(ctx context.Context, from, to domain.Point, roads []domain.Road) (domain.Route, error)
}

// Status is exposed on the health/overview endpoints.
type Status struct {
	Mode          string `json:"mode"`
	OSRMAvailable bool   `json:"osrm_available"`
	BreakerOpen   bool   `json:"breaker_open"`
	LastError     string `json:"last_error,omitempty"`
}

// segmentDistM returns the distance in metres from p to segment a-b using an
// equirectangular projection (accurate enough at city scale).
func segmentDistM(p, a, b domain.Point) float64 {
	cosLat := math.Cos(a.Lat * math.Pi / 180)
	toXY := func(q domain.Point) (float64, float64) {
		return (q.Lng - a.Lng) * cosLat * 111320, (q.Lat - a.Lat) * 110540
	}
	px, py := toXY(p)
	bx, by := toXY(b)
	l2 := bx*bx + by*by
	if l2 == 0 {
		return math.Hypot(px, py)
	}
	t := (px*bx + py*by) / l2
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-t*bx, py-t*by)
}

func straightLine(from, to domain.Point, speed float64, notes string) domain.Route {
	d := domain.DistanceM(from, to)
	return domain.Route{
		Source:    "straight",
		DistanceM: math.Round(d),
		DurationS: math.Round(d / speed),
		Geometry:  []domain.Point{from, to},
		Notes:     notes,
	}
}
