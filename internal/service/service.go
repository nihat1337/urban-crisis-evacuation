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
	"strings"
	"sync"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/crisis"
	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
	"github.com/nihat1337/urban-crisis-evacuation/internal/events"
	"github.com/nihat1337/urban-crisis-evacuation/internal/notify"
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
	// Anti-flood: identical signals inside DedupeWindow are dropped; more than
	// FloodThreshold signals per 10 s switches flood mode on, in which
	// anonymous signals are stored as "suspect" and kept off the live queue.
	DedupeWindow   time.Duration
	FloodThreshold int
}

type dedupeEntry struct {
	at    time.Time
	sosID string
}

// floodGuard is the in-memory SOS flood detector (O(1) per request).
type floodGuard struct {
	mu         sync.Mutex
	dedupe     map[string]dedupeEntry
	recent     []time.Time // creation times within the last 10 s
	active     bool
	since      time.Time
	duplicates int64
	suspects   int64
	ratePeak   int
}

type statusReporter interface{ Status() routing.Status }

type Service struct {
	cfg    Config
	store  store.Store
	router routing.Router
	engine *crisis.Engine
	hub    *events.Hub
	log    *slog.Logger

	notifier  notify.Sender
	publicURL string
	notifyMu  sync.Mutex
	lastSMS   map[string]time.Time // citizen id → last reroute SMS (rate limit)

	flood floodGuard
}

func New(cfg Config, st store.Store, rt routing.Router, eng *crisis.Engine, hub *events.Hub, log *slog.Logger) *Service {
	if cfg.MaxAlternatives <= 0 {
		cfg.MaxAlternatives = 2
	}
	if cfg.ShelterWarnPct <= 0 {
		cfg.ShelterWarnPct = 90
	}
	if cfg.DedupeWindow <= 0 {
		cfg.DedupeWindow = 30 * time.Second
	}
	if cfg.FloodThreshold <= 0 {
		cfg.FloodThreshold = 60
	}
	return &Service{cfg: cfg, store: st, router: rt, engine: eng, hub: hub, log: log,
		notifier: notify.NewLogSender(log), publicURL: "http://localhost:8080", lastSMS: map[string]time.Time{},
		flood: floodGuard{dedupe: map[string]dedupeEntry{}}}
}

// checkDuplicate returns the id of an identical recent signal, if any, and
// otherwise registers this one. Keys: the citizen id, and type+location
// rounded to ~11 m (catches bots that rotate ids but not coordinates).
func (s *Service) checkDuplicate(citizenID string, typ domain.SOSType, p domain.Point, sosID string) (string, bool) {
	now := time.Now()
	s.flood.mu.Lock()
	defer s.flood.mu.Unlock()
	citizenKey := "c:" + citizenID
	geoKey := fmt.Sprintf("g:%s|%.4f,%.4f", typ, p.Lat, p.Lng)
	check := []string{citizenKey}
	// Same type at the same spot from a "different" id is only treated as a
	// duplicate while a flood is on: bots rotate ids but not coordinates,
	// whereas in normal operation several people in one building legitimately
	// report the same incident and must all count for the crisis rule.
	if s.flood.active {
		check = append(check, geoKey)
	}
	for _, k := range check {
		if e, ok := s.flood.dedupe[k]; ok && now.Sub(e.at) < s.cfg.DedupeWindow {
			s.flood.duplicates++
			return e.sosID, true
		}
	}
	s.flood.dedupe[citizenKey] = dedupeEntry{at: now, sosID: sosID}
	s.flood.dedupe[geoKey] = dedupeEntry{at: now, sosID: sosID} // always remembered, only enforced during a flood
	if len(s.flood.dedupe) > 20000 { // bounded memory even under attack
		for k, e := range s.flood.dedupe {
			if now.Sub(e.at) >= s.cfg.DedupeWindow {
				delete(s.flood.dedupe, k)
			}
		}
	}
	return "", false
}

