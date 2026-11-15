package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

// GoogleDirections routes with the Google Maps **Routes API v2**
// (computeRoutes) in WALK mode — evacuation is on foot. It needs a server-side
// API key with the Routes API enabled; without a key the Chain never uses it.
// (The older "Directions API" is legacy and disabled for new projects.)
type GoogleDirections struct {
	BaseURL    string // overridable for tests
	Key        string
	TravelMode string // WALK | DRIVE | BICYCLE
	Client     *http.Client

	breaker
}

func NewGoogleDirections(key string, timeout time.Duration) *GoogleDirections {
	return &GoogleDirections{
		BaseURL:    "https://routes.googleapis.com",
		Key:        key,
		TravelMode: "WALK",
		Client:     &http.Client{Timeout: timeout},
		breaker:    newBreaker(3, 60*time.Second),
	}
}

func (g *GoogleDirections) Name() string { return "google" }

func (g *GoogleDirections) Healthy() (bool, string) { return g.healthy() }

type routesLatLng struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type routesWaypoint struct {
	Location struct {
		LatLng routesLatLng `json:"latLng"`
	} `json:"location"`
}

type routesRequest struct {
	Origin          routesWaypoint `json:"origin"`
	Destination     routesWaypoint `json:"destination"`
	TravelMode      string         `json:"travelMode"`
	PolylineQuality string         `json:"polylineQuality"`
	LanguageCode    string         `json:"languageCode"`
	Units           string         `json:"units"`
}

type routesResponse struct {
	Routes []struct {
		DistanceMeters float64 `json:"distanceMeters"`
		Duration       string  `json:"duration"` // e.g. "912s"
		Polyline       struct {
			EncodedPolyline string `json:"encodedPolyline"`
		} `json:"polyline"`
	} `json:"routes"`
	Error struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"error"`
}

func (g *GoogleDirections) Route(ctx context.Context, from, to domain.Point, _ []domain.Road, _ Options) (domain.Route, error) {
	if ok, _ := g.healthy(); !ok {
		return domain.Route{}, fmt.Errorf("%w: circuit breaker open", ErrUnavailable)
	}
	var reqBody routesRequest
	reqBody.Origin.Location.LatLng = routesLatLng{from.Lat, from.Lng}
	reqBody.Destination.Location.LatLng = routesLatLng{to.Lat, to.Lng}
	reqBody.TravelMode = g.TravelMode
	reqBody.PolylineQuality = "OVERVIEW"
	reqBody.LanguageCode = "az"
	reqBody.Units = "METRIC"
	payload, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.BaseURL+"/directions/v2:computeRoutes", bytes.NewReader(payload))
	if err != nil {
		return domain.Route{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", g.Key)
	req.Header.Set("X-Goog-FieldMask", "routes.duration,routes.distanceMeters,routes.polyline.encodedPolyline")

	resp, err := g.Client.Do(req)
	if err != nil {
		g.fail(err)
		return domain.Route{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var body routesResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		g.fail(err)
		return domain.Route{}, fmt.Errorf("%w: decode: %v", ErrUnavailable, err)
	}
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound:
		// About this particular request (e.g. no walkable route): not an outage.
		return domain.Route{}, fmt.Errorf("google routes: %s %s", body.Error.Status, body.Error.Message)
	default: // 401/403 key or API disabled, 429 quota, 5xx
		err := fmt.Errorf("google routes http %d: %s %s", resp.StatusCode, body.Error.Status, body.Error.Message)
		g.fail(err)
		return domain.Route{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if len(body.Routes) == 0 {
		return domain.Route{}, fmt.Errorf("google routes: no route")
	}
	g.success()
	r := body.Routes[0]
	dur, _ := strconv.ParseFloat(strings.TrimSuffix(r.Duration, "s"), 64)
	return domain.Route{
		Source:    "google",
		DistanceM: math.Round(r.DistanceMeters),
		DurationS: math.Round(dur),
		Geometry:  decodePolyline(r.Polyline.EncodedPolyline),
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
