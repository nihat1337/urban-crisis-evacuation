package plan

import (
	"sort"
	"testing"
)

// 250 located people and 6 open points with capacity 120 -> exactly 100/100/50.
// The test does not hardcode point IDs: the first chosen point depends on the
// centroid of the remaining people, so only the group sizes are contractual.
func TestBuildGroups250(t *testing.T) {
	people := make([]Person, 250)
	for i := range people {
		people[i] = Person{
			Phone:  pnum(i),
			Lat:    40.383 + float64(i%10)*0.001,
			Lng:    49.872 + float64(i/10%10)*0.001,
			HasLoc: true,
		}
	}
	points := make([]Point, 6)
	for i := range points {
		points[i] = Point{
			ID: string(rune('a' + i)), Name: string(rune('A' + i)),
			Lat: 40.383 + float64(i)*0.0005, Lng: 49.872 + float64(i)*0.0005,
			Capacity: 120, Open: true,
		}
	}

	res := Build(people, points, 3000)

	if len(res.Assignments) != 250 {
		t.Fatalf("assigned = %d, want 250", len(res.Assignments))
	}
	if len(res.Unassigned) != 0 {
		t.Fatalf("unassigned = %d, want 0", len(res.Unassigned))
	}
	counts := map[string]int{}
	for _, a := range res.Assignments {
		counts[a.PointID]++
	}
	sizes := make([]int, 0, len(counts))
	for _, n := range counts {
		sizes = append(sizes, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sizes)))
	if len(sizes) != 3 || sizes[0] != 100 || sizes[1] != 100 || sizes[2] != 50 {
		t.Fatalf("group sizes = %v, want [100 100 50]", sizes)
	}
	if len(res.Points) != 3 {
		t.Fatalf("used points = %d, want 3", len(res.Points))
	}
}

func pnum(i int) string {
	digits := []byte("000000")
	for p := len(digits) - 1; p >= 0 && i > 0; p-- {
		digits[p] = byte('0' + i%10)
		i /= 10
	}
	return "+99450" + string(digits)
}
