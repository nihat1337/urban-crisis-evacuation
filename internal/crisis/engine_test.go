package crisis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store/memory"
)

func newEngine(t *testing.T) (*Engine, *memory.Store, *time.Time) {
	t.Helper()
	st := memory.New()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	e := New(DefaultConfig(), st)
	e.Now = func() time.Time { return now }
	return e, st, &now
}

func sos(i int, citizen string, zone string, lat, lng float64, at time.Time) domain.SOS {
	return domain.SOS{ID: fmt.Sprintf("s%d", i), CitizenID: citizen, Type: domain.SOSFire, ZoneID: zone,
		Location: domain.Point{Lat: lat, Lng: lng}, Status: domain.SOSOpen, CreatedAt: at}
}

func TestClusterAlertAfterFourDistinctSignals(t *testing.T) {
	e, st, now := newEngine(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		s := sos(i, fmt.Sprintf("c%d", i), "nasimi", 40.387, 49.830, now.Add(-time.Duration(i)*time.Minute))
		_ = st.CreateSOS(ctx, s)
		if a, _ := e.OnSOS(ctx, s); a != nil {
			t.Fatalf("no alert expected after %d signals", i)
		}
	}
	s4 := sos(4, "c4", "nasimi", 40.388, 49.831, *now)
	_ = st.CreateSOS(ctx, s4)
	a, err := e.OnSOS(ctx, s4)
	if err != nil || a == nil {
		t.Fatalf("expected alert, got %v %v", a, err)
	}
	if a.Level != domain.AlertRed || a.Kind != domain.AlertSOSCluster || a.SignalCount != 4 {
		t.Fatalf("unexpected alert %+v", a)
	}
	// 5th signal must not create a duplicate while the first is unexecuted.
	s5 := sos(5, "c5", "nasimi", 40.388, 49.831, *now)
	_ = st.CreateSOS(ctx, s5)
	if a2, _ := e.OnSOS(ctx, s5); a2 != nil {
		t.Fatal("duplicate alert created")
	}
}

func TestSameCitizenRepeatedDoesNotCount(t *testing.T) {
	e, st, now := newEngine(t)
	ctx := context.Background()
	for i := 1; i <= 6; i++ {
		s := sos(i, "same", "nasimi", 40.387, 49.830, *now)
		_ = st.CreateSOS(ctx, s)
		if a, _ := e.OnSOS(ctx, s); a != nil {
			t.Fatal("same citizen spamming must not trigger an alert")
		}
	}
}

func TestOldSignalsOutsideWindowIgnored(t *testing.T) {
	e, st, now := newEngine(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		s := sos(i, fmt.Sprintf("c%d", i), "nasimi", 40.387, 49.830, now.Add(-10*time.Minute))
		_ = st.CreateSOS(ctx, s)
	}
	s4 := sos(4, "c4", "nasimi", 40.387, 49.830, *now)
	_ = st.CreateSOS(ctx, s4)
	if a, _ := e.OnSOS(ctx, s4); a != nil {
		t.Fatal("signals older than the window must be ignored")
	}
}

func TestRadiusAcrossZones(t *testing.T) {
	e, st, now := newEngine(t)
	ctx := context.Background()
	// Different zone ids but within 500 m of each other.
	for i := 1; i <= 3; i++ {
		s := sos(i, fmt.Sprintf("c%d", i), "zoneA", 40.3870, 49.8300, *now)
		_ = st.CreateSOS(ctx, s)
		_, _ = e.OnSOS(ctx, s)
	}
	s4 := sos(4, "c4", "zoneB", 40.3890, 49.8310, *now) // ~240 m away
	_ = st.CreateSOS(ctx, s4)
	if a, _ := e.OnSOS(ctx, s4); a == nil {
		t.Fatal("expected alert via 500 m radius rule")
	}
}

func TestShelterFullAlert(t *testing.T) {
	e, _, _ := newEngine(t)
	ctx := context.Background()
	sh := domain.Shelter{ID: "x", Name: "Test", ZoneID: "nasimi", Capacity: 100, Occupancy: 95, Active: true}
	if a, _ := e.OnShelter(ctx, sh); a != nil {
		t.Fatal("exactly 95% must not alert (rule is > 95%)")
	}
	sh.Occupancy = 96
	a, err := e.OnShelter(ctx, sh)
	if err != nil || a == nil || a.Kind != domain.AlertShelterFull || a.ShelterID != "x" {
		t.Fatalf("expected shelter alert, got %+v %v", a, err)
	}
	if a2, _ := e.OnShelter(ctx, sh); a2 != nil {
		t.Fatal("duplicate shelter alert")
	}
}
