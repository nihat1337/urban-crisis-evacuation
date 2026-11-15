// Package domain holds the core types shared by every layer of the system.
// It has no dependencies on storage, HTTP or routing so that any of those can
// be swapped without touching the model.
package domain

import (
	"math"
	"time"
)

// Point is a WGS84 coordinate.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

const earthRadiusM = 6371000.0

// DistanceM returns the great-circle (haversine) distance between two points in metres.
func DistanceM(a, b Point) float64 {
	const toRad = math.Pi / 180
	dLat := (b.Lat - a.Lat) * toRad
	dLng := (b.Lng - a.Lng) * toRad
	la1 := a.Lat * toRad
	la2 := b.Lat * toRad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// Zone is an administrative district of the city.
type Zone struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Center  Point   `json:"center"`
	RadiusM float64 `json:"radius_m"`
}

type ShelterType string

const (
	ShelterBunker    ShelterType = "shelter"
	ShelterPark      ShelterType = "park"
	ShelterStadium   ShelterType = "stadium"
	ShelterTemporary ShelterType = "temporary"
)

// Shelter is a safe assembly point (sığınacaq / park / stadion).
type Shelter struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Type      ShelterType `json:"type"`
	ZoneID    string      `json:"zone_id"`
	Location  Point       `json:"location"`
	Capacity  int         `json:"capacity"`
	Occupancy int         `json:"occupancy"`
	Active    bool        `json:"active"`
	// Accessible: reachable by wheelchair / medical transport (ramps, no stairs).
	Accessible bool `json:"accessible"`
}

// OccupancyPct returns occupancy as a percentage rounded to one decimal.
func (s Shelter) OccupancyPct() float64 {
	if s.Capacity <= 0 {
		return 100
	}
	return math.Round(float64(s.Occupancy)/float64(s.Capacity)*1000) / 10
}

type SOSType string

const (
	SOSFire     SOSType = "fire"
	SOSCollapse SOSType = "collapse"
	SOSFlood    SOSType = "flood"
	SOSMedical  SOSType = "medical"
)

func (t SOSType) Valid() bool {
	switch t {
	case SOSFire, SOSCollapse, SOSFlood, SOSMedical:
		return true
	}
	return false
}

type SOSStatus string

const (
	SOSOpen     SOSStatus = "open"
	SOSAssigned SOSStatus = "assigned"
	SOSResolved SOSStatus = "resolved"
)

// CitizenRole distinguishes citizens who need special care (hərəkət
// məhdudiyyəti / xüsusi qayğı). Their SOS is critical priority and their
// evacuation routes must be barrier-free.
type CitizenRole string

const (
	RoleStandard CitizenRole = "standard"
	RolePriority CitizenRole = "priority"
)

func (r CitizenRole) Valid() bool { return r == RoleStandard || r == RolePriority }

// SOS priority: 1 = critical (priority role), 2 = normal.
const (
	PriorityCritical = 1
	PriorityNormal   = 2
)

