// Package zones resolves which city districts currently need dispatcher
// attention. It is pure: incidents and district geometry come from the caller.
package zones

import (
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	// SOSClusterMin is the minimum number of distinct senders for a cluster.
	SOSClusterMin = 3
	// SOSClusterWindow is how far back a cluster is counted.
	SOSClusterWindow = 15 * time.Minute
	// NearbyKM is the radius that makes a neighbouring district "nearby".
	NearbyKM = 3.0
)

type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type District struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Center LatLng `json:"center"`
}

type Incident struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // sos | alert | road | hazard
	Reason     string    `json:"reason,omitempty"`
	DistrictID string    `json:"district_id,omitempty"`
	Location   *LatLng   `json:"location,omitempty"`
	Count      int       `json:"count,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type ActiveZone struct {
	DistrictID     string     `json:"district_id"`
	Name           string     `json:"name"`
	Status         string     `json:"status"` // affected | nearby
	IncidentCount  int        `json:"incident_count"`
	LastIncidentAt *time.Time `json:"last_incident_at,omitempty"`
	DistanceKm     float64    `json:"distance_km"`
	Reason         string     `json:"reason"`
}

// ResolveActiveZones turns raw incidents into the sorted dropdown payload.
func ResolveActiveZones(incidents []Incident, districts []District, now time.Time) []ActiveZone {
	type acc struct {
		count  int
		last   time.Time
		reason string
	}
	affected := map[string]*acc{}
	for _, in := range incidents {
		id := in.DistrictID
		if in.Location != nil {
			id = nearestDistrict(in.Location, districts)
		}
		if id == "" {
			continue
		}
		a := affected[id]
		if a == nil {
			a = &acc{}
			affected[id] = a
		}
		n := in.Count
		if n < 1 {
			n = 1
		}
		a.count += n
		if in.CreatedAt.After(a.last) {
			a.last = in.CreatedAt
		}
		if a.reason == "" {
			if in.Kind == "sos" {
				mins := int(now.Sub(in.CreatedAt).Minutes())
				if mins < 0 {
					mins = 0
				}
				a.reason = fmt.Sprintf("%d SOS / %d dəq", a.count, mins)
			} else if in.Reason != "" {
				a.reason = in.Reason
			} else {
				a.reason = "Hadisə xəbərdarlığı"
			}
		}
	}
	var out []ActiveZone
	for id, a := range affected {
		d := districtByID(id, districts)
		if d == nil {
			continue
		}
		last := a.last
		out = append(out, ActiveZone{DistrictID: id, Name: d.Name, Status: "affected",
			IncidentCount: a.count, LastIncidentAt: &last, DistanceKm: 0, Reason: a.reason})
	}
	affectedIDs := map[string]bool{}
	for id := range affected {
		affectedIDs[id] = true
	}
	for i := range districts {
		d := &districts[i]
		if affectedIDs[d.ID] {
			continue
		}
		best := math.MaxFloat64
		var count int
		var last time.Time
		for _, in := range incidents {
			if in.Location == nil {
				continue
			}
			km := haversine(in.Location.Lat, in.Location.Lng, d.Center.Lat, d.Center.Lng) / 1000
			if km < best {
				best = km
			}
			if km <= NearbyKM {
				n := in.Count
				if n < 1 {
					n = 1
				}
				count += n
				if in.CreatedAt.After(last) {
					last = in.CreatedAt
				}
			}
		}
		if best <= NearbyKM {
			lastp := last
			out = append(out, ActiveZone{DistrictID: d.ID, Name: d.Name, Status: "nearby",
				IncidentCount: count, LastIncidentAt: &lastp, DistanceKm: round1(best),
				Reason: fmt.Sprintf("%.1f km", best)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Status != b.Status {
			return a.Status == "affected"
		}
		if a.Status == "affected" {
			if a.IncidentCount != b.IncidentCount {
				return a.IncidentCount > b.IncidentCount
			}
			if a.LastIncidentAt != nil && b.LastIncidentAt != nil && !a.LastIncidentAt.Equal(*b.LastIncidentAt) {
				return a.LastIncidentAt.After(*b.LastIncidentAt)
			}
			return a.Name < b.Name
		}
		if a.DistanceKm != b.DistanceKm {
			return a.DistanceKm < b.DistanceKm
		}
		return a.Name < b.Name
	})
	return out
}

func nearestDistrict(p *LatLng, districts []District) string {
	bestID := ""
	best := math.MaxFloat64
	for _, d := range districts {
		if km := haversine(p.Lat, p.Lng, d.Center.Lat, d.Center.Lng); km < best {
			best, bestID = km, d.ID
		}
	}
	return bestID
}

func districtByID(id string, districts []District) *District {
	for i := range districts {
		if districts[i].ID == id {
			return &districts[i]
		}
	}
	return nil
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func haversine(lat1, lng1, lat2, lng2 float64) float64 {
	const rad = math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * 6371000 * math.Asin(math.Min(1, math.Sqrt(a)))
}
