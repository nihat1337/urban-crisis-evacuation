// Package service is the application layer: it orchestrates the store, the
// router, the crisis engine and the event hub. HTTP handlers stay thin.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/crisis"
	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
	"github.com/nihat1337/urban-crisis-evacuation/internal/events"
	"github.com/nihat1337/urban-crisis-evacuation/internal/routing"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store"
)

// Error is a typed failure the API layer maps to an HTTP status and an i18n message.
type Error struct {
	Status int
	Code   string // i18n suffix: err.<Code>
	Err    error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Err.Error()
	}
	return e.Code
}
func (e *Error) Unwrap() error { return e.Err }

func invalid(code string) *Error  { return &Error{Status: 400, Code: code} }
func notFound() *Error            { return &Error{Status: 404, Code: "not_found"} }
func conflict(code string) *Error { return &Error{Status: 409, Code: code} }

type Config struct {
	ShelterWarnPct  float64
	MaxAlternatives int
}

type statusReporter interface{ Status() routing.Status }

type Service struct {
	cfg    Config
	store  store.Store
	router routing.Router
	engine *crisis.Engine
	hub    *events.Hub
	log    *slog.Logger
}

func New(cfg Config, st store.Store, rt routing.Router, eng *crisis.Engine, hub *events.Hub, log *slog.Logger) *Service {
	if cfg.MaxAlternatives <= 0 {
		cfg.MaxAlternatives = 2
	}
	if cfg.ShelterWarnPct <= 0 {
		cfg.ShelterWarnPct = 90
	}
	return &Service{cfg: cfg, store: st, router: rt, engine: eng, hub: hub, log: log}
}

func (s *Service) StoreName() string { return s.store.Name() }

func (s *Service) RoutingStatus() routing.Status {
	if r, ok := s.router.(statusReporter); ok {
		return r.Status()
	}
	return routing.Status{Mode: s.router.Name()}
}

func (s *Service) Ping(ctx context.Context) error { return s.store.Ping(ctx) }

func (s *Service) Zones(ctx context.Context) ([]domain.Zone, error) { return s.store.ListZones(ctx) }

func (s *Service) Roads(ctx context.Context) ([]domain.Road, error) { return s.store.ListRoads(ctx) }

func (s *Service) nearestZone(ctx context.Context, p domain.Point) (domain.Zone, error) {
	zones, err := s.store.ListZones(ctx)
	if err != nil {
		return domain.Zone{}, err
	}
	if len(zones) == 0 {
		return domain.Zone{}, errors.New("no zones configured")
	}
	best := zones[0]
	bestD := math.MaxFloat64
	for _, z := range zones {
		if d := domain.DistanceM(p, z.Center); d < bestD {
			best, bestD = z, d
		}
	}
	return best, nil
}

func (s *Service) zoneByID(ctx context.Context, id string) (domain.Zone, error) {
	zones, err := s.store.ListZones(ctx)
	if err != nil {
		return domain.Zone{}, err
	}
	for _, z := range zones {
		if z.ID == id {
			return z, nil
		}
	}
	return domain.Zone{}, notFound()
}

// resolveOrigin turns (optional point, optional zone) into a concrete origin and zone id.
func (s *Service) resolveOrigin(ctx context.Context, origin *domain.Point, zoneID string) (domain.Point, string, error) {
	switch {
	case origin == nil && zoneID == "":
		return domain.Point{}, "", invalid("location_required")
	case origin == nil:
		z, err := s.zoneByID(ctx, zoneID)
		if err != nil {
			return domain.Point{}, "", err
		}
		return z.Center, z.ID, nil
	case zoneID == "":
		z, err := s.nearestZone(ctx, *origin)
		if err != nil {
			return domain.Point{}, "", err
		}
		return *origin, z.ID, nil
	}
	return *origin, zoneID, nil
}

// NearestShelters lists active shelters by distance from the origin.
func (s *Service) NearestShelters(ctx context.Context, origin *domain.Point, zoneID string, limit int) ([]domain.ShelterOption, error) {
	o, _, err := s.resolveOrigin(ctx, origin, zoneID)
	if err != nil {
		return nil, err
	}
	shelters, err := s.store.ListShelters(ctx)
	if err != nil {
		return nil, err
	}
	opts := make([]domain.ShelterOption, 0, len(shelters))
	for _, sh := range shelters {
		if !sh.Active {
			continue
		}
		opts = append(opts, domain.ShelterOption{
			Shelter:      sh,
			DistanceM:    math.Round(domain.DistanceM(o, sh.Location)),
			OccupancyPct: sh.OccupancyPct(),
		})
	}
	sort.Slice(opts, func(i, j int) bool { return opts[i].DistanceM < opts[j].DistanceM })
	for i := range opts {
		if opts[i].OccupancyPct < s.cfg.ShelterWarnPct {
			opts[i].Recommended = true
			break
		}
	}
	if limit > 0 && len(opts) > limit {
		opts = opts[:limit]
	}
	return opts, nil
}

