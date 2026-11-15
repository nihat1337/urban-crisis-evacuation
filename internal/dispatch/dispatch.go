// Package dispatch implements the dispatcher's SMS evacuation module: active
// districts, the preview plan, and confirmed sending through the existing SMS
// sender (wrapped by the caller in a tiny adapter).
package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
	"github.com/nihat1337/urban-crisis-evacuation/internal/events"
	"github.com/nihat1337/urban-crisis-evacuation/internal/plan"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store"
	"github.com/nihat1337/urban-crisis-evacuation/internal/zones"
)

// SMSSender is the only SMS dependency of this module.
type SMSSender interface {
	Send(ctx context.Context, to, text string) error
}

// SMSSenderFunc lets main pass the existing service.SendSMS method directly.
type SMSSenderFunc func(ctx context.Context, to, text string) error

func (f SMSSenderFunc) Send(ctx context.Context, to, text string) error { return f(ctx, to, text) }

type Config struct {
	PublicURL string
	DemoMode  bool
	Workers   int
}

type Service struct {
	cfg   Config
	store store.Store
	sms   SMSSender
	hub   *events.Hub
	log   *slog.Logger

	mu        sync.Mutex
	plans     map[string]*Plan
	reserved  map[string]int
	incidents []zones.Incident
}

func New(cfg Config, st store.Store, sms SMSSender, hub *events.Hub, log *slog.Logger) *Service {
	if cfg.Workers <= 0 {
		cfg.Workers = 20
	}
	return &Service{cfg: cfg, store: st, sms: sms, hub: hub, log: log,
		plans: map[string]*Plan{}, reserved: map[string]int{}}
}

type PlanPoint struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Quota    int     `json:"quota"`
	Assigned int     `json:"assigned"`
	AvgDistM float64 `json:"avg_distance_m"`
}

type Plan struct {
	ID              string            `json:"id"`
	ZoneID          string            `json:"zone_id"`
	ZoneName        string            `json:"zone_name"`
	TotalRecipients int               `json:"total_recipients"`
	Points          []PlanPoint       `json:"points"`
	Unassigned      int               `json:"unassigned"`
	NoLocation      int               `json:"no_location"`
	NeedsNewPoint   bool              `json:"needs_new_point"`
	Status          string            `json:"status"`
	CreatedAt       time.Time         `json:"created_at"`
	Assignments     []plan.Assignment `json:"-"`
	Fallbacks       []string          `json:"-"`
}

type SendResult struct {
	Sent   int `json:"sent"`
	Failed int `json:"failed"`
}

var ErrNotFound = fmt.Errorf("plan not found")
var ErrAlready = fmt.Errorf("plan already confirmed")
var ErrQuota = fmt.Errorf("point quota changed")

func (s *Service) DistrictList(ctx context.Context) ([]zones.District, error) {
	zs, err := s.store.ListZones(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]zones.District, 0, len(zs))
	for _, z := range zs {
		out = append(out, zones.District{ID: z.ID, Name: z.Name, Center: zones.LatLng{Lat: z.Center.Lat, Lng: z.Center.Lng}})
	}
	return out, nil
}

