package routing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

func p(lat, lng float64) domain.Point { return domain.Point{Lat: lat, Lng: lng} }

// A small square: A-B-C-D with a diagonal shortcut A-C.
func squareRoads() []domain.Road {
	a, b, c, d := p(40.400, 49.800), p(40.400, 49.810), p(40.410, 49.810), p(40.410, 49.800)
	return []domain.Road{
		{ID: "ab", From: a, To: b, Status: domain.RoadOpen, Accessible: true},
		{ID: "bc", From: b, To: c, Status: domain.RoadOpen, Accessible: true},
		{ID: "cd", From: c, To: d, Status: domain.RoadOpen, Accessible: true},
		{ID: "da", From: d, To: a, Status: domain.RoadOpen, Accessible: true},
		{ID: "ac", From: a, To: c, Status: domain.RoadOpen, Accessible: false},
	}
}

func TestSimulatedAvoidsBlockedRoad(t *testing.T) {
	sim := NewSimulated(1.3)
	a, c := p(40.400, 49.800), p(40.410, 49.810)

	direct, err := sim.Route(context.Background(), a, c, squareRoads(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(direct.Geometry) != 2 {
		t.Fatalf("expected diagonal shortcut, got %d points", len(direct.Geometry))
	}

	roads := squareRoads()
	roads[4].Status = domain.RoadBlocked
	detour, err := sim.Route(context.Background(), a, c, roads, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !detour.Detour {
		t.Fatal("route must be flagged as detour when a road is blocked")
	}
	if detour.DistanceM <= direct.DistanceM {
		t.Fatalf("detour (%.0f m) should be longer than direct (%.0f m)", detour.DistanceM, direct.DistanceM)
	}
	if len(detour.Geometry) != 3 {
		t.Fatalf("expected path via one corner, got %d points", len(detour.Geometry))
	}
}

func TestSimulatedEmptyGraphFallsBackToStraightLine(t *testing.T) {
	r, err := NewSimulated(1.3).Route(context.Background(), p(40.4, 49.8), p(40.41, 49.81), nil, Options{})
	if err != nil || r.Source != "straight" || len(r.Geometry) != 2 {
		t.Fatalf("unexpected %+v %v", r, err)
	}
}

func TestChainFallsBackWhenOSRMDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	osrm := NewOSRM(srv.URL, "driving", time.Second)
	ch := NewChain("auto", osrm, NewSimulated(1.3))
	a, c := p(40.400, 49.800), p(40.410, 49.810)

	for i := 0; i < 3; i++ {
		r, err := ch.Route(context.Background(), a, c, squareRoads(), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if r.Source != "simulated" {
			t.Fatalf("expected simulated fallback, got %s", r.Source)
		}
	}
	st := ch.Status()
	if !st.BreakerOpen {
		t.Fatal("breaker should open after 3 failures")
	}
}

func TestChainUsesOSRMWhenHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":1500,"duration":1100,"geometry":{"coordinates":[[49.8,40.4],[49.81,40.41]]}}]}`))
	}))
	defer srv.Close()
	ch := NewChain("auto", NewOSRM(srv.URL, "driving", time.Second), NewSimulated(1.3))
	r, err := ch.Route(context.Background(), p(40.4, 49.8), p(40.41, 49.81), squareRoads(), Options{})
	if err != nil || r.Source != "osrm" || r.DistanceM != 1500 || r.Geometry[0].Lat != 40.4 {
		t.Fatalf("unexpected %+v %v", r, err)
	}
}

