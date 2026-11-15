// Package seed contains the demo dataset for Bakı: districts, assembly points,
// rescue brigades and a simulated road graph. It is applied once on first start.
package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store"
)

func p(lat, lng float64) domain.Point { return domain.Point{Lat: lat, Lng: lng} }

func Zones() []domain.Zone {
	return []domain.Zone{
		{ID: "nasimi", Name: "Nəsimi", Center: p(40.3870, 49.8300), RadiusM: 2500},
		{ID: "sabail", Name: "Səbail", Center: p(40.3660, 49.8350), RadiusM: 2500},
		{ID: "yasamal", Name: "Yasamal", Center: p(40.3850, 49.8080), RadiusM: 2500},
		{ID: "narimanov", Name: "Nərimanov", Center: p(40.4050, 49.8600), RadiusM: 2500},
		{ID: "khatai", Name: "Xətai", Center: p(40.3830, 49.8720), RadiusM: 2500},
		{ID: "nizami", Name: "Nizami", Center: p(40.4100, 49.9150), RadiusM: 3000},
		{ID: "binagadi", Name: "Binəqədi", Center: p(40.4300, 49.8100), RadiusM: 3000},
		{ID: "sabunchu", Name: "Sabunçu", Center: p(40.4400, 49.9500), RadiusM: 3500},
	}
}

// Shelters: underground bunkers (sığınacaq) are reached by stairs, so they are
// not accessible for wheelchairs / medical transport; parks and stadiums are.
func Shelters() []domain.Shelter {
	return []domain.Shelter{
		{ID: "sh-boulevard", Name: "Dənizkənarı Milli Park", Type: domain.ShelterPark, ZoneID: "sabail", Location: p(40.3620, 49.8400), Capacity: 5000, Occupancy: 1200, Active: true, Accessible: true},
		{ID: "sh-zabitler", Name: "Zabitlər Parkı", Type: domain.ShelterPark, ZoneID: "nasimi", Location: p(40.3760, 49.8350), Capacity: 2500, Occupancy: 900, Active: true, Accessible: true},
		{ID: "sh-nasimi-1", Name: "Nəsimi Sığınacağı №1", Type: domain.ShelterBunker, ZoneID: "nasimi", Location: p(40.3900, 49.8380), Capacity: 800, Occupancy: 300, Active: true, Accessible: false},
		{ID: "sh-bahramov", Name: "Tofiq Bəhramov Stadionu", Type: domain.ShelterStadium, ZoneID: "narimanov", Location: p(40.3980, 49.8530), Capacity: 30000, Occupancy: 4000, Active: true, Accessible: true},
		{ID: "sh-olympic", Name: "Bakı Olimpiya Stadionu", Type: domain.ShelterStadium, ZoneID: "nizami", Location: p(40.4300, 49.9190), Capacity: 68000, Occupancy: 2000, Active: true, Accessible: true},
		{ID: "sh-yasamal-2", Name: "Yasamal Sığınacağı №2", Type: domain.ShelterBunker, ZoneID: "yasamal", Location: p(40.3870, 49.8120), Capacity: 800, Occupancy: 560, Active: true, Accessible: false},
		{ID: "sh-khatai-park", Name: "Xətai Mərkəzi Parkı", Type: domain.ShelterPark, ZoneID: "khatai", Location: p(40.3900, 49.8750), Capacity: 4000, Occupancy: 1500, Active: true, Accessible: true},
		{ID: "sh-khatai-3", Name: "Xətai Sığınacağı №3", Type: domain.ShelterBunker, ZoneID: "khatai", Location: p(40.3800, 49.8700), Capacity: 600, Occupancy: 546, Active: true, Accessible: false},
		{ID: "sh-binagadi-4", Name: "Binəqədi Sığınacağı №4", Type: domain.ShelterBunker, ZoneID: "binagadi", Location: p(40.4280, 49.8150), Capacity: 700, Occupancy: 200, Active: true, Accessible: false},
		{ID: "sh-sabunchu-sq", Name: "Sabunçu Toplanış Meydanı", Type: domain.ShelterPark, ZoneID: "sabunchu", Location: p(40.4420, 49.9480), Capacity: 3000, Occupancy: 400, Active: true, Accessible: true},
	}
}

func Brigades(now time.Time) []domain.Brigade {
	return []domain.Brigade{
		{ID: "br-1", Name: "Xilasetmə briqadası №1", Status: domain.BrigadeAvailable, Location: p(40.3800, 49.8300), ZoneID: "nasimi", UpdatedAt: now},
		{ID: "br-2", Name: "Xilasetmə briqadası №2", Status: domain.BrigadeAvailable, Location: p(40.4000, 49.8600), ZoneID: "narimanov", UpdatedAt: now},
		{ID: "br-3", Name: "Xilasetmə briqadası №3", Status: domain.BrigadeAvailable, Location: p(40.4150, 49.9100), ZoneID: "nizami", UpdatedAt: now},
		{ID: "br-4", Name: "Xilasetmə briqadası №4", Status: domain.BrigadeBusy, Location: p(40.4300, 49.8100), ZoneID: "binagadi", UpdatedAt: now},
		{ID: "br-5", Name: "Tibbi briqada №5", Status: domain.BrigadeAvailable, Location: p(40.3850, 49.8700), ZoneID: "khatai", UpdatedAt: now},
	}
}

