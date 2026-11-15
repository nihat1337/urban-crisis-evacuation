// Package plan contains the pure SMS evacuation planner. It performs no I/O:
// callers pass people and assembly points in, and receive assignments out.
package plan

import (
	"math"
	"sort"
)

const (
	// GroupSize is the maximum number of people gathered at one point.
	GroupSize = 100
	// CapacityRatio caps use at 90% of the real capacity.
	CapacityRatio = 0.9
)

type Person struct {
	Phone   string  `json:"phone"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	HasLoc  bool    `json:"has_loc"`
	ZoneID  string  `json:"zone_id,omitempty"`
	Citizen string  `json:"citizen_id,omitempty"`
}

type Point struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Capacity  int     `json:"capacity"`
	Occupancy int     `json:"occupancy"`
	Open      bool    `json:"open"`
}

// Quota is the number of people this point may still take.
func (p Point) Quota() int {
	q := int(math.Floor(CapacityRatio*float64(p.Capacity))) - p.Occupancy
	if q > GroupSize {
		q = GroupSize
	}
	if q < 0 {
		q = 0
	}
	return q
}

type Assignment struct {
	Phone     string  `json:"phone"`
	PointID   string  `json:"point_id"`
	PointName string  `json:"point_name"`
	DistM     float64 `json:"distance_m"`
	Bearing   string  `json:"bearing"`
}

type PointStat struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Quota    int     `json:"quota"`
	Assigned int     `json:"assigned"`
	AvgDistM float64 `json:"avg_distance_m"`
}

type Result struct {
	Assignments []Assignment `json:"assignments"`
	Unassigned  []Person     `json:"unassigned"`
	NoLocation  []Person     `json:"no_location"`
	Points      []PointStat  `json:"points"`
}

// Build groups located people around one assembly point at a time. Points are
// chosen nearest to the centroid of the remaining people, never to each person.
func Build(people []Person, points []Point, maxDistM float64) Result {
	res := Result{}
	located := make([]Person, 0, len(people))
	for _, p := range people {
		if p.HasLoc {
			located = append(located, p)
		} else {
			res.NoLocation = append(res.NoLocation, p)
		}
	}
	// Stable, deterministic order: ties are always resolved by phone.
	sort.SliceStable(located, func(i, j int) bool { return located[i].Phone < located[j].Phone })

	remaining := located
	used := make(map[string]bool, len(points))
	stats := make(map[string]*PointStat, len(points))
	for _, pt := range points {
		q := pt.Quota()
		stats[pt.ID] = &PointStat{ID: pt.ID, Name: pt.Name, Quota: q}
	}

	for len(remaining) > 0 {
		var centroidLat, centroidLng float64
		for _, p := range remaining {
			centroidLat += p.Lat
			centroidLng += p.Lng
		}
		centroidLat /= float64(len(remaining))
		centroidLng /= float64(len(remaining))

		var chosen *Point
		best := math.MaxFloat64
		for i := range points {
			pt := &points[i]
			if used[pt.ID] || !pt.Open || stats[pt.ID].Quota <= 0 {
				continue
			}
			d := haversine(centroidLat, centroidLng, pt.Lat, pt.Lng)
			if d < best-1e-9 || (math.Abs(d-best) < 1e-9 && (chosen == nil || pt.ID < chosen.ID)) {
				best, chosen = d, pt
			}
		}
		if chosen == nil {
			break
		}
		used[chosen.ID] = true // mark even if nobody can be assigned: guarantees progress
		st := stats[chosen.ID]

		ordered := append([]Person(nil), remaining...)
		sort.SliceStable(ordered, func(i, j int) bool {
			di := haversine(ordered[i].Lat, ordered[i].Lng, chosen.Lat, chosen.Lng)
			dj := haversine(ordered[j].Lat, ordered[j].Lng, chosen.Lat, chosen.Lng)
			if math.Abs(di-dj) > 1e-9 {
				return di < dj
			}
			return ordered[i].Phone < ordered[j].Phone
		})

		assigned := make(map[string]bool)
		var distSum float64
		for _, p := range ordered {
			if st.Assigned >= st.Quota {
				break
			}
			d := haversine(p.Lat, p.Lng, chosen.Lat, chosen.Lng)
			if d > maxDistM {
				break
			}
			res.Assignments = append(res.Assignments, Assignment{
				Phone: p.Phone, PointID: chosen.ID, PointName: chosen.Name,
				DistM: d, Bearing: Bearing(p.Lat, p.Lng, chosen.Lat, chosen.Lng),
			})
			assigned[p.Phone] = true
			st.Assigned++
			distSum += d
		}
		if st.Assigned > 0 {
			st.AvgDistM = distSum / float64(st.Assigned)
		}
		if len(assigned) == 0 {
			continue
		}
		next := remaining[:0]
		for _, p := range remaining {
			if !assigned[p.Phone] {
				next = append(next, p)
			}
		}
		remaining = next
	}

	res.Unassigned = append([]Person(nil), remaining...)
	for _, pt := range points {
		if st := stats[pt.ID]; st.Assigned > 0 {
			res.Points = append(res.Points, *st)
		}
	}
	// Used points in assignment order are already reflected in the map; the
	// UI mainly needs the non-empty rows, sorted by point name for stability.
	sort.Slice(res.Points, func(i, j int) bool { return res.Points[i].Name < res.Points[j].Name })
	return res
}

const earthRadiusM = 6371000.0

func haversine(lat1, lng1, lat2, lng2 float64) float64 {
	const rad = math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(a)))
}

var bearings = []string{"şimal", "şimal-şərq", "şərq", "cənub-şərq", "cənub", "cənub-qərb", "qərb", "şimal-qərb"}

// Bearing returns the 8-sector Azerbaijani compass direction from a to b.
func Bearing(lat1, lng1, lat2, lng2 float64) string {
	const rad = math.Pi / 180
	dLng := (lng2 - lng1) * rad
	y := math.Sin(dLng) * math.Cos(lat2*rad)
	x := math.Cos(lat1*rad)*math.Sin(lat2*rad) - math.Sin(lat1*rad)*math.Cos(lat2*rad)*math.Cos(dLng)
	deg := math.Atan2(y, x)/rad + 360
	deg = math.Mod(deg, 360)
	idx := int(math.Floor((deg+22.5)/45)) % 8
	return bearings[idx]
}