// Plan builds the evacuation plan: routes to the nearest non-full shelters are
// computed concurrently and the fastest one is recommended.
func (s *Service) Plan(ctx context.Context, origin *domain.Point, zoneID string) (domain.EvacuationPlan, error) {
	o, zid, err := s.resolveOrigin(ctx, origin, zoneID)
	if err != nil {
		return domain.EvacuationPlan{}, err
	}
	opts, err := s.NearestShelters(ctx, &o, zid, 0)
	if err != nil {
		return domain.EvacuationPlan{}, err
	}
	roads, err := s.store.ListRoads(ctx)
	if err != nil {
		return domain.EvacuationPlan{}, err
	}
	plan := domain.EvacuationPlan{Origin: o, ZoneID: zid, GeneratedAt: time.Now().UTC()}

	var usable []domain.ShelterOption
	for _, op := range opts {
		if op.OccupancyPct < s.cfg.ShelterWarnPct {
			usable = append(usable, op)
		}
	}
	if len(usable) == 0 {
		plan.Warnings = append(plan.Warnings, "all_shelters_full")
		usable = opts
	}
	if len(usable) == 0 {
		return plan, nil
	}
	n := len(usable)
	if n > 1+s.cfg.MaxAlternatives {
		n = 1 + s.cfg.MaxAlternatives
	}
	cands := usable[:n]

	var wg sync.WaitGroup
	for i := range cands {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := s.router.Route(ctx, o, cands[i].Shelter.Location, roads)
			if err != nil {
				s.log.Warn("route failed", "shelter", cands[i].Shelter.ID, "err", err)
				return
			}
			cands[i].Route = &r
		}(i)
	}
	wg.Wait()

	sort.SliceStable(cands, func(i, j int) bool {
		di, dj := math.MaxFloat64, math.MaxFloat64
		if cands[i].Route != nil {
			di = cands[i].Route.DurationS
		}
		if cands[j].Route != nil {
			dj = cands[j].Route.DurationS
		}
		return di < dj
	})
	for i := range cands {
		cands[i].Recommended = i == 0
	}
	plan.Recommended = &cands[0]
	plan.Alternatives = cands[1:]
	if plan.Recommended.Route != nil && plan.Recommended.Route.Detour {
		plan.Warnings = append(plan.Warnings, "detour")
	}
	if plan.Recommended.Route != nil && plan.Recommended.Route.Source != "osrm" {
		plan.Warnings = append(plan.Warnings, "routing_fallback")
	}
	return plan, nil
}

type SOSInput struct {
	CitizenID string         `json:"citizen_id"`
	Type      domain.SOSType `json:"type"`
	Lat       *float64       `json:"lat"`
	Lng       *float64       `json:"lng"`
	ZoneID    string         `json:"zone_id"`
	Message   string         `json:"message"`
}

// SubmitSOS stores the signal, marks the citizen as needing help, and runs the crisis engine.
func (s *Service) SubmitSOS(ctx context.Context, in SOSInput) (domain.SOS, *domain.Alert, error) {
	if !in.Type.Valid() {
		return domain.SOS{}, nil, invalid("invalid_sos_type")
	}
	var origin *domain.Point
	if in.Lat != nil && in.Lng != nil {
		origin = &domain.Point{Lat: *in.Lat, Lng: *in.Lng}
	}
	o, zid, err := s.resolveOrigin(ctx, origin, in.ZoneID)
	if err != nil {
		return domain.SOS{}, nil, err
	}
	if in.CitizenID == "" {
		in.CitizenID = "anon-" + domain.NewID()
	}
	if len(in.Message) > 500 {
		in.Message = in.Message[:500]
	}
	now := time.Now().UTC()
	sos := domain.SOS{
		ID: domain.NewID(), CitizenID: in.CitizenID, Type: in.Type, ZoneID: zid,
		Location: o, Message: in.Message, Status: domain.SOSOpen, CreatedAt: now,
	}
	if err := s.store.CreateSOS(ctx, sos); err != nil {
		return domain.SOS{}, nil, err
	}
	_ = s.store.UpsertCitizen(ctx, domain.Citizen{ID: in.CitizenID, Status: domain.CitizenNeedHelp, Location: &o, UpdatedAt: now})
	s.hub.Publish("sos.created", sos)

	alert, err := s.engine.OnSOS(ctx, sos)
	if err != nil {
		s.log.Error("crisis engine", "err", err)
		return sos, nil, nil
	}
	if alert != nil {
		s.log.Warn("RED ALERT", "kind", alert.Kind, "zone", alert.ZoneID, "signals", alert.SignalCount)
		s.hub.Publish("alert.created", alert)
	}
	return sos, alert, nil
}

