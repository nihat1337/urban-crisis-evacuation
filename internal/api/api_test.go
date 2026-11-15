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
	"strings"
	"testing"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/auth"
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
	svc := service.New(service.Config{ShelterWarnPct: 90, MaxAlternatives: 2, FloodThreshold: 25, DedupeWindow: 30 * time.Second}, st, router, eng, hub, log)
	h := New(Config{DispatcherKey: testKey, RateLimitRPS: 100000, RateLimitBurst: 100000, DefaultLang: "az", Version: "test",
		JWTSecret: []byte("test-secret"), Users: auth.ParseUsers("admin:a:admin,op:o:operator,view:v:viewer"),
		SOSRatePerMin: 6000000, SOSBurst: 100000}, svc, hub, log)
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

func TestPrioritySOSAndAccessiblePlan(t *testing.T) {
	srv := newTestServer(t)

	// A reduced-mobility citizen's SOS is escalated to critical priority (#1).
	resp, out := do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{
		"citizen_id": "wheelchair-1", "role": "priority", "type": "medical", "lat": 40.3870, "lng": 49.8300,
	}, "")
	if resp.StatusCode != 201 {
		t.Fatalf("priority sos: %d %v", resp.StatusCode, out)
	}
	sos := out["sos"].(map[string]any)
	if sos["priority"] != float64(1) || sos["role"] != "priority" {
		t.Fatalf("expected priority 1 / role priority, got %v", sos)
	}
	// A normal citizen stays at priority 2.
	_, out = do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{"citizen_id": "walker-1", "type": "fire", "lat": 40.3870, "lng": 49.8300}, "")
	if out["sos"].(map[string]any)["priority"] != float64(2) {
		t.Fatalf("expected priority 2, got %v", out["sos"])
	}
	// Dispatcher list: critical first.
	_, out = do(t, "GET", srv.URL+"/api/v1/dispatch/sos", nil, testKey)
	list := out["sos"].([]any)
	if list[0].(map[string]any)["citizen_id"] != "wheelchair-1" {
		t.Fatalf("critical SOS must be listed first: %v", list[0])
	}

	// Accessible plan: no bunkers (stairs), route over accessible roads only.
	resp, out = do(t, "GET", srv.URL+"/api/v1/evacuation/plan?lat=40.3870&lng=49.8300&role=priority", nil, "")
	if resp.StatusCode != 200 || out["accessible"] != true {
		t.Fatalf("accessible plan: %d %v", resp.StatusCode, out)
	}
	all := []any{out["recommended"]}
	all = append(all, out["alternatives"].([]any)...)
	for _, o := range all {
		opt := o.(map[string]any)
		sh := opt["shelter"].(map[string]any)
		if sh["accessible"] != true || sh["type"] == "shelter" {
			t.Fatalf("non-accessible shelter offered to a priority citizen: %v", sh["name"])
		}
		if route, ok := opt["route"].(map[string]any); ok && route["accessible"] != true {
			t.Fatalf("route is not flagged accessible: %v", route)
		}
	}
	// The default plan from the same spot does offer the (non-accessible) bunker.
	_, out = do(t, "GET", srv.URL+"/api/v1/evacuation/plan?lat=40.3870&lng=49.8300", nil, "")
	if out["accessible"] != false {
		t.Fatalf("default plan must not be accessible-restricted: %v", out["accessible"])
	}
}

