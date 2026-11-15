package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nihat1337/urban-crisis-evacuation/internal/crisis"
	"github.com/nihat1337/urban-crisis-evacuation/internal/events"
	"github.com/nihat1337/urban-crisis-evacuation/internal/routing"
	"github.com/nihat1337/urban-crisis-evacuation/internal/seed"
	"github.com/nihat1337/urban-crisis-evacuation/internal/service"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store/memory"
)

const testKey = "test-key"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := memory.New()
	if err := seed.Apply(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub()
	eng := crisis.New(crisis.DefaultConfig(), st)
	router := routing.NewChain("simulated", nil, routing.NewSimulated(1.3))
	svc := service.New(service.Config{ShelterWarnPct: 90, MaxAlternatives: 2}, st, router, eng, hub, log)
	h := New(Config{DispatcherKey: testKey, RateLimitRPS: 1000, RateLimitBurst: 1000, DefaultLang: "az", Version: "test"}, svc, hub, log)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url string, body any, key string) (*http.Response, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Dispatcher-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestHealthAndPlan(t *testing.T) {
	srv := newTestServer(t)
	resp, out := do(t, "GET", srv.URL+"/api/v1/health", nil, "")
	if resp.StatusCode != 200 || out["status"] != "ok" {
		t.Fatalf("health: %d %v", resp.StatusCode, out)
	}

	resp, out = do(t, "GET", srv.URL+"/api/v1/evacuation/plan?lat=40.387&lng=49.830", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("plan: %d %v", resp.StatusCode, out)
	}
	rec, _ := out["recommended"].(map[string]any)
	if rec == nil || rec["route"] == nil {
		t.Fatalf("plan must have a recommended shelter with a route: %v", out)
	}
	if out["zone_id"] != "nasimi" {
		t.Fatalf("expected nearest zone nasimi, got %v", out["zone_id"])
	}

	resp, out = do(t, "GET", srv.URL+"/api/v1/evacuation/plan", nil, "")
	if resp.StatusCode != 400 {
		t.Fatalf("plan without location must be 400, got %d %v", resp.StatusCode, out)
	}
	resp, _ = do(t, "GET", srv.URL+"/api/v1/evacuation/plan?zone=khatai", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("plan by zone: %d", resp.StatusCode)
	}
}

func TestDispatcherAuth(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := do(t, "GET", srv.URL+"/api/v1/dispatch/overview", nil, "")
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	resp, _ = do(t, "GET", srv.URL+"/api/v1/dispatch/overview", nil, "wrong")
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	resp, out := do(t, "GET", srv.URL+"/api/v1/dispatch/overview", nil, testKey)
	if resp.StatusCode != 200 || out["zones"] == nil {
		t.Fatalf("overview: %d %v", resp.StatusCode, out)
	}
}

func TestSOSClusterTriggersAlertAndExecution(t *testing.T) {
	srv := newTestServer(t)
	var alertID string
	for i := 1; i <= 4; i++ {
		resp, out := do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{
			"citizen_id": fmt.Sprintf("c%d", i), "type": "fire", "lat": 40.3870, "lng": 49.8300,
		}, "")
		if resp.StatusCode != 201 {
			t.Fatalf("sos %d: %d %v", i, resp.StatusCode, out)
		}
		if i < 4 && out["alert"] != nil {
			t.Fatalf("alert too early at signal %d", i)
		}
		if i == 4 {
			a, _ := out["alert"].(map[string]any)
			if a == nil || a["level"] != "red" {
				t.Fatalf("expected red alert on 4th signal: %v", out)
			}
			alertID, _ = a["id"].(string)
		}
	}

	resp, out := do(t, "POST", srv.URL+"/api/v1/dispatch/alerts/"+alertID+"/execute", map[string]any{"action": "dispatch_brigade"}, testKey)
	if resp.StatusCode != 200 {
		t.Fatalf("execute: %d %v", resp.StatusCode, out)
	}
	br, _ := out["brigade"].(map[string]any)
	if br == nil || br["status"] != "en_route" {
		t.Fatalf("brigade must be en_route: %v", out)
	}
	resp, _ = do(t, "POST", srv.URL+"/api/v1/dispatch/alerts/"+alertID+"/execute", map[string]any{"action": "open_shelter"}, testKey)
	if resp.StatusCode != 409 {
		t.Fatalf("second execution must be 409, got %d", resp.StatusCode)
	}
}

func TestInvalidSOS(t *testing.T) {
	srv := newTestServer(t)
	resp, out := do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{"type": "meteor", "lat": 40.4, "lng": 49.8}, "")
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d %v", resp.StatusCode, out)
	}
	errObj, _ := out["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "invalid_sos_type" {
		t.Fatalf("unexpected error payload %v", out)
	}
}

func TestShelterFullCreatesAlertAndRoadBlockReroutes(t *testing.T) {
	srv := newTestServer(t)
	// 96% → alert
	resp, out := do(t, "PATCH", srv.URL+"/api/v1/dispatch/shelters/sh-khatai-3", map[string]any{"occupancy": 580}, testKey)
	if resp.StatusCode != 200 || out["alert"] == nil {
		t.Fatalf("expected shelter alert: %d %v", resp.StatusCode, out)
	}
	// Block the road and ask for a plan right next to it: route must be flagged as detour.
	resp, _ = do(t, "PATCH", srv.URL+"/api/v1/dispatch/roads/r-sh-3", map[string]any{"status": "blocked"}, testKey)
	if resp.StatusCode != 200 {
		t.Fatalf("block road: %d", resp.StatusCode)
	}
	resp, out = do(t, "GET", srv.URL+"/api/v1/evacuation/plan?lat=40.3870&lng=49.8300", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("plan: %d", resp.StatusCode)
	}
	rec := out["recommended"].(map[string]any)
	route := rec["route"].(map[string]any)
	if route["detour"] != true {
		t.Fatalf("route should be marked as detour after a road block: %v", route)
	}
}