func (s *Service) UpdateCitizenStatus(ctx context.Context, id string, status domain.CitizenStatus, loc *domain.Point) (domain.Citizen, error) {
	if !status.Valid() {
		return domain.Citizen{}, invalid("invalid_status")
	}
	if id == "" {
		return domain.Citizen{}, invalid("invalid_json")
	}
	c := domain.Citizen{ID: id, Status: status, Location: loc, UpdatedAt: time.Now().UTC()}
	if err := s.store.UpsertCitizen(ctx, c); err != nil {
		return domain.Citizen{}, err
	}
	s.hub.Publish("citizen.updated", c)
	return c, nil
}

// SetShelter updates occupancy/active flag, reroutes citizens if the warn
// threshold is reached and asks the crisis engine about the alert threshold.
func (s *Service) SetShelter(ctx context.Context, id string, occupancy *int, active *bool) (domain.Shelter, *domain.Alert, error) {
	sh, err := s.store.GetShelter(ctx, id)
	if err != nil {
		return domain.Shelter{}, nil, err
	}
	if occupancy != nil {
		v := *occupancy
		if v < 0 {
			v = 0
		}
		if v > sh.Capacity {
			v = sh.Capacity
		}
		sh.Occupancy = v
	}
	if active != nil {
		sh.Active = *active
	}
	if err := s.store.UpsertShelter(ctx, sh); err != nil {
		return domain.Shelter{}, nil, err
	}
	s.hub.Publish("shelter.updated", sh)
	if sh.OccupancyPct() >= s.cfg.ShelterWarnPct || !sh.Active {
		s.hub.Publish("route.invalidated", map[string]any{
			"reason": "shelter_full", "shelter_id": sh.ID, "shelter_name": sh.Name, "occupancy_pct": sh.OccupancyPct(),
		})
	}
	alert, err := s.engine.OnShelter(ctx, sh)
	if err != nil {
		s.log.Error("crisis engine", "err", err)
		return sh, nil, nil
	}
	if alert != nil {
		s.hub.Publish("alert.created", alert)
	}
	return sh, alert, nil
}

func (s *Service) SetRoadStatus(ctx context.Context, id string, status domain.RoadStatus) (domain.Road, error) {
	if !status.Valid() {
		return domain.Road{}, invalid("invalid_road_status")
	}
	r, err := s.store.GetRoad(ctx, id)
	if err != nil {
		return domain.Road{}, err
	}
	r.Status = status
	r.UpdatedAt = time.Now().UTC()
	if err := s.store.UpsertRoad(ctx, r); err != nil {
		return domain.Road{}, err
	}
	s.hub.Publish("road.updated", r)
	reason := "road_updated"
	if status == domain.RoadBlocked {
		reason = "road_blocked"
	}
	s.hub.Publish("route.invalidated", map[string]any{"reason": reason, "road_id": r.ID, "road_name": r.Name, "status": r.Status})
	return r, nil
}

type ExecuteResult struct {
	Alert   domain.Alert    `json:"alert"`
	Brigade *domain.Brigade `json:"brigade,omitempty"`
	Shelter *domain.Shelter `json:"shelter,omitempty"`
	SOS     *domain.SOS     `json:"sos,omitempty"`
}