func TestProfileAndSMSNotification(t *testing.T) {
	srv := newTestServer(t)

	// Invalid phone is rejected; a local AZ number is normalised to +994.
	resp, out := do(t, "PUT", srv.URL+"/api/v1/citizens/cit-1/profile", map[string]any{"name": "Leyla", "phone": "12"}, "")
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for bad phone, got %d %v", resp.StatusCode, out)
	}
	resp, out = do(t, "PUT", srv.URL+"/api/v1/citizens/cit-1/profile", map[string]any{"name": "Leyla", "phone": "050 123 45 67", "email": "l@example.com", "role": "priority"}, "")
	if resp.StatusCode != 200 {
		t.Fatalf("profile: %d %v", resp.StatusCode, out)
	}
	c := out["citizen"].(map[string]any)
	if c["phone"] != "+994501234567" || c["role"] != "priority" {
		t.Fatalf("unexpected citizen %v", c)
	}

	// SOS from that citizen: role comes from the profile, SMS is sent with the nearest accessible shelter.
	resp, out = do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{"citizen_id": "cit-1", "type": "medical", "lat": 40.3870, "lng": 49.8300}, "")
	if resp.StatusCode != 201 {
		t.Fatalf("sos: %d %v", resp.StatusCode, out)
	}
	sos := out["sos"].(map[string]any)
	if sos["priority"] != float64(1) || sos["citizen_phone"] != "+994501234567" || sos["citizen_name"] != "Leyla" {
		t.Fatalf("profile not applied to SOS: %v", sos)
	}
	n, _ := out["notification"].(map[string]any)
	if n == nil || n["status"] != "simulated" || n["to"] != "+994501234567" {
		t.Fatalf("expected simulated SMS, got %v", out["notification"])
	}
	msg := n["message"].(string)
	if !strings.Contains(msg, "Leyla") || !strings.Contains(msg, "Zabitlər Parkı") || !strings.Contains(msg, "role=priority") {
		t.Fatalf("unexpected SMS text: %s", msg)
	}

	// Anonymous SOS: no phone → no SMS, but the signal is still accepted.
	resp, out = do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{"citizen_id": "anon-x", "type": "fire", "lat": 40.3870, "lng": 49.8300}, "")
	if resp.StatusCode != 201 || out["notification"] != nil {
		t.Fatalf("anonymous SOS must not send SMS: %d %v", resp.StatusCode, out["notification"])
	}

	// Dispatcher re-sends manually; journal lists both messages.
	resp, out = do(t, "POST", srv.URL+"/api/v1/dispatch/sos/"+sos["id"].(string)+"/notify", nil, testKey)
	if resp.StatusCode != 200 || out["notification"].(map[string]any)["reason"] != "manual" {
		t.Fatalf("manual notify: %d %v", resp.StatusCode, out)
	}
	_, out = do(t, "GET", srv.URL+"/api/v1/dispatch/notifications", nil, testKey)
	if len(out["notifications"].([]any)) != 2 {
		t.Fatalf("expected 2 notifications, got %v", out["notifications"])
	}
}

// Jury scenario via the API: with the external routing API "down" (simulated
// 503) the plan endpoint still answers 200 in offline/backup mode and health
// reports routing_mode=offline; switching the outage off restores online mode.
func TestExternalRoutingOutageKeepsServing(t *testing.T) {
	st := memory.New()
	if err := seed.Apply(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub()
	eng := crisis.New(crisis.DefaultConfig(), st)
	osrm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":1500,"duration":1100,"geometry":{"coordinates":[[49.83,40.387],[49.838,40.39]]}}]}`))
	}))
	defer osrm.Close()
	router := routing.NewChain("auto", routing.NewOSRM(osrm.URL, "driving", time.Second), routing.NewSimulated(1.3))
	svc := service.New(service.Config{ShelterWarnPct: 90, MaxAlternatives: 2}, st, router, eng, hub, log)
	srv := httptest.NewServer(New(Config{DispatcherKey: testKey, RateLimitRPS: 1000, RateLimitBurst: 1000, DefaultLang: "az"}, svc, hub, log))
	defer srv.Close()

	resp, out := do(t, "GET", srv.URL+"/api/v1/evacuation/plan?lat=40.387&lng=49.830", nil, "")
	if resp.StatusCode != 200 || out["mode"] != "online" {
		t.Fatalf("online plan expected: %d %v", resp.StatusCode, out["mode"])
	}
	resp, out = do(t, "POST", srv.URL+"/api/v1/dispatch/chaos", map[string]any{"routing": "503"}, testKey)
	if resp.StatusCode != 200 || out["routing"].(map[string]any)["degraded"] != true {
		t.Fatalf("chaos on: %d %v", resp.StatusCode, out)
	}
	resp, out = do(t, "GET", srv.URL+"/api/v1/evacuation/plan?lat=40.387&lng=49.830", nil, "")
	if resp.StatusCode != 200 || out["mode"] != "offline" || out["offline_reason"] == "" {
		t.Fatalf("offline plan expected: %d mode=%v", resp.StatusCode, out["mode"])
	}
	rec := out["recommended"].(map[string]any)["route"].(map[string]any)
	if rec["offline"] != true || (rec["source"] != "cache" && rec["source"] != "simulated") {
		t.Fatalf("backup route expected, got %v", rec)
	}
	_, out = do(t, "GET", srv.URL+"/api/v1/health", nil, "")
	if out["routing_mode"] != "offline" || out["status"] != "ok" {
		t.Fatalf("health must stay ok but report offline routing: %v", out)
	}
	resp, _ = do(t, "POST", srv.URL+"/api/v1/dispatch/chaos", map[string]any{"routing": "off"}, testKey)
	if resp.StatusCode != 200 {
		t.Fatalf("chaos off: %d", resp.StatusCode)
	}
	_, out = do(t, "GET", srv.URL+"/api/v1/evacuation/plan?lat=40.387&lng=49.830", nil, "")
	if out["mode"] != "online" {
		t.Fatalf("expected online after recovery, got %v", out["mode"])
	}
}