// Roads returns a simulated road graph: arterial roads between district
// centres plus an access road from each shelter to its district centre.
func Roads(now time.Time) []domain.Road {
	zones := map[string]domain.Point{}
	for _, z := range Zones() {
		zones[z.ID] = z.Center
	}
	// accessible=false marks streets with stairs / steep steps / barriers that a
	// wheelchair or medical transport cannot use (pedestrian stairs on Nizami
	// küçəsi, steep section of Mikayıl Müşfiq küçəsi).
	type arterial struct {
		id, name, a, b string
		accessible     bool
	}
	arterials := []arterial{
		{"r-1", "Azadlıq prospekti", "nasimi", "yasamal", true},
		{"r-2", "Nizami küçəsi", "nasimi", "sabail", false},
		{"r-3", "Heydər Əliyev prospekti", "nasimi", "narimanov", true},
		{"r-4", "Neftçilər prospekti", "sabail", "khatai", true},
		{"r-5", "Nobel prospekti", "khatai", "nizami", true},
		{"r-6", "Ziya Bünyadov prospekti", "narimanov", "nizami", true},
		{"r-7", "Tbilisi prospekti", "yasamal", "binagadi", true},
		{"r-8", "Babək prospekti", "narimanov", "khatai", true},
		{"r-9", "Moskva prospekti", "binagadi", "narimanov", true},
		{"r-10", "Sabunçu yolu", "nizami", "sabunchu", true},
		{"r-11", "Mikayıl Müşfiq küçəsi", "yasamal", "sabail", false},
	}
	var roads []domain.Road
	for _, a := range arterials {
		st := domain.RoadOpen
		if a.id == "r-4" {
			st = domain.RoadCongested // demo: Neftçilər prospekti is congested
		}
		roads = append(roads, domain.Road{ID: a.id, Name: a.name, From: zones[a.a], To: zones[a.b], Status: st, Accessible: a.accessible, UpdatedAt: now})
	}
	// A shelter's access road inherits the shelter's accessibility.
	for i, s := range Shelters() {
		roads = append(roads, domain.Road{
			ID: fmt.Sprintf("r-sh-%d", i+1), Name: s.Name + " girişi",
			From: zones[s.ZoneID], To: s.Location, Status: domain.RoadOpen, Accessible: s.Accessible, UpdatedAt: now,
		})
	}
	return roads
}

// Apply seeds the store if it is empty (no zones yet). On an already seeded
// store it only upgrades attributes introduced later (accessibility flags),
// so a database created before the priority-SOS module keeps live data but
// still learns which shelters/roads have stairs or barriers.
func Apply(ctx context.Context, st store.Store) error {
	zones, err := st.ListZones(ctx)
	if err != nil {
		return err
	}
	if len(zones) > 0 {
		return syncAccessibility(ctx, st)
	}
	now := time.Now().UTC()
	for _, z := range Zones() {
		if err := st.UpsertZone(ctx, z); err != nil {
			return err
		}
	}
	for _, s := range Shelters() {
		if err := st.UpsertShelter(ctx, s); err != nil {
			return err
		}
	}
	for _, b := range Brigades(now) {
		if err := st.UpsertBrigade(ctx, b); err != nil {
			return err
		}
	}
	for _, r := range Roads(now) {
		if err := st.UpsertRoad(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// syncAccessibility copies the Accessible flag from the seed definitions onto
// existing shelters/roads without touching occupancy, active or road status.
func syncAccessibility(ctx context.Context, st store.Store) error {
	seedShelters := map[string]bool{}
	for _, s := range Shelters() {
		seedShelters[s.ID] = s.Accessible
	}
	existing, err := st.ListShelters(ctx)
	if err != nil {
		return err
	}
	for _, sh := range existing {
		if want, ok := seedShelters[sh.ID]; ok && sh.Accessible != want {
			sh.Accessible = want
			if err := st.UpsertShelter(ctx, sh); err != nil {
				return err
			}
		}
	}
	seedRoads := map[string]bool{}
	for _, r := range Roads(time.Time{}) {
		seedRoads[r.ID] = r.Accessible
	}
	roads, err := st.ListRoads(ctx)
	if err != nil {
		return err
	}
	for _, r := range roads {
		if want, ok := seedRoads[r.ID]; ok && r.Accessible != want {
			r.Accessible = want
			if err := st.UpsertRoad(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}
