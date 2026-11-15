package routing

import (
	"context"
	"encoding/json"
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
		if r.Method != http.MethodPost || r.URL.Path != "/directions/v2:computeRoutes" || r.Header.Get("X-Goog-Api-Key") != "k" || r.Header.Get("X-Goog-FieldMask") == "" {
			t.Errorf("unexpected request %s %s key=%q", r.Method, r.URL.Path, r.Header.Get("X-Goog-Api-Key"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["travelMode"] != "WALK" {
			t.Errorf("expected WALK, got %v", body["travelMode"])
		}
		_, _ = w.Write([]byte(`{"routes":[{"distanceMeters":1200,"duration":"900s","polyline":{"encodedPolyline":"_p~iF~ps|U_ulLnnqC"}}]}`))
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
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Routes API has not been used in project"}}`))
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

// Jury scenario: the external routing API returns HTTP 503. The chain must not
// error, must open the breaker, serve the last cached route first and the
// local road graph after that — all flagged Offline.
func TestOutage503ServesCacheThenLocalGraph(t *testing.T) {
	healthy := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":1500,"duration":1100,"geometry":{"coordinates":[[49.8,40.4],[49.81,40.41]]}}]}`))
	}))
	defer srv.Close()
	ch := NewChain("auto", NewOSRM(srv.URL, "driving", time.Second), NewSimulated(1.3))
	a, c := p(40.400, 49.800), p(40.410, 49.810)

	// 1) online: real route, cached
	r, err := ch.Route(context.Background(), a, c, squareRoads(), Options{})
	if err != nil || r.Source != "osrm" || r.Offline {
		t.Fatalf("online route expected, got %+v %v", r, err)
	}
	if ch.Cache.Len() != 1 {
		t.Fatalf("route should be cached")
	}
	// 2) outage: same pair → cached route, flagged offline
	healthy = false
	r, err = ch.Route(context.Background(), a, c, squareRoads(), Options{})
	if err != nil || r.Source != "cache" || !r.Offline || r.DistanceM != 1500 || r.CachedAt == nil {
		t.Fatalf("cached offline route expected, got %+v %v", r, err)
	}
	// 3) outage, unseen pair → local road graph, flagged offline
	r, err = ch.Route(context.Background(), p(40.400, 49.810), p(40.410, 49.800), squareRoads(), Options{})
	if err != nil || r.Source != "simulated" || !r.Offline || r.OfflineReason == "" {
		t.Fatalf("local-graph offline route expected, got %+v %v", r, err)
	}
	// breaker opens after 3 failures; status reports degraded
	for i := 0; i < 3; i++ {
		_, _ = ch.Route(context.Background(), a, c, squareRoads(), Options{})
	}
	if st := ch.Status(); !st.Degraded || !st.BreakerOpen {
		t.Fatalf("expected degraded status, got %+v", st)
	}
	// 4) recovery: breaker closed by cooldown → online again
	ch.OSRM.breaker.openUntil = time.Time{}
	healthy = true
	r, _ = ch.Route(context.Background(), a, c, squareRoads(), Options{})
	if r.Source != "osrm" || r.Offline {
		t.Fatalf("expected recovery to online, got %+v", r)
	}
}

// Jury scenario: the API hangs longer than the timeout. The request must come
// back promptly with a backup route instead of blocking the citizen.
func TestOutageTimeoutFallsBackPromptly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	ch := NewChain("auto", NewOSRM(srv.URL, "driving", 700*time.Millisecond), NewSimulated(1.3))
	start := time.Now()
	r, err := ch.Route(context.Background(), p(40.400, 49.800), p(40.410, 49.810), squareRoads(), Options{})
	if err != nil || !r.Offline || r.Source != "simulated" {
		t.Fatalf("expected offline fallback, got %+v %v", r, err)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("fallback took too long: %s", el)
	}
}

func TestChaosSwitchSimulatesOutage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":1500,"duration":1100,"geometry":{"coordinates":[[49.8,40.4],[49.81,40.41]]}}]}`))
	}))
	defer srv.Close()
	ch := NewChain("auto", NewOSRM(srv.URL, "driving", time.Second), NewSimulated(1.3))
	ch.Timeout = 300 * time.Millisecond
	ch.SetChaos("timeout")
	start := time.Now()
	r, err := ch.Route(context.Background(), p(40.400, 49.800), p(40.410, 49.810), squareRoads(), Options{})
	if err != nil || !r.Offline || time.Since(start) < 250*time.Millisecond {
		t.Fatalf("expected simulated timeout then fallback, got %+v %v", r, err)
	}
	if st := ch.Status(); !st.Degraded || st.Chaos != "timeout" {
		t.Fatalf("status should report chaos, got %+v", st)
	}
	ch.SetChaos("")
	r, _ = ch.Route(context.Background(), p(40.400, 49.800), p(40.410, 49.810), squareRoads(), Options{})
	if r.Offline || r.Source != "osrm" {
		t.Fatalf("expected online after chaos off, got %+v", r)
	}
}