func login(t *testing.T, url, user, pass string) string {
	t.Helper()
	resp, out := do(t, "POST", url+"/api/v1/auth/login", map[string]any{"username": user, "password": pass}, "")
	if resp.StatusCode != 200 {
		t.Fatalf("login %s: %d %v", user, resp.StatusCode, out)
	}
	return out["token"].(string)
}

func doAuth(t *testing.T, method, url string, body any, token string) (*http.Response, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// Jury scenario #4b: JWT + RBAC. Critical operations (brigade dispatch via
// alert execution, chaos switch, brigade status) are admin-only; operators
// manage field data; viewers only read.
func TestJWTRoleBasedAccess(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := do(t, "POST", srv.URL+"/api/v1/auth/login", map[string]any{"username": "admin", "password": "wrong"}, "")
	if resp.StatusCode != 401 {
		t.Fatalf("bad password must be 401, got %d", resp.StatusCode)
	}
	admin, op, view := login(t, srv.URL, "admin", "a"), login(t, srv.URL, "op", "o"), login(t, srv.URL, "view", "v")

	// everyone can read the panel
	for _, tok := range []string{admin, op, view} {
		if resp, _ := doAuth(t, "GET", srv.URL+"/api/v1/dispatch/overview", nil, tok); resp.StatusCode != 200 {
			t.Fatalf("overview should be readable, got %d", resp.StatusCode)
		}
	}
	// viewer cannot change field data; operator can
	if resp, _ := doAuth(t, "PATCH", srv.URL+"/api/v1/dispatch/roads/r-1", map[string]any{"status": "congested"}, view); resp.StatusCode != 403 {
		t.Fatalf("viewer road patch must be 403, got %d", resp.StatusCode)
	}
	if resp, _ := doAuth(t, "PATCH", srv.URL+"/api/v1/dispatch/roads/r-1", map[string]any{"status": "congested"}, op); resp.StatusCode != 200 {
		t.Fatalf("operator road patch must be 200, got %d", resp.StatusCode)
	}
	// only admin may run critical operations
	if resp, _ := doAuth(t, "POST", srv.URL+"/api/v1/dispatch/chaos", map[string]any{"routing": "off"}, op); resp.StatusCode != 403 {
		t.Fatalf("operator chaos must be 403, got %d", resp.StatusCode)
	}
	if resp, _ := doAuth(t, "PATCH", srv.URL+"/api/v1/dispatch/brigades/br-1", map[string]any{"status": "busy"}, op); resp.StatusCode != 403 {
		t.Fatalf("operator brigade patch must be 403, got %d", resp.StatusCode)
	}
	if resp, _ := doAuth(t, "PATCH", srv.URL+"/api/v1/dispatch/brigades/br-1", map[string]any{"status": "busy"}, admin); resp.StatusCode != 200 {
		t.Fatalf("admin brigade patch must be 200, got %d", resp.StatusCode)
	}
	// brigade dispatch through an alert: create a cluster, operator denied, admin allowed
	var alertID string
	for i := 1; i <= 4; i++ {
		_, out := do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{"citizen_id": fmt.Sprintf("rb%d", i), "type": "fire", "lat": 40.3870 + float64(i)*0.0003, "lng": 49.8300}, "")
		if a, _ := out["alert"].(map[string]any); a != nil {
			alertID, _ = a["id"].(string)
		}
	}
	if alertID == "" {
		t.Fatal("expected an alert")
	}
	if resp, _ := doAuth(t, "POST", srv.URL+"/api/v1/dispatch/alerts/"+alertID+"/execute", map[string]any{"action": "dispatch_brigade"}, op); resp.StatusCode != 403 {
		t.Fatalf("operator must not dispatch brigades, got %d", resp.StatusCode)
	}
	if resp, out := doAuth(t, "POST", srv.URL+"/api/v1/dispatch/alerts/"+alertID+"/execute", map[string]any{"action": "dispatch_brigade"}, admin); resp.StatusCode != 200 {
		t.Fatalf("admin dispatch must be 200, got %d %v", resp.StatusCode, out)
	}
	// tampered token is rejected
	if resp, _ := doAuth(t, "GET", srv.URL+"/api/v1/dispatch/overview", nil, admin+"x"); resp.StatusCode != 401 {
		t.Fatalf("tampered token must be 401, got %d", resp.StatusCode)
	}
	_, out := doAuth(t, "GET", srv.URL+"/api/v1/dispatch/session", nil, op)
	if out["role"] != "operator" || out["user"] != "op" {
		t.Fatalf("session should expose role, got %v", out)
	}
}