// ActiveZones combines active alerts, SOS clusters and dev-injected incidents.
func (s *Service) ActiveZones(ctx context.Context) ([]zones.ActiveZone, error) {
	districts, err := s.DistrictList(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var incidents []zones.Incident
	if alerts, err := s.store.ListAlerts(ctx); err == nil {
		for _, a := range alerts {
			if a.Executed {
				continue
			}
			reason := "Hadisə xəbərdarlığı"
			if a.Kind == domain.AlertSOSCluster {
				reason = "SOS klasteri"
			} else if a.Kind == domain.AlertShelterFull {
				reason = "Toplanış məntəqəsi dolub"
			}
			incidents = append(incidents, zones.Incident{ID: a.ID, Kind: "alert", Reason: reason,
				DistrictID: a.ZoneID, Location: &zones.LatLng{Lat: a.Center.Lat, Lng: a.Center.Lng}, CreatedAt: a.CreatedAt})
		}
	}
	if list, err := s.store.ListSOS(ctx, now.Add(-zones.SOSClusterWindow)); err == nil {
		type cl struct {
			senders map[string]bool
			last    time.Time
			loc     zones.LatLng
			zone    string
		}
		clusters := map[string]*cl{}
		for _, v := range list {
			if v.Suspect || v.Status == domain.SOSResolved {
				continue
			}
			c := clusters[v.ZoneID]
			if c == nil {
				c = &cl{senders: map[string]bool{}}
				clusters[v.ZoneID] = c
			}
			c.senders[v.CitizenID] = true
			if v.CreatedAt.After(c.last) {
				c.last = v.CreatedAt
				c.loc = zones.LatLng{Lat: v.Location.Lat, Lng: v.Location.Lng}
			}
			c.zone = v.ZoneID
		}
		for zid, c := range clusters {
			if len(c.senders) >= zones.SOSClusterMin {
				incidents = append(incidents, zones.Incident{ID: "sos-" + zid, Kind: "sos", DistrictID: zid,
					Location: &c.loc, Count: len(c.senders), CreatedAt: c.last})
			}
		}
	}
	s.mu.Lock()
	incidents = append(incidents, s.incidents...)
	s.mu.Unlock()
	return zones.ResolveActiveZones(incidents, districts, now), nil
}

// InjectIncident is the dev helper behind POST /dev/incidents.
func (s *Service) InjectIncident(ctx context.Context, district string, count int) ([]zones.ActiveZone, error) {
	if count <= 0 {
		count = 3
	}
	districts, err := s.DistrictList(ctx)
	if err != nil {
		return nil, err
	}
	d := findDistrict(districts, district)
	if d == nil {
		return nil, fmt.Errorf("unknown district %q", district)
	}
	s.mu.Lock()
	s.incidents = append(s.incidents, zones.Incident{ID: fmt.Sprintf("dev-%d", time.Now().UnixNano()),
		Kind: "sos", DistrictID: d.ID, Location: &zones.LatLng{Lat: d.Center.Lat, Lng: d.Center.Lng}, Count: count, CreatedAt: time.Now().UTC()})
	s.mu.Unlock()
	return s.ActiveZones(ctx)
}

// Seed creates demo citizens and six open points around a district.
func (s *Service) Seed(ctx context.Context, district string, citizens int) error {
	if citizens <= 0 {
		citizens = 250
	}
	districts, err := s.DistrictList(ctx)
	if err != nil {
		return err
	}
	d := findDistrict(districts, district)
	if d == nil {
		return fmt.Errorf("unknown district %q", district)
	}
	names := []string{"Park A", "Məktəb №12", "Stadion", "Məktəb №7", "İdman zalı", "Mərkəzi Meydan"}
	for i, name := range names {
		angle := float64(i) * math.Pi / 3
		sh := domain.Shelter{ID: fmt.Sprintf("sim-%s-%d", d.ID, i+1), Name: name, Type: domain.ShelterPark,
			ZoneID: d.ID, Location: domain.Point{Lat: d.Center.Lat + 0.004*math.Cos(angle), Lng: d.Center.Lng + 0.004*math.Sin(angle)},
			Capacity: 120, Occupancy: 0, Active: true, Accessible: true}
		if err := s.store.UpsertShelter(ctx, sh); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	for i := 0; i < citizens; i++ {
		angle := float64(i) * 0.61803398875 * 2 * math.Pi
		r := 0.009 * math.Sqrt(float64(i)/float64(citizens)) // ~2 km across
		loc := domain.Point{Lat: d.Center.Lat + r*math.Cos(angle), Lng: d.Center.Lng + r*math.Sin(angle)}
		phone := fmt.Sprintf("+99450%07d", 1000000+i)
		c := domain.Citizen{ID: fmt.Sprintf("sim-%s-%d", d.ID, i+1), Role: domain.RoleStandard, Status: domain.CitizenSafe,
			Location: &loc, UpdatedAt: now, Phone: phone, Name: fmt.Sprintf("Demo %d", i+1), ZoneID: d.ID}
		if err := s.store.UpsertCitizen(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// Preview builds and stores a pending plan. Nothing is sent.
func (s *Service) Preview(ctx context.Context, zoneID string, disaster bool) (*Plan, error) {
	districts, err := s.DistrictList(ctx)
	if err != nil {
		return nil, err
	}
	d := findDistrict(districts, zoneID)
	if d == nil {
		return nil, fmt.Errorf("unknown district %q", zoneID)
	}
	citizens, err := s.store.ListCitizens(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.store.ListShelters(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	reserved := make(map[string]int, len(s.reserved))
	for k, v := range s.reserved {
		reserved[k] = v
	}
	s.mu.Unlock()

	var people []plan.Person
	for _, c := range citizens {
		if c.Phone == "" || c.Role == domain.RolePriority {
			continue // mobility-limited people have their own pipeline
		}
		if c.Location == nil {
			if c.ZoneID == d.ID {
				people = append(people, plan.Person{Phone: c.Phone, HasLoc: false, ZoneID: c.ZoneID, Citizen: c.ID})
			}
			continue
		}
		if nearestZone(c.Location, districts) != d.ID {
			continue
		}
		people = append(people, plan.Person{Phone: c.Phone, Lat: c.Location.Lat, Lng: c.Location.Lng, HasLoc: true, ZoneID: d.ID, Citizen: c.ID})
	}
	points := make([]plan.Point, 0, len(all))
	for _, sh := range all {
		points = append(points, plan.Point{ID: sh.ID, Name: sh.Name, Lat: sh.Location.Lat, Lng: sh.Location.Lng,
			Capacity: sh.Capacity, Occupancy: sh.Occupancy + reserved[sh.ID], Open: sh.Active})
	}
	maxDist := 1500.0
	if disaster {
		maxDist = 3000
	}
	res := plan.Build(people, points, maxDist)
	if !disaster && len(res.Assignments) == 0 && len(people) > 0 {
		res = plan.Build(people, points, 3000) // widen automatically if nothing is reachable normally
	}
	p := &Plan{ID: domain.NewID(), ZoneID: d.ID, ZoneName: d.Name, Status: "pending", CreatedAt: time.Now().UTC(),
		Assignments: res.Assignments, TotalRecipients: len(res.Assignments), Unassigned: len(res.Unassigned), NoLocation: len(res.NoLocation)}
	p.NeedsNewPoint = p.Unassigned > 0
	for _, st := range res.Points {
		p.Points = append(p.Points, PlanPoint{ID: st.ID, Name: st.Name, Quota: st.Quota, Assigned: st.Assigned, AvgDistM: st.AvgDistM})
	}
	for _, x := range res.NoLocation {
		p.Fallbacks = append(p.Fallbacks, x.Phone)
	}
	for _, x := range res.Unassigned {
		p.Fallbacks = append(p.Fallbacks, x.Phone)
	}
	s.mu.Lock()
	s.plans[p.ID] = p
	s.mu.Unlock()
	return p, nil
}

// Confirm re-checks quota, reserves the seats and sends through the adapter.
// The plan is marked confirmed before any I/O, so a second call is a no-op.
func (s *Service) Confirm(ctx context.Context, id string) (SendResult, error) {
	s.mu.Lock()
	p := s.plans[id]
	if p == nil {
		s.mu.Unlock()
		return SendResult{}, ErrNotFound
	}
	if p.Status == "confirmed" {
		s.mu.Unlock()
		return SendResult{}, ErrAlready
	}
	shelters, err := s.store.ListShelters(ctx)
	if err != nil {
		s.mu.Unlock()
		return SendResult{}, err
	}
	byID := map[string]domain.Shelter{}
	for _, sh := range shelters {
		byID[sh.ID] = sh
	}
	perPoint := map[string]int{}
	for _, a := range p.Assignments {
		perPoint[a.PointID]++
	}
	for pid, n := range perPoint {
		sh, ok := byID[pid]
		if !ok {
			s.mu.Unlock()
			return SendResult{}, ErrQuota
		}
		free := int(math.Floor(plan.CapacityRatio*float64(sh.Capacity))) - sh.Occupancy - s.reserved[pid]
		if n > free {
			s.mu.Unlock()
			return SendResult{}, ErrQuota
		}
	}
	for pid, n := range perPoint {
		s.reserved[pid] += n
	}
	p.Status = "confirmed"
	s.mu.Unlock()

	var mu sync.Mutex
	var wg sync.WaitGroup
	res := SendResult{}
	sem := make(chan struct{}, s.cfg.Workers)
	send := func(to, text string) {
		defer wg.Done()
		defer func() { <-sem }()
		if err := s.sms.Send(ctx, to, text); err != nil {
			mu.Lock()
			res.Failed++
			mu.Unlock()
			s.log.Warn("dispatch sms failed", "to", to, "err", err)
			return
		}
		mu.Lock()
		res.Sent++
		mu.Unlock()
	}
	for _, a := range p.Assignments {
		wg.Add(1)
		sem <- struct{}{}
		go send(a.Phone, s.message(a.PointName, a.DistM, a.Bearing))
	}
	for _, phone := range p.Fallbacks {
		wg.Add(1)
		sem <- struct{}{}
		go send(phone, s.fallback())
	}
	wg.Wait()
	s.log.Info("dispatch plan confirmed", "plan", p.ID, "zone", p.ZoneID, "sent", res.Sent, "failed", res.Failed)
	return res, nil
}

func (s *Service) message(point string, distM float64, bearing string) string {
	d := int(math.Round(distM/10) * 10)
	text := fmt.Sprintf("TƏHLÜKƏ. Məntəqə: %s, %d m %s. Xəritə: %s", point, d, bearing, s.link())
	return s.demo(text)
}

func (s *Service) fallback() string {
	return s.demo("TƏHLÜKƏ. Ən yaxın məntəqəni xəritədə görün: " + s.link())
}

func (s *Service) demo(text string) string {
	if s.cfg.DemoMode {
		return "[DEMO/TEST] " + text
	}
	return text
}

func (s *Service) link() string {
	if s.cfg.PublicURL == "" {
		return "http://localhost:8080"
	}
	return strings.TrimRight(s.cfg.PublicURL, "/")
}

func findDistrict(districts []zones.District, q string) *zones.District {
	q = strings.TrimSpace(strings.ToLower(q))
	for i := range districts {
		if strings.ToLower(districts[i].ID) == q || strings.ToLower(districts[i].Name) == q {
			return &districts[i]
		}
	}
	return nil
}

func nearestZone(p *domain.Point, districts []zones.District) string {
	best, bestID := math.MaxFloat64, ""
	for _, d := range districts {
		dx := (p.Lat - d.Center.Lat) * 111320
		dy := (p.Lng - d.Center.Lng) * 111320 * math.Cos(p.Lat*math.Pi/180)
		if dd := dx*dx + dy*dy; dd < best {
			best, bestID = dd, d.ID
		}
	}
	return bestID
}

// ZonesFromStore is a lightweight helper used by the API/test layers.
func (s *Service) ZoneName(ctx context.Context, id string) string {
	ds, err := s.DistrictList(ctx)
	if err != nil {
		return id
	}
	if d := findDistrict(ds, id); d != nil {
		return d.Name
	}
	return id
}
