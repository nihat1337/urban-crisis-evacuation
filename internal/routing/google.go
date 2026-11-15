package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

// GoogleDirections routes with the Google Maps Directions API (walking mode by
// default — evacuation is on foot). It needs a server-side API key with the
// Directions API enabled; without a key the Chain simply never uses it.
type GoogleDirections struct {
	BaseURL string // overridable for tests
	Key     string
	Mode    string // walking | driving | bicycling
	Client  *http.Client

	breaker
}

func NewGoogleDirections(key string, timeout time.Duration) *GoogleDirections {
	return &GoogleDirections{
		BaseURL: "https://maps.googleapis.com",
		Key:     key,
		Mode:    "walking",
		Client:  &http.Client{Timeout: timeout},
		breaker: newBreaker(3, 60*time.Second),
	}
}

func (g *GoogleDirections) Name() string { return "google" }

func (g *GoogleDirections) Healthy() (bool, string) { return g.healthy() }

type googleResponse struct {
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
	Routes       []struct {
		OverviewPolyline struct {
			Points string `json:"points"`
		} `json:"overview_polyline"`
		Legs []struct {
			Distance struct {
				Value float64 `json:"value"`
			} `json:"distance"`
			Duration struct {
				Value float64 `json:"value"`
			} `json:"duration"`
		} `json:"legs"`
	} `json:"routes"`
}

func (g *GoogleDirections) Route(ctx context.Context, from, to domain.Point, _ []domain.Road, _ Options) (domain.Route, error) {
	if ok, _ := g.healthy(); !ok {
		return domain.Route{}, fmt.Errorf("%w: circuit breaker open", ErrUnavailable)
	}
	q := url.Values{}
	q.Set("origin", fmt.Sprintf("%f,%f", from.Lat, from.Lng))
	q.Set("destination", fmt.Sprintf("%f,%f", to.Lat, to.Lng))
	q.Set("mode", g.Mode)
	q.Set("key", g.Key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+"/maps/api/directions/json?"+q.Encode(), nil)
	if err != nil {
		return domain.Route{}, err
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		g.fail(err)
		return domain.Route{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	var body googleResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		g.fail(err)
		return domain.Route{}, fmt.Errorf("%w: decode: %v", ErrUnavailable, err)
	}
	switch body.Status {
	case "OK":
	case "ZERO_RESULTS", "NOT_FOUND", "INVALID_REQUEST":
		// Valid answers about this particular request: do not trip the breaker.
		return domain.Route{}, fmt.Errorf("google: %s %s", body.Status, body.ErrorMessage)
	default: // REQUEST_DENIED, OVER_QUERY_LIMIT, OVER_DAILY_LIMIT, UNKNOWN_ERROR
		err := fmt.Errorf("google: %s %s", body.Status, body.ErrorMessage)
		g.fail(err)
		return domain.Route{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if len(body.Routes) == 0 || len(body.Routes[0].Legs) == 0 {
		return domain.Route{}, fmt.Errorf("google: empty route")
	}
	g.success()
	r := body.Routes[0]
	var dist, dur float64
	for _, l := range r.Legs {
		dist += l.Distance.Value
		dur += l.Duration.Value
	}
	return domain.Route{
		Source:    "google",
		DistanceM: math.Round(dist),
		DurationS: math.Round(dur),
		Geometry:  decodePolyline(r.OverviewPolyline.Points),
	}, nil
}

// decodePolyline decodes Google's encoded polyline format.
func decodePolyline(s string) []domain.Point {
	var pts []domain.Point
	var lat, lng int64
	i := 0
	next := func() int64 {
		var result int64
		var shift uint
		for {
			if i >= len(s) {
				return 0
			}
			b := int64(s[i]) - 63
			i++
			result |= (b & 0x1f) << shift
			shift += 5
			if b < 0x20 {
				break
			}
		}
		if result&1 != 0 {
			return ^(result >> 1)
		}
		return result >> 1
	}
	for i < len(s) {
		lat += next()
		lng += next()
		pts = append(pts, domain.Point{Lat: float64(lat) / 1e5, Lng: float64(lng) / 1e5})
	}
	return pts
}