func TestChainBypassesOSRMWhenCorridorBlocked(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[]}`))
	}))
	defer srv.Close()
	ch := NewChain("auto", NewOSRM(srv.URL, "driving", time.Second), NewSimulated(1.3))
	roads := squareRoads()
	roads[4].Status = domain.RoadBlocked // diagonal, sits right on the corridor
	r, err := ch.Route(context.Background(), p(40.400, 49.800), p(40.410, 49.810), roads, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("OSRM must not be called when a blocked road lies on the corridor")
	}
	if !r.Detour || r.Source != "simulated" {
		t.Fatalf("unexpected %+v", r)
	}
}

func TestAccessibleRouteAvoidsNonAccessibleRoads(t *testing.T) {
	ch := NewChain("auto", NewOSRM("http://127.0.0.1:1", "driving", time.Second), NewSimulated(1.3))
	a, c := p(40.400, 49.800), p(40.410, 49.810)

	normal, err := NewSimulated(1.3).Route(context.Background(), a, c, squareRoads(), Options{})
	if err != nil || len(normal.Geometry) != 2 {
		t.Fatalf("normal route should use the diagonal: %+v %v", normal, err)
	}
	acc, err := ch.Route(context.Background(), a, c, squareRoads(), Options{Accessible: true})
	if err != nil {
		t.Fatal(err)
	}
	if !acc.Accessible || acc.Source != "simulated" {
		t.Fatalf("accessible route must come from the curated graph: %+v", acc)
	}
	if len(acc.Geometry) != 3 || acc.DistanceM <= normal.DistanceM {
		t.Fatalf("accessible route must avoid the stairs diagonal: %+v", acc)
	}
}

func TestDecodePolyline(t *testing.T) {
	// Google's documented example.
	pts := decodePolyline("_p~iF~ps|U_ulLnnqC_mqNvxq`@")
	want := []domain.Point{{Lat: 38.5, Lng: -120.2}, {Lat: 40.7, Lng: -120.95}, {Lat: 43.252, Lng: -126.453}}
	if len(pts) != len(want) {
		t.Fatalf("got %d points", len(pts))
	}
	for i := range want {
		if pts[i] != want[i] {
			t.Fatalf("point %d: got %+v want %+v", i, pts[i], want[i])
		}
	}
}

func TestChainPrefersGoogleThenFallsBack(t *testing.T) {
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mode") != "walking" || r.URL.Query().Get("key") != "k" {
			t.Errorf("unexpected query %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"status":"OK","routes":[{"overview_polyline":{"points":"_p~iF~ps|U_ulLnnqC"},"legs":[{"distance":{"value":1200},"duration":{"value":900}}]}]}`))
	}))
	defer google.Close()
	osrm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":1500,"duration":1100,"geometry":{"coordinates":[[49.8,40.4],[49.81,40.41]]}}]}`))
	}))
	defer osrm.Close()

	g := NewGoogleDirections("k", time.Second)
	g.BaseURL = google.URL
	ch := NewChain("auto", NewOSRM(osrm.URL, "driving", time.Second), NewSimulated(1.3))
	ch.Google = g

	r, err := ch.Route(context.Background(), p(40.4, 49.8), p(40.41, 49.81), squareRoads(), Options{})
	if err != nil || r.Source != "google" || r.DistanceM != 1200 || r.DurationS != 900 || len(r.Geometry) != 2 {
		t.Fatalf("expected google route, got %+v %v", r, err)
	}
	if st := ch.Status(); st.Provider != "google" || !st.GoogleAvailable || !st.OSRMAvailable {
		t.Fatalf("unexpected status %+v", st)
	}

	// Google denies the request (bad key / billing) → OSRM takes over, breaker opens after 3.
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"REQUEST_DENIED","error_message":"billing"}`))
	}))
	defer denied.Close()
	g.BaseURL = denied.URL
	for i := 0; i < 3; i++ {
		r, err = ch.Route(context.Background(), p(40.4, 49.8), p(40.41, 49.81), squareRoads(), Options{})
		if err != nil || r.Source != "osrm" {
			t.Fatalf("expected osrm fallback, got %+v %v", r, err)
		}
	}
	if st := ch.Status(); st.GoogleAvailable || st.Provider != "osrm" {
		t.Fatalf("google breaker should be open: %+v", st)
	}
}