// SOS is a single distress signal sent by a citizen.
type SOS struct {
	ID        string      `json:"id"`
	CitizenID string      `json:"citizen_id"`
	Role      CitizenRole `json:"role"`
	Priority  int         `json:"priority"`
	Type      SOSType     `json:"type"`
	ZoneID    string    `json:"zone_id"`
	Location  Point     `json:"location"`
	Message   string    `json:"message,omitempty"`
	Status    SOSStatus `json:"status"`
	BrigadeID string    `json:"brigade_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Contact snapshot from the citizen profile at the time of the signal.
	CitizenName  string `json:"citizen_name,omitempty"`
	CitizenPhone string `json:"citizen_phone,omitempty"`
	CitizenEmail string `json:"citizen_email,omitempty"`
}

type CitizenStatus string

const (
	CitizenSafe       CitizenStatus = "safe"
	CitizenEvacuating CitizenStatus = "evacuating"
	CitizenNeedHelp   CitizenStatus = "need_help"
)

func (s CitizenStatus) Valid() bool {
	switch s {
	case CitizenSafe, CitizenEvacuating, CitizenNeedHelp:
		return true
	}
	return false
}

// Citizen tracks the self-reported safety status of one device/user.
type Citizen struct {
	ID        string        `json:"id"`
	Role      CitizenRole   `json:"role"`
	Status    CitizenStatus `json:"status"`
	Location  *Point        `json:"location,omitempty"`
	UpdatedAt time.Time     `json:"updated_at"`
	// Optional profile: lets dispatchers call back and lets the system push
	// SMS guidance that works even when mobile data is down. No password —
	// an emergency app must never put a login between a person and SOS.
	Name  string `json:"name,omitempty"`
	Phone string `json:"phone,omitempty"`
	Email string `json:"email,omitempty"`
}

// Notification is an outbound message (SMS) sent to a citizen.
type Notification struct {
	ID         string    `json:"id"`
	CitizenID  string    `json:"citizen_id"`
	SOSID      string    `json:"sos_id,omitempty"`
	Channel    string    `json:"channel"` // sms
	To         string    `json:"to"`
	Message    string    `json:"message"`
	Reason     string    `json:"reason"` // sos | reroute | manual
	Provider   string    `json:"provider"`
	ProviderID string    `json:"provider_id,omitempty"`
	Status     string    `json:"status"` // sent | simulated | failed
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type BrigadeStatus string

const (
	BrigadeAvailable BrigadeStatus = "available"
	BrigadeEnRoute   BrigadeStatus = "en_route"
	BrigadeBusy      BrigadeStatus = "busy"
)

func (s BrigadeStatus) Valid() bool {
	switch s {
	case BrigadeAvailable, BrigadeEnRoute, BrigadeBusy:
		return true
	}
	return false
}

// Brigade is a rescue team.
type Brigade struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Status        BrigadeStatus `json:"status"`
	Location      Point         `json:"location"`
	ZoneID        string        `json:"zone_id"`
	AssignedSOSID string        `json:"assigned_sos_id,omitempty"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type RoadStatus string

const (
	RoadOpen      RoadStatus = "open"
	RoadCongested RoadStatus = "congested"
	RoadBlocked   RoadStatus = "blocked"
)

func (s RoadStatus) Valid() bool {
	switch s {
	case RoadOpen, RoadCongested, RoadBlocked:
		return true
	}
	return false
}

// Road is one segment of the (simulated) road network.
type Road struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	From      Point      `json:"from"`
	To        Point      `json:"to"`
	Status    RoadStatus `json:"status"`
	// Accessible: has ramps / no stairs or barriers — usable by wheelchairs and medical transport.
	Accessible bool      `json:"accessible"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type AlertLevel string

const (
	AlertRed    AlertLevel = "red"
	AlertYellow AlertLevel = "yellow"
)

type AlertKind string

const (
	AlertSOSCluster  AlertKind = "sos_cluster"
	AlertShelterFull AlertKind = "shelter_full"
)

type AlertAction string

const (
	ActionDispatchBrigade AlertAction = "dispatch_brigade"
	ActionOpenShelter     AlertAction = "open_shelter"
)

func (a AlertAction) Valid() bool {
	return a == ActionDispatchBrigade || a == ActionOpenShelter
}

// Alert is produced by the crisis engine for the dispatcher panel.
type Alert struct {
	ID               string        `json:"id"`
	Level            AlertLevel    `json:"level"`
	Kind             AlertKind     `json:"kind"`
	ZoneID           string        `json:"zone_id"`
	ShelterID        string        `json:"shelter_id,omitempty"`
	Center           Point         `json:"center"`
	Message          string        `json:"message"`
	SignalCount      int           `json:"signal_count,omitempty"`
	SuggestedActions []AlertAction `json:"suggested_actions"`
	Executed         bool          `json:"executed"`
	ExecutedAction   AlertAction   `json:"executed_action,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	ExecutedAt       *time.Time    `json:"executed_at,omitempty"`
}

// Route is a computed path between two points.
type Route struct {
	Source    string  `json:"source"` // "osrm" | "simulated" | "straight"
	DistanceM float64 `json:"distance_m"`
	DurationS float64 `json:"duration_s"`
	Geometry  []Point `json:"geometry"`
	Detour    bool    `json:"detour"`     // true when a blocked road forced an alternative
	Accessible bool   `json:"accessible"` // true when computed over barrier-free roads only
	Notes     string  `json:"notes,omitempty"`
	// Offline is true when the external routing API was unavailable (HTTP 5xx,
	// timeout, breaker open) and the route came from the local backup: the
	// last cached route ("cache") or the local road graph ("simulated").
	Offline       bool       `json:"offline"`
	OfflineReason string     `json:"offline_reason,omitempty"`
	CachedAt      *time.Time `json:"cached_at,omitempty"`
}

// ShelterOption is a candidate shelter together with its route.
type ShelterOption struct {
	Shelter      Shelter `json:"shelter"`
	DistanceM    float64 `json:"distance_m"`
	OccupancyPct float64 `json:"occupancy_pct"`
	Recommended  bool    `json:"recommended"`
	Route        *Route  `json:"route,omitempty"`
}

// EvacuationPlan is what a citizen receives: a recommended shelter plus alternatives.
type EvacuationPlan struct {
	Origin       Point           `json:"origin"`
	ZoneID       string          `json:"zone_id"`
	Accessible   bool            `json:"accessible"` // plan restricted to accessible shelters & roads
	Mode         string          `json:"mode"`       // online | offline (backup routing in use)
	OfflineReason string         `json:"offline_reason,omitempty"`
	Recommended  *ShelterOption  `json:"recommended"`
	Alternatives []ShelterOption `json:"alternatives"`
	Warnings     []string        `json:"warnings,omitempty"`
	GeneratedAt  time.Time       `json:"generated_at"`
}