// noteSOS records a creation and returns whether flood mode is active.
func (s *Service) noteSOS() bool {
	now := time.Now()
	s.flood.mu.Lock()
	defer s.flood.mu.Unlock()
	cut := now.Add(-10 * time.Second)
	i := 0
	for i < len(s.flood.recent) && s.flood.recent[i].Before(cut) {
		i++
	}
	s.flood.recent = append(s.flood.recent[i:], now)
	n := len(s.flood.recent)
	if n > s.flood.ratePeak {
		s.flood.ratePeak = n
	}
	switch {
	case !s.flood.active && n > s.cfg.FloodThreshold:
		s.flood.active, s.flood.since = true, now
		s.log.Warn("SOS FLOOD detected — flood mode ON", "sos_10s", n, "threshold", s.cfg.FloodThreshold)
		go s.hub.Publish("security.flood", s.securityLocked())
	case s.flood.active && n < s.cfg.FloodThreshold/4 && now.Sub(s.flood.since) > 30*time.Second:
		s.flood.active = false
		s.log.Info("SOS flood over — flood mode OFF", "sos_10s", n)
		go s.hub.Publish("security.flood", s.securityLocked())
	}
	return s.flood.active
}

func (s *Service) securityLocked() map[string]any {
	n := 0
	cut := time.Now().Add(-10 * time.Second)
	for _, t := range s.flood.recent {
		if !t.Before(cut) {
			n++
		}
	}
	return map[string]any{
		"flood_mode": s.flood.active, "sos_10s": n, "threshold_10s": s.cfg.FloodThreshold,
		"duplicates_dropped": s.flood.duplicates, "suspects": s.flood.suspects, "peak_10s": s.flood.ratePeak,
		"dedupe_window_s": int(s.cfg.DedupeWindow.Seconds()),
	}
}

// SecurityStatus is shown on the dispatcher panel.
func (s *Service) SecurityStatus() map[string]any {
	s.flood.mu.Lock()
	defer s.flood.mu.Unlock()
	return s.securityLocked()
}

// WithNotifier swaps the SMS gateway and the public URL used in message links.
func (s *Service) WithNotifier(n notify.Sender, publicURL string) *Service {
	if n != nil {
		s.notifier = n
	}
	if publicURL != "" {
		s.publicURL = strings.TrimRight(publicURL, "/")
	}
	return s
}

func (s *Service) NotifyStatus() map[string]any {
	return map[string]any{"provider": s.notifier.Name(), "simulated": s.notifier.Simulated()}
}

type chaosSetter interface {
	SetChaos(mode string)
	Chaos() string
}

// SetRoutingChaos simulates an external routing outage ("503", "timeout") or
// clears it (""). Used by the dispatcher panel to demonstrate the fallback.
func (s *Service) SetRoutingChaos(mode string) (routing.Status, error) {
	switch mode {
	case "", "off", "503", "timeout":
	default:
		return routing.Status{}, invalid("invalid_action")
	}
	if mode == "off" {
		mode = ""
	}
	c, ok := s.router.(chaosSetter)
	if !ok {
		return routing.Status{}, conflict("invalid_action")
	}
	c.SetChaos(mode)
	st := s.RoutingStatus()
	s.log.Warn("routing chaos switched", "mode", mode, "degraded", st.Degraded)
	s.hub.Publish("routing.status", st)
	return st, nil
}

// StartMonitors watches the routing layer and publishes routing.status over
// SSE whenever it flips between online and offline/backup mode, so every
// open citizen app shows (or hides) the offline banner within seconds.
func (s *Service) StartMonitors(ctx context.Context) {
	// Aggregated security stats while a flood is on (instead of one SSE event per bot request).
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		var lastSuspects int64 = -1
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				st := s.SecurityStatus()
				if st["flood_mode"] == true || st["suspects"].(int64) != lastSuspects {
					lastSuspects = st["suspects"].(int64)
					s.hub.Publish("security.stats", st)
				}
			}
		}
	}()
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		last := s.RoutingStatus().Degraded
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				st := s.RoutingStatus()
				if st.Degraded != last {
					last = st.Degraded
					if st.Degraded {
						s.log.Warn("routing degraded → offline/backup mode", "err", st.LastError)
					} else {
						s.log.Info("routing recovered → online mode")
					}
					s.hub.Publish("routing.status", st)
				}
			}
		}
	}()
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