// Jury scenario #4a: SOS flood. Duplicates are filtered, a flood switches
// anonymous signals to "suspect" (kept off the live queue, ignored by the
// crisis engine) and the API keeps answering fast.
func TestSOSFloodProtection(t *testing.T) {
	srv := newTestServer(t)
	// duplicate from the same citizen inside the window → 200 duplicate, nothing new
	resp, out := do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{"citizen_id": "dup-1", "type": "fire", "lat": 40.3870, "lng": 49.8300}, "")
	if resp.StatusCode != 201 {
		t.Fatalf("first sos: %d", resp.StatusCode)
	}
	firstID := out["sos"].(map[string]any)["id"]
	resp, out = do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{"citizen_id": "dup-1", "type": "fire", "lat": 40.3870, "lng": 49.8300}, "")
	if resp.StatusCode != 200 || out["duplicate"] != true || out["sos"].(map[string]any)["id"] != firstID {
		t.Fatalf("expected duplicate, got %d %v", resp.StatusCode, out)
	}
	// outside a flood, a different person at the same spot is a legitimate second signal
	resp, out = do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{"citizen_id": "neighbour", "type": "fire", "lat": 40.3870, "lng": 49.8300}, "")
	if resp.StatusCode != 201 || out["duplicate"] == true {
		t.Fatalf("second person at same spot must be accepted outside a flood, got %d %v", resp.StatusCode, out)
	}

	// flood: 60 anonymous signals from distinct spots → after the threshold they become suspect
	start := time.Now()
	suspects, created := 0, 0
	for i := 0; i < 60; i++ {
		resp, out := do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{
			"citizen_id": fmt.Sprintf("bot-%d", i), "type": "collapse", "lat": 40.4200 + float64(i)*0.0015, "lng": 49.8600 + float64(i%7)*0.0015}, "")
		if resp.StatusCode != 201 {
			t.Fatalf("flood sos %d: %d %v", i, resp.StatusCode, out)
		}
		created++
		if out["sos"].(map[string]any)["suspect"] == true {
			suspects++
		}
	}
	elapsed := time.Since(start)
	if suspects == 0 || suspects > created-20 {
		t.Fatalf("expected suspects after threshold (25/10s), got %d of %d", suspects, created)
	}
	if avg := elapsed / time.Duration(created); avg > 300*time.Millisecond {
		t.Fatalf("average SOS latency too high: %s", avg)
	}
	// during the flood, a bot re-using an already reported spot+type is dropped as a duplicate
	resp, out = do(t, "POST", srv.URL+"/api/v1/sos", map[string]any{"citizen_id": "bot-rotated-id", "type": "collapse", "lat": 40.4200, "lng": 49.8600}, "")
	if resp.StatusCode != 200 || out["duplicate"] != true {
		t.Fatalf("same-spot duplicate expected during flood, got %d %v", resp.StatusCode, out)
	}
	// live queue hides suspects, security block reports the flood
	_, ov := do(t, "GET", srv.URL+"/api/v1/dispatch/overview", nil, testKey)
	if int(ov["suspect_sos"].(float64)) != suspects {
		t.Fatalf("overview suspect_sos=%v want %d", ov["suspect_sos"], suspects)
	}
	for _, s := range ov["sos"].([]any) {
		if s.(map[string]any)["suspect"] == true {
			t.Fatal("suspect SOS must not appear in the live queue")
		}
	}
	sec := ov["security"].(map[string]any)
	if sec["flood_mode"] != true || sec["duplicates_dropped"].(float64) < 2 {
		t.Fatalf("security status wrong: %v", sec)
	}
	// no red alert from bots: the only alerts allowed came from real signals (none here)
	for _, a := range ov["alerts"].([]any) {
		if a.(map[string]any)["zone_id"] == "binagadi" || a.(map[string]any)["zone_id"] == "sabunchu" {
			t.Fatalf("bot flood must not trigger alerts: %v", a)
		}
	}
	_, m := do(t, "GET", srv.URL+"/api/v1/metrics", nil, "")
	if m["latency"].(map[string]any)["sos"] == nil {
		t.Fatalf("metrics should report sos latency: %v", m)
	}
}
