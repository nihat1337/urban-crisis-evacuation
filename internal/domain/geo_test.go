package domain

import "testing"

func TestDistanceM(t *testing.T) {
	a := Point{Lat: 40.3870, Lng: 49.8300} // Nəsimi
	b := Point{Lat: 40.3660, Lng: 49.8350} // Səbail
	d := DistanceM(a, b)
	if d < 2300 || d > 2500 {
		t.Fatalf("unexpected distance %.0f m", d)
	}
	if DistanceM(a, a) != 0 {
		t.Fatal("distance to self must be 0")
	}
}

func TestOccupancyPct(t *testing.T) {
	s := Shelter{Capacity: 200, Occupancy: 191}
	if got := s.OccupancyPct(); got != 95.5 {
		t.Fatalf("got %v want 95.5", got)
	}
	if (Shelter{}).OccupancyPct() != 100 {
		t.Fatal("zero capacity must read as full")
	}
}