// NearestShelters lists active shelters by distance from the origin. With
// accessible=true only barrier-free shelters (ramps, no stairs) are returned.
func (s *Service) NearestShelters(ctx context.Context, origin *domain.Point, zoneID string, limit int, accessible bool) ([]domain.ShelterOption, error) {
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
		if !sh.Active || (accessible && !sh.Accessible) {
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
// computed concurrently and the fastest one is recommended. With
// accessible=true (priority citizens) only accessible shelters are considered
// and routes run exclusively over barrier-free roads.
func (s *Service) Plan(ctx context.Context, origin *domain.Point, zoneID string, accessible bool) (domain.EvacuationPlan, error) {
	o, zid, err := s.resolveOrigin(ctx, origin, zoneID)
	if err != nil {
		return domain.EvacuationPlan{}, err
	}
	opts, err := s.NearestShelters(ctx, &o, zid, 0, accessible)
	if err != nil {
		return domain.EvacuationPlan{}, err
	}
	plan := domain.EvacuationPlan{Origin: o, ZoneID: zid, Accessible: accessible, GeneratedAt: time.Now().UTC()}
	if accessible && len(opts) == 0 {
		// Never leave a priority citizen without guidance: fall back to all shelters but say so.
		plan.Warnings = append(plan.Warnings, "no_accessible_shelter")
		if opts, err = s.NearestShelters(ctx, &o, zid, 0, false); err != nil {
			return domain.EvacuationPlan{}, err
		}
	}
	roads, err := s.store.ListRoads(ctx)
	if err != nil {
		return domain.EvacuationPlan{}, err
	}
	routeOpts := routing.Options{Accessible: accessible}

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
			r, err := s.router.Route(ctx, o, cands[i].Shelter.Location, roads, routeOpts)
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
	plan.Mode = "online"
	for _, c := range cands {
		if c.Route != nil && c.Route.Offline {
			plan.Mode = "offline"
			plan.OfflineReason = c.Route.OfflineReason
			break
		}
	}
	if plan.Mode == "offline" {
		plan.Warnings = append(plan.Warnings, "offline_mode")
	}
	if plan.Recommended.Route != nil && plan.Recommended.Route.Detour {
		plan.Warnings = append(plan.Warnings, "detour")
	}
	if plan.Recommended.Route != nil && plan.Recommended.Route.Source != "osrm" && !accessible {
		plan.Warnings = append(plan.Warnings, "routing_fallback")
	}
	if accessible && plan.Recommended.Route != nil && !plan.Recommended.Route.Accessible {
		plan.Warnings = append(plan.Warnings, "accessible_route_unavailable")
	}
	return plan, nil
}

type SOSInput struct {
	CitizenID string             `json:"citizen_id"`
	Role      domain.CitizenRole `json:"role"` // "priority" → critical priority + accessible routing
	Type      domain.SOSType     `json:"type"`
	Lat       *float64           `json:"lat"`
	Lng       *float64           `json:"lng"`
	ZoneID    string             `json:"zone_id"`
	Message   string             `json:"message"`
}

// ProfileInput is the optional citizen profile. No password by design: an
// emergency app must never put a login between a person and the SOS button.
type ProfileInput struct {
	Name  string             `json:"name"`
	Phone string             `json:"phone"`
	Email string             `json:"email"`
	Role  domain.CitizenRole `json:"role"`
}

// UpsertProfile saves name/phone/email (phone required, normalised to +994…)
// while keeping the citizen's current status and location.
func (s *Service) UpsertProfile(ctx context.Context, id string, in ProfileInput) (domain.Citizen, error) {
	if id == "" {
		return domain.Citizen{}, invalid("invalid_json")
	}
	phone, ok := notify.NormalizePhone(in.Phone)
	if !ok {
		return domain.Citizen{}, invalid("invalid_phone")
	}
	if !notify.ValidEmail(in.Email) {
		return domain.Citizen{}, invalid("invalid_email")
	}
	if in.Role == "" {
		in.Role = domain.RoleStandard
	}
	if !in.Role.Valid() {
		return domain.Citizen{}, invalid("invalid_role")
	}
	c, err := s.store.GetCitizen(ctx, id)
	if err != nil {
		c = domain.Citizen{ID: id, Status: domain.CitizenSafe}
	}
	c.Name = strings.TrimSpace(in.Name)
	if len(c.Name) > 80 {
		c.Name = c.Name[:80]
	}
	c.Phone = phone
	c.Email = strings.TrimSpace(in.Email)
	c.Role = in.Role
	c.UpdatedAt = time.Now().UTC()
	if err := s.store.UpsertCitizen(ctx, c); err != nil {
		return domain.Citizen{}, err
	}
	s.hub.Publish("citizen.updated", c)
	return c, nil
}

func (s *Service) GetCitizen(ctx context.Context, id string) (domain.Citizen, error) {
	return s.store.GetCitizen(ctx, id)
}

// SOSResult is what a citizen gets back: the stored signal, an alert if the
// crisis rule fired, and the SMS that was pushed to their phone (if any).
type SOSResult struct {
	SOS          domain.SOS           `json:"sos"`
	Alert        *domain.Alert        `json:"alert"`
	Notification *domain.Notification `json:"notification"`
	Duplicate    bool                 `json:"duplicate"` // identical recent signal — nothing new was created
}

// SubmitSOS stores the signal, marks the citizen as needing help, runs the
// crisis engine and — when the citizen has a phone — texts them the nearest
// (accessible, for priority citizens) assembly point. A signal from a
// priority-role citizen is automatically escalated to critical priority (#1).
func (s *Service) SubmitSOS(ctx context.Context, in SOSInput) (SOSResult, error) {
	if !in.Type.Valid() {
		return SOSResult{}, invalid("invalid_sos_type")
	}
	var profile domain.Citizen
	if in.CitizenID != "" {
		profile, _ = s.store.GetCitizen(ctx, in.CitizenID)
	}
	if in.Role == "" {
		in.Role = profile.Role
	}
	if in.Role == "" {
		in.Role = domain.RoleStandard
	}
	if !in.Role.Valid() {
		return SOSResult{}, invalid("invalid_role")
	}
	priority := domain.PriorityNormal
	if in.Role == domain.RolePriority {
		priority = domain.PriorityCritical
	}
	var origin *domain.Point
	if in.Lat != nil && in.Lng != nil {
		origin = &domain.Point{Lat: *in.Lat, Lng: *in.Lng}
	}
	o, zid, err := s.resolveOrigin(ctx, origin, in.ZoneID)
	if err != nil {
		return SOSResult{}, err
	}
	if in.CitizenID == "" {
		in.CitizenID = "anon-" + domain.NewID()
	}
	if len(in.Message) > 500 {
		in.Message = in.Message[:500]
	}
	now := time.Now().UTC()
	id := domain.NewID()
	// Duplicate filter: same citizen, or same type at the same spot, inside the window.
	if prevID, dup := s.checkDuplicate(in.CitizenID, in.Type, o, id); dup {
		res := SOSResult{Duplicate: true}
		if prev, err := s.store.GetSOS(ctx, prevID); err == nil {
			res.SOS = prev
		}
		return res, nil
	}
	floodMode := s.noteSOS()
	sos := domain.SOS{
		ID: id, CitizenID: in.CitizenID, Role: in.Role, Priority: priority, Type: in.Type, ZoneID: zid,
		Location: o, Message: in.Message, Status: domain.SOSOpen, CreatedAt: now,
		CitizenName: profile.Name, CitizenPhone: profile.Phone, CitizenEmail: profile.Email,
	}
	if floodMode && profile.Phone == "" {
		// Under a flood only verified (profiled) citizens reach the live queue;
		// anonymous signals are kept for audit but never block the dispatcher.
		sos.Suspect, sos.SuspectReason = true, "flood_anonymous"
		s.flood.mu.Lock()
		s.flood.suspects++
		s.flood.mu.Unlock()
	}
	if err := s.store.CreateSOS(ctx, sos); err != nil {
		return SOSResult{}, err
	}
	if sos.Suspect {
		return SOSResult{SOS: sos}, nil // no SSE fan-out, no crisis engine, no SMS
	}
	profile.ID = in.CitizenID
	profile.Role = in.Role
	profile.Status = domain.CitizenNeedHelp
	profile.Location = &o
	profile.UpdatedAt = now
	_ = s.store.UpsertCitizen(ctx, profile)
	s.hub.Publish("sos.created", sos)
	if priority == domain.PriorityCritical {
		s.log.Warn("PRIORITY SOS", "id", sos.ID, "zone", sos.ZoneID, "type", sos.Type)
		s.hub.Publish("sos.priority", sos)
	}
	res := SOSResult{SOS: sos}

	if sos.CitizenPhone != "" {
		if n, err := s.notifyShelter(ctx, sos, "sos"); err != nil {
			s.log.Warn("sms failed", "err", err)
		} else {
			res.Notification = n
		}
	}

	alert, err := s.engine.OnSOS(ctx, sos)
	if err != nil {
		s.log.Error("crisis engine", "err", err)
		return res, nil
	}
	if alert != nil {
		s.log.Warn("RED ALERT", "kind", alert.Kind, "zone", alert.ZoneID, "signals", alert.SignalCount)
		s.hub.Publish("alert.created", alert)
		res.Alert = alert
	}
	return res, nil
}

// notifyShelter texts the citizen behind an SOS the nearest assembly point
// (accessible-only for priority citizens) with distance, time and a map link.
func (s *Service) notifyShelter(ctx context.Context, sos domain.SOS, reason string) (*domain.Notification, error) {
	if sos.CitizenPhone == "" {
		return nil, conflict("no_phone")
	}
	accessible := sos.Role == domain.RolePriority
	plan, err := s.Plan(ctx, &sos.Location, sos.ZoneID, accessible)
	if err != nil {
		return nil, err
	}
	msg := s.composeShelterSMS(sos, plan, reason)
	n := domain.Notification{
		ID: domain.NewID(), CitizenID: sos.CitizenID, SOSID: sos.ID, Channel: "sms", To: sos.CitizenPhone,
		Message: msg, Reason: reason, Provider: s.notifier.Name(), CreatedAt: time.Now().UTC(),
	}
	pid, err := s.notifier.Send(ctx, sos.CitizenPhone, msg)
	n.ProviderID = pid
	switch {
	case err != nil:
		n.Status = "failed"
		n.Error = err.Error()
	case s.notifier.Simulated():
		n.Status = "simulated"
	default:
		n.Status = "sent"
	}
	if err := s.store.CreateNotification(ctx, n); err != nil {
		return nil, err
	}
	s.hub.Publish("notification.sent", n)
	return &n, nil
}

func (s *Service) composeShelterSMS(sos domain.SOS, plan domain.EvacuationPlan, reason string) string {
	var b strings.Builder
	b.WriteString("FHN 112: ")
	if sos.CitizenName != "" {
		b.WriteString(sos.CitizenName + ", ")
	}
	if reason == "reroute" {
		b.WriteString("marşrut dəyişdi! ")
	}
	if plan.Recommended == nil {
		b.WriteString("hazırda uyğun toplanış məntəqəsi yoxdur, dispeçer sizinlə əlaqə saxlayacaq.")
		return b.String()
	}
	r := plan.Recommended
	b.WriteString("ən yaxın təhlükəsiz məntəqə: " + r.Shelter.Name)
	if r.Route != nil {
		km := r.Route.DistanceM
		if km >= 1000 {
			b.WriteString(fmt.Sprintf(" — %.1f km, ~%d dəq", km/1000, int(math.Ceil(r.Route.DurationS/60))))
		} else {
			b.WriteString(fmt.Sprintf(" — %.0f m, ~%d dəq", km, int(math.Ceil(r.Route.DurationS/60))))
		}
		if r.Route.Accessible {
			b.WriteString(" (♿ panduslu marşrut)")
		}
	}
	b.WriteString(fmt.Sprintf(". Doluluq %.0f%%. Xəritə: %s/?lat=%.5f&lng=%.5f", r.OccupancyPct, s.publicURL, sos.Location.Lat, sos.Location.Lng))
	if plan.Accessible {
		b.WriteString("&role=priority")
	}
	b.WriteString(" . Yol bağlanarsa yeni SMS alacaqsınız.")
	return b.String()
}

// NotifySOS is the dispatcher's one-click "text them the nearest shelter".
func (s *Service) NotifySOS(ctx context.Context, sosID string) (*domain.Notification, error) {
	sos, err := s.store.GetSOS(ctx, sosID)
	if err != nil {
		return nil, err
	}
	return s.notifyShelter(ctx, sos, "manual")
}

// renotifyOpenSOS re-texts everyone with an open SOS and a phone number after
// the road network or shelter capacity changed. Rate-limited per citizen.
func (s *Service) renotifyOpenSOS(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list, err := s.store.ListSOS(ctx, time.Now().UTC().Add(-2*time.Hour))
	if err != nil {
		return
	}
	for _, sos := range list {
		if sos.Status == domain.SOSResolved || sos.CitizenPhone == "" {
			continue
		}
		s.notifyMu.Lock()
		last, seen := s.lastSMS[sos.CitizenID]
		if seen && time.Since(last) < 2*time.Minute {
			s.notifyMu.Unlock()
			continue
		}
		s.lastSMS[sos.CitizenID] = time.Now()
		s.notifyMu.Unlock()
		if _, err := s.notifyShelter(ctx, sos, reason); err != nil {
			s.log.Warn("reroute sms failed", "citizen", sos.CitizenID, "err", err)
		}
	}
}

func (s *Service) ListNotifications(ctx context.Context, limit int) ([]domain.Notification, error) {
	list, err := s.store.ListNotifications(ctx, limit)
	if list == nil {
		list = []domain.Notification{}
	}
	return list, err
}

func (s *Service) UpdateCitizenStatus(ctx context.Context, id string, status domain.CitizenStatus, role domain.CitizenRole, loc *domain.Point) (domain.Citizen, error) {
	if !status.Valid() {
		return domain.Citizen{}, invalid("invalid_status")
	}
	if id == "" {
		return domain.Citizen{}, invalid("invalid_json")
	}
	if role == "" {
		role = domain.RoleStandard
	}
	if !role.Valid() {
		return domain.Citizen{}, invalid("invalid_role")
	}
	c := domain.Citizen{ID: id, Role: role, Status: status, Location: loc, UpdatedAt: time.Now().UTC()}
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
		go s.renotifyOpenSOS("reroute")
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
	if status == domain.RoadBlocked {
		go s.renotifyOpenSOS("reroute")
	}
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
		// Attach the brigade to the most urgent open SOS in the zone: the store
		// returns critical-priority (#1) signals first, then newest first.
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
	CriticalSOS     int     `json:"critical_sos"`
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
	Citizens      map[string]int        `json:"citizens"`
	Notifications []domain.Notification `json:"notifications"`
	Notify        map[string]any        `json:"notify"`
	Security      map[string]any        `json:"security"`
	SuspectSOS    int                   `json:"suspect_sos"` // hidden from the queue, counted here
	Routing       routing.Status        `json:"routing"`
	Store         string                `json:"store"`
	Crisis        crisis.Config         `json:"crisis"`
	Thresholds    map[string]any        `json:"thresholds"`
}

// Overview is the single payload behind the live dispatcher panel.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	now := time.Now().UTC()
	ov := Overview{GeneratedAt: now, Store: s.store.Name(), Routing: s.RoutingStatus(), Crisis: s.engine.Config(),
		Citizens: map[string]int{}, Notify: s.NotifyStatus(), Thresholds: map[string]any{"shelter_warn_pct": s.cfg.ShelterWarnPct}}
	var err error
	if ov.Notifications, err = s.ListNotifications(ctx, 30); err != nil {
		return ov, err
	}
	if ov.Shelters, err = s.store.ListShelters(ctx); err != nil {
		return ov, err
	}
	all, err := s.store.ListSOS(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return ov, err
	}
	ov.SOS = make([]domain.SOS, 0, len(all))
	for _, v := range all {
		if v.Suspect {
			ov.SuspectSOS++
			continue // flood traffic never clutters the live queue
		}
		ov.SOS = append(ov.SOS, v)
	}
	if len(ov.SOS) > 300 {
		ov.SOS = ov.SOS[:300]
	}
	ov.Security = s.SecurityStatus()
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
				if v.Priority == domain.PriorityCritical {
					st.CriticalSOS++
				}
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
