package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

// OSRM calls an external OSRM-compatible routing API behind a circuit breaker
// so a dead provider cannot slow every request down.
type OSRM struct {
	BaseURL string
	Profile string
	Client  *http.Client
	// SpeedMps, when > 0, replaces OSRM's (driving) duration with a pedestrian
	// estimate so durations are comparable with the simulated router.
	SpeedMps float64

	breaker
}

func NewOSRM(baseURL, profile string, timeout time.Duration) *OSRM {
	return &OSRM{
		BaseURL: baseURL,
		Profile: profile,
		Client:  &http.Client{Timeout: timeout},
		breaker: newBreaker(3, 60*time.Second),
	}
}

func (o *OSRM) Name() string { return "osrm" }

// Healthy reports whether the breaker is closed (API believed reachable).
func (o *OSRM) Healthy() (ok bool, lastErr string) { return o.healthy() }

type osrmResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Routes  []struct {
		Distance float64 `json:"distance"`
		Duration float64 `json:"duration"`
		Geometry struct {
			Coordinates [][]float64 `json:"coordinates"`
		} `json:"geometry"`
	} `json:"routes"`
}

// Route ignores roads: OSRM only knows the real street network. Closures are
// handled by the Chain router which decides when to bypass OSRM.
func (o *OSRM) Route(ctx context.Context, from, to domain.Point, _ []domain.Road, _ Options) (domain.Route, error) {
	if ok, _ := o.healthy(); !ok {
		return domain.Route{}, fmt.Errorf("%w: circuit breaker open", ErrUnavailable)
	}
	url := fmt.Sprintf("%s/route/v1/%s/%f,%f;%f,%f?overview=full&geometries=geojson&steps=false",
		o.BaseURL, o.Profile, from.Lng, from.Lat, to.Lng, to.Lat)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return domain.Route{}, err
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		o.fail(err)
		return domain.Route{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		err := fmt.Errorf("osrm http %d", resp.StatusCode)
		o.fail(err)
		return domain.Route{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var body osrmResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		o.fail(err)
		return domain.Route{}, fmt.Errorf("%w: decode: %v", ErrUnavailable, err)
	}
	if body.Code != "Ok" || len(body.Routes) == 0 {
		// A "NoRoute" is a valid answer, not an outage: do not trip the breaker.
		return domain.Route{}, fmt.Errorf("osrm: %s %s", body.Code, body.Message)
	}
	o.success()
	r := body.Routes[0]
	geom := make([]domain.Point, 0, len(r.Geometry.Coordinates))
	for _, c := range r.Geometry.Coordinates {
		if len(c) >= 2 {
			geom = append(geom, domain.Point{Lat: c[1], Lng: c[0]})
		}
	}
	dur := r.Duration
	if o.SpeedMps > 0 {
		dur = math.Round(r.Distance / o.SpeedMps)
	}
	return domain.Route{Source: "osrm", DistanceM: math.Round(r.Distance), DurationS: dur, Geometry: geom}, nil
}
