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

func Shelters() []domain.Shelter {
	return []domain.Shelter{
		{ID: "sh-boulevard", Name: "Dənizkənarı Milli Park", Type: domain.ShelterPark, ZoneID: "sabail", Location: p(40.3620, 49.8400), Capacity: 5000, Occupancy: 1200, Active: true},
		{ID: "sh-zabitler", Name: "Zabitlər Parkı", Type: domain.ShelterPark, ZoneID: "nasimi", Location: p(40.3760, 49.8350), Capacity: 2500, Occupancy: 900, Active: true},
		{ID: "sh-nasimi-1", Name: "Nəsimi Sığınacağı №1", Type: domain.ShelterBunker, ZoneID: "nasimi", Location: p(40.3900, 49.8380), Capacity: 800, Occupancy: 300, Active: true},
		{ID: "sh-bahramov", Name: "Tofiq Bəhramov Stadionu", Type: domain.ShelterStadium, ZoneID: "narimanov", Location: p(40.3980, 49.8530), Capacity: 30000, Occupancy: 4000, Active: true},
		{ID: "sh-olympic", Name: "Bakı Olimpiya Stadionu", Type: domain.ShelterStadium, ZoneID: "nizami", Location: p(40.4300, 49.9190), Capacity: 68000, Occupancy: 2000, Active: true},
		{ID: "sh-yasamal-2", Name: "Yasamal Sığınacağı №2", Type: domain.ShelterBunker, ZoneID: "yasamal", Location: p(40.3870, 49.8120), Capacity: 800, Occupancy: 560, Active: true},
		{ID: "sh-khatai-park", Name: "Xətai Mərkəzi Parkı", Type: domain.ShelterPark, ZoneID: "khatai", Location: p(40.3900, 49.8750), Capacity: 4000, Occupancy: 1500, Active: true},
		{ID: "sh-khatai-3", Name: "Xətai Sığınacağı №3", Type: domain.ShelterBunker, ZoneID: "khatai", Location: p(40.3800, 49.8700), Capacity: 600, Occupancy: 546, Active: true},
		{ID: "sh-binagadi-4", Name: "Binəqədi Sığınacağı №4", Type: domain.ShelterBunker, ZoneID: "binagadi", Location: p(40.4280, 49.8150), Capacity: 700, Occupancy: 200, Active: true},
		{ID: "sh-sabunchu-sq", Name: "Sabunçu Toplanış Meydanı", Type: domain.ShelterPark, ZoneID: "sabunchu", Location: p(40.4420, 49.9480), Capacity: 3000, Occupancy: 400, Active: true},
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
	type arterial struct{ id, name, a, b string }
	arterials := []arterial{
		{"r-1", "Azadlıq prospekti", "nasimi", "yasamal"},
		{"r-2", "Nizami küçəsi", "nasimi", "sabail"},
		{"r-3", "Heydər Əliyev prospekti", "nasimi", "narimanov"},
		{"r-4", "Neftçilər prospekti", "sabail", "khatai"},
		{"r-5", "Nobel prospekti", "khatai", "nizami"},
		{"r-6", "Ziya Bünyadov prospekti", "narimanov", "nizami"},
		{"r-7", "Tbilisi prospekti", "yasamal", "binagadi"},
		{"r-8", "Babək prospekti", "narimanov", "khatai"},
		{"r-9", "Moskva prospekti", "binagadi", "narimanov"},
		{"r-10", "Sabunçu yolu", "nizami", "sabunchu"},
		{"r-11", "Mikayıl Müşfiq küçəsi", "yasamal", "sabail"},
	}
	var roads []domain.Road
	for _, a := range arterials {
		st := domain.RoadOpen
		if a.id == "r-4" {
			st = domain.RoadCongested // demo: Neftçilər prospekti is congested
		}
		roads = append(roads, domain.Road{ID: a.id, Name: a.name, From: zones[a.a], To: zones[a.b], Status: st, UpdatedAt: now})
	}
	for i, s := range Shelters() {
		roads = append(roads, domain.Road{
			ID: fmt.Sprintf("r-sh-%d", i+1), Name: s.Name + " girişi",
			From: zones[s.ZoneID], To: s.Location, Status: domain.RoadOpen, UpdatedAt: now,
		})
	}
	return roads
}

// Apply seeds the store if it is empty (no zones yet).
func Apply(ctx context.Context, st store.Store) error {
	zones, err := st.ListZones(ctx)
	if err != nil {
		return err
	}
	if len(zones) > 0 {
		return nil
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