// ExecuteAlert performs the one-click decision attached to an alert.
func (s *Service) ExecuteAlert(ctx context.Context, id string, action domain.AlertAction) (ExecuteResult, error) {
	a, err := s.store.GetAlert(ctx, id)
	if err != nil {
		return ExecuteResult{}, err
	}
	if a.Executed {
		return ExecuteResult{}, conflict("already_executed")
	}
	if !action.Valid() {
		return ExecuteResult{}, invalid("invalid_action")
	}
	res := ExecuteResult{}
	now := time.Now().UTC()

	switch action {
	case domain.ActionDispatchBrigade:
		brigades, err := s.store.ListBrigades(ctx)
		if err != nil {
			return res, err
		}
		var best *domain.Brigade
		bestD := math.MaxFloat64
		for i := range brigades {
			b := brigades[i]
			if b.Status != domain.BrigadeAvailable {
				continue
			}
			if d := domain.DistanceM(b.Location, a.Center); d < bestD {
				best, bestD = &brigades[i], d
			}
		}
		if best == nil {
			return res, conflict("no_brigade")
		}
		best.Status = domain.BrigadeEnRoute
		best.ZoneID = a.ZoneID
		best.UpdatedAt = now
		// Attach the brigade to the most recent open SOS in the zone, if any.
		if list, err := s.store.ListSOS(ctx, time.Time{}); err == nil {
			for i := range list {
				if list[i].ZoneID == a.ZoneID && list[i].Status == domain.SOSOpen {
					list[i].Status = domain.SOSAssigned
					list[i].BrigadeID = best.ID
					best.AssignedSOSID = list[i].ID
					if err := s.store.UpdateSOS(ctx, list[i]); err == nil {
						res.SOS = &list[i]
						s.hub.Publish("sos.updated", list[i])
					}
					break
				}
			}
		}
		if err := s.store.UpsertBrigade(ctx, *best); err != nil {
			return res, err
		}
		res.Brigade = best
		s.hub.Publish("brigade.updated", best)

	case domain.ActionOpenShelter:
		zoneName := a.ZoneID
		if z, err := s.zoneByID(ctx, a.ZoneID); err == nil {
			zoneName = z.Name
		}
		// ~300 m north-east of the hotspot so it does not sit inside the danger area.
		loc := domain.Point{Lat: a.Center.Lat + 0.0027, Lng: a.Center.Lng + 0.0035}
		sh := domain.Shelter{
			ID: "sh-tmp-" + domain.NewID(), Name: fmt.Sprintf("Müvəqqəti toplanış nöqtəsi — %s", zoneName),
			Type: domain.ShelterTemporary, ZoneID: a.ZoneID, Location: loc, Capacity: 1000, Occupancy: 0, Active: true,
		}
		if err := s.store.UpsertShelter(ctx, sh); err != nil {
			return res, err
		}
		// Make it reachable on the simulated graph.
		if z, err := s.zoneByID(ctx, a.ZoneID); err == nil {
			_ = s.store.UpsertRoad(ctx, domain.Road{ID: "r-" + sh.ID, Name: sh.Name + " girişi", From: z.Center, To: loc, Status: domain.RoadOpen, UpdatedAt: now})
		}
		res.Shelter = &sh
		s.hub.Publish("shelter.created", sh)
		s.hub.Publish("route.invalidated", map[string]any{"reason": "shelter_opened", "shelter_id": sh.ID, "shelter_name": sh.Name})
	}

	a.Executed = true
	a.ExecutedAction = action
	a.ExecutedAt = &now
	if err := s.store.UpsertAlert(ctx, a); err != nil {
		return res, err
	}
	res.Alert = a
	s.hub.Publish("alert.executed", a)
	return res, nil
}

func (s *Service) UpdateBrigade(ctx context.Context, id string, status *domain.BrigadeStatus, loc *domain.Point) (domain.Brigade, error) {
	b, err := s.store.GetBrigade(ctx, id)
	if err != nil {
		return domain.Brigade{}, err
	}
	if status != nil {
		if !status.Valid() {
			return domain.Brigade{}, invalid("invalid_status")
		}
		b.Status = *status
		if *status == domain.BrigadeAvailable {
			b.AssignedSOSID = ""
		}
	}
	if loc != nil {
		b.Location = *loc
	}
	b.UpdatedAt = time.Now().UTC()
	if err := s.store.UpsertBrigade(ctx, b); err != nil {
		return domain.Brigade{}, err
	}
	s.hub.Publish("brigade.updated", b)
	return b, nil
}

