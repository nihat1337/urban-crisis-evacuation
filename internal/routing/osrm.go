package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

// OSRM calls an external OSRM-compatible routing API. It carries a small
// circuit breaker: after MaxFailures consecutive errors it stops calling the
// API for Cooldown so a dead provider cannot slow every request down.
type OSRM struct {
	BaseURL     string
	Profile     string
	Client      *http.Client
	MaxFailures int
	Cooldown    time.Duration

	mu        sync.Mutex
	failures  int
	openUntil time.Time
	lastErr   string
}

func NewOSRM(baseURL, profile string, timeout time.Duration) *OSRM {
	return &OSRM{
		BaseURL:     baseURL,
		Profile:     profile,
		Client:      &http.Client{Timeout: timeout},
		MaxFailures: 3,
		Cooldown:    60 * time.Second,
	}
}

func (o *OSRM) Name() string { return "osrm" }

// Healthy reports whether the breaker is closed (API believed reachable).
func (o *OSRM) Healthy() (ok bool, lastErr string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return time.Now().After(o.openUntil), o.lastErr
}

func (o *OSRM) recordFailure(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failures++
	o.lastErr = err.Error()
	if o.failures >= o.MaxFailures {
		o.openUntil = time.Now().Add(o.Cooldown)
		o.failures = 0
	}
}

func (o *OSRM) recordSuccess() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failures = 0
	o.lastErr = ""
}

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
func (o *OSRM) Route(ctx context.Context, from, to domain.Point, _ []domain.Road) (domain.Route, error) {
	if ok, _ := o.Healthy(); !ok {
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
		o.recordFailure(err)
		return domain.Route{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		err := fmt.Errorf("osrm http %d", resp.StatusCode)
		o.recordFailure(err)
		return domain.Route{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var body osrmResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		o.recordFailure(err)
		return domain.Route{}, fmt.Errorf("%w: decode: %v", ErrUnavailable, err)
	}
	if body.Code != "Ok" || len(body.Routes) == 0 {
		// A "NoRoute" is a valid answer, not an outage: do not trip the breaker.
		return domain.Route{}, fmt.Errorf("osrm: %s %s", body.Code, body.Message)
	}
	o.recordSuccess()
	r := body.Routes[0]
	geom := make([]domain.Point, 0, len(r.Geometry.Coordinates))
	for _, c := range r.Geometry.Coordinates {
		if len(c) >= 2 {
			geom = append(geom, domain.Point{Lat: c[1], Lng: c[0]})
		}
	}
	return domain.Route{Source: "osrm", DistanceM: r.Distance, DurationS: r.Duration, Geometry: geom}, nil
}
