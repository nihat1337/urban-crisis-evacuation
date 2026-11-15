package api

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/auth"
	"github.com/nihat1337/urban-crisis-evacuation/internal/crisis"
	"github.com/nihat1337/urban-crisis-evacuation/internal/dispatch"
	"github.com/nihat1337/urban-crisis-evacuation/internal/events"
	"github.com/nihat1337/urban-crisis-evacuation/internal/routing"
	"github.com/nihat1337/urban-crisis-evacuation/internal/seed"
	"github.com/nihat1337/urban-crisis-evacuation/internal/service"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store/memory"
)

// newSMSTestServer is newTestServer plus the SMS evacuation module and the
// dev helpers (DevMode), wired exactly like cmd/server.
func newSMSTestServer(t *testing.T) *httptest.Server {
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
	ds := dispatch.New(dispatch.Config{PublicURL: "http://localhost:8080", Workers: 20}, st, dispatch.SMSSenderFunc(svc.SendSMS), hub, log)
	h := New(Config{DispatcherKey: testKey, RateLimitRPS: 100000, RateLimitBurst: 100000, DefaultLang: "az", Version: "test",
		JWTSecret: []byte("test-secret"), Users: auth.ParseUsers("admin:a:admin,op:o:operator,view:v:viewer"),
		SOSRatePerMin: 6000000, SOSBurst: 100000, Dispatch: ds, DevMode: true}, svc, hub, log)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestSMSEvacuationModule(t *testing.T) {
	srv := newSMSTestServer(t)
	admin, op, view := login(t, srv.URL, "admin", "a"), login(t, srv.URL, "op", "o"), login(t, srv.URL, "view", "v")

	// every SMS endpoint is protected like the rest of the dispatcher API
	if resp, _ := do(t, "GET", srv.URL+"/api/v1/dispatch/active-zones", nil, ""); resp.StatusCode != 401 {
		t.Fatalf("active-zones without login must be 401, got %d", resp.StatusCode)
	}
	if resp, _ := do(t, "POST", srv.URL+"/api/dispatch/zones/khatai/sms-plan", nil, ""); resp.StatusCode != 401 {
		t.Fatalf("sms-plan without login must be 401, got %d", resp.StatusCode)
	}
	if resp, _ := do(t, "POST", srv.URL+"/dev/seed", map[string]any{"district": "khatai"}, ""); resp.StatusCode != 401 {
		t.Fatalf("dev seed without login must be 401, got %d", resp.StatusCode)
	}
	if resp, _ := doAuth(t, "GET", srv.URL+"/api/v1/dispatch/active-zones", nil, view); resp.StatusCode != 200 {
		t.Fatalf("viewer may read active zones, got %d", resp.StatusCode)
	}
	if resp, _ := doAuth(t, "POST", srv.URL+"/api/v1/dispatch/zones/khatai/sms-plan", nil, view); resp.StatusCode != 403 {
		t.Fatalf("viewer must not build/send SMS plans, got %d", resp.StatusCode)
	}
	if resp, _ := doAuth(t, "POST", srv.URL+"/api/v1/dev/seed", map[string]any{"district": "khatai"}, op); resp.StatusCode != 403 {
		t.Fatalf("operator must not seed demo data, got %d", resp.StatusCode)
	}

	// admin seeds 250 citizens + 6 points around Xətai and injects an incident
	if resp, _ := doAuth(t, "POST", srv.URL+"/api/v1/dev/seed", map[string]any{"district": "khatai", "citizens": 250}, admin); resp.StatusCode != 200 {
		t.Fatalf("admin seed must be 200, got %d", resp.StatusCode)
	}
	if resp, _ := doAuth(t, "POST", srv.URL+"/api/v1/dev/incidents", map[string]any{"district": "khatai", "count": 4}, admin); resp.StatusCode != 200 {
		t.Fatalf("admin incident must be 200, got %d", resp.StatusCode)
	}
	_, az := doAuth(t, "GET", srv.URL+"/api/v1/dispatch/active-zones", nil, view)
	zs, _ := az["zones"].([]any)
	if len(zs) == 0 || zs[0].(map[string]any)["district_id"] != "khatai" || zs[0].(map[string]any)["status"] != "affected" {
		t.Fatalf("khatai must be the first affected zone, got %v", az)
	}

	// operator builds the plan (spec path) …
	resp, p := doAuth(t, "POST", srv.URL+"/api/dispatch/zones/khatai/sms-plan", nil, op)
	if resp.StatusCode != 200 {
		t.Fatalf("operator sms-plan must be 200, got %d %v", resp.StatusCode, p)
	}
	total := int(p["total_recipients"].(float64)) + int(p["unassigned"].(float64)) + int(p["no_location"].(float64))
	if total < 250 {
		t.Fatalf("every seeded citizen must be reached, got %d", total)
	}
	for _, x := range p["points"].([]any) {
		if n := int(x.(map[string]any)["assigned"].(float64)); n > 100 {
			t.Fatalf("a point got %d people, max is 100", n)
		}
	}
	// … and sends it: everyone gets an SMS, a second confirm is refused
	id := p["id"].(string)
	resp, r := doAuth(t, "POST", srv.URL+"/api/v1/dispatch/sms-plans/"+id+"/confirm", nil, op)
	if resp.StatusCode != 200 || int(r["sent"].(float64)) != total || int(r["failed"].(float64)) != 0 {
		t.Fatalf("confirm must send %d SMS, got %d %v", total, resp.StatusCode, r)
	}
	if resp, _ := doAuth(t, "POST", srv.URL+"/api/v1/dispatch/sms-plans/"+id+"/confirm", nil, op); resp.StatusCode != 409 {
		t.Fatalf("second confirm must be 409, got %d", resp.StatusCode)
	}
	if resp, _ := doAuth(t, "POST", srv.URL+"/api/v1/dispatch/zones/nowhere/sms-plan", nil, op); resp.StatusCode != 400 {
		t.Fatalf("unknown district must be 400, got %d", resp.StatusCode)
	}
}