func (s *Service) ResolveSOS(ctx context.Context, id string) (domain.SOS, error) {
	v, err := s.store.GetSOS(ctx, id)
	if err != nil {
		return domain.SOS{}, err
	}
	v.Status = domain.SOSResolved
	if err := s.store.UpdateSOS(ctx, v); err != nil {
		return domain.SOS{}, err
	}
	if v.BrigadeID != "" {
		if b, err := s.store.GetBrigade(ctx, v.BrigadeID); err == nil && b.AssignedSOSID == v.ID {
			b.Status = domain.BrigadeAvailable
			b.AssignedSOSID = ""
			b.UpdatedAt = time.Now().UTC()
			_ = s.store.UpsertBrigade(ctx, b)
			s.hub.Publish("brigade.updated", b)
		}
	}
	s.hub.Publish("sos.updated", v)
	return v, nil
}

func (s *Service) ListSOS(ctx context.Context, since time.Duration) ([]domain.SOS, error) {
	var from time.Time
	if since > 0 {
		from = time.Now().UTC().Add(-since)
	}
	list, err := s.store.ListSOS(ctx, from)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []domain.SOS{}
	}
	return list, nil
}

type ZoneStat struct {
	domain.Zone
	OpenSOS         int     `json:"open_sos"`
	RecentSOS       int     `json:"recent_sos"`
	Shelters        int     `json:"shelters"`
	AvgOccupancyPct float64 `json:"avg_occupancy_pct"`
	OpenAlerts      int     `json:"open_alerts"`
}

type Overview struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Zones       []ZoneStat       `json:"zones"`
	Shelters    []domain.Shelter `json:"shelters"`
	SOS         []domain.SOS     `json:"sos"`
	Brigades    []domain.Brigade `json:"brigades"`
	Alerts      []domain.Alert   `json:"alerts"`
	Roads       []domain.Road    `json:"roads"`
	Citizens    map[string]int   `json:"citizens"`
	Routing     routing.Status   `json:"routing"`
	Store       string           `json:"store"`
	Crisis      crisis.Config    `json:"crisis"`
	Thresholds  map[string]any   `json:"thresholds"`
}

// Overview is the single payload behind the live dispatcher panel.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	now := time.Now().UTC()
	ov := Overview{GeneratedAt: now, Store: s.store.Name(), Routing: s.RoutingStatus(), Crisis: s.engine.Config(),
		Citizens: map[string]int{}, Thresholds: map[string]any{"shelter_warn_pct": s.cfg.ShelterWarnPct}}
	var err error
	if ov.Shelters, err = s.store.ListShelters(ctx); err != nil {
		return ov, err
	}
	if ov.SOS, err = s.store.ListSOS(ctx, now.Add(-24*time.Hour)); err != nil {
		return ov, err
	}
	if len(ov.SOS) > 300 {
		ov.SOS = ov.SOS[:300]
	}
	if ov.Brigades, err = s.store.ListBrigades(ctx); err != nil {
		return ov, err
	}
	if ov.Alerts, err = s.store.ListAlerts(ctx); err != nil {
		return ov, err
	}
	if len(ov.Alerts) > 100 {
		ov.Alerts = ov.Alerts[:100]
	}
	if ov.Roads, err = s.store.ListRoads(ctx); err != nil {
		return ov, err
	}
	zones, err := s.store.ListZones(ctx)
	if err != nil {
		return ov, err
	}
	citizens, err := s.store.ListCitizens(ctx)
	if err != nil {
		return ov, err
	}
	for _, c := range citizens {
		ov.Citizens[string(c.Status)]++
	}
	window := s.engine.Config().Window
	for _, z := range zones {
		st := ZoneStat{Zone: z}
		var pctSum float64
		for _, sh := range ov.Shelters {
			if sh.ZoneID == z.ID && sh.Active {
				st.Shelters++
				pctSum += sh.OccupancyPct()
			}
		}
		if st.Shelters > 0 {
			st.AvgOccupancyPct = math.Round(pctSum/float64(st.Shelters)*10) / 10
		}
		for _, v := range ov.SOS {
			if v.ZoneID != z.ID {
				continue
			}
			if v.Status != domain.SOSResolved {
				st.OpenSOS++
			}
			if now.Sub(v.CreatedAt) <= window {
				st.RecentSOS++
			}
		}
		for _, a := range ov.Alerts {
			if a.ZoneID == z.ID && !a.Executed {
				st.OpenAlerts++
			}
		}
		ov.Zones = append(ov.Zones, st)
	}
	for _, p := range []*[]domain.SOS{&ov.SOS} {
		if *p == nil {
			*p = []domain.SOS{}
		}
	}
	if ov.Alerts == nil {
		ov.Alerts = []domain.Alert{}
	}
	return ov, nil
}
