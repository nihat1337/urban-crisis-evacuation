package routing

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

// Simulated routes over the road graph stored in the system using Dijkstra.
// Blocked roads are removed from the graph; congested roads are penalised.
// It never fails: with no usable graph it degrades to a straight line.
type Simulated struct {
	SpeedMps         float64 // pedestrian evacuation speed
	CongestionFactor float64
	ConnectK         int // how many graph nodes to connect origin/destination to
}

func NewSimulated(speedMps float64) *Simulated {
	if speedMps <= 0 {
		speedMps = 1.3
	}
	return &Simulated{SpeedMps: speedMps, CongestionFactor: 1.8, ConnectK: 2}
}

func (s *Simulated) Name() string { return "simulated" }

func key(p domain.Point) string { return fmt.Sprintf("%.5f,%.5f", p.Lat, p.Lng) }

type edge struct {
	to   int
	w    float64 // weight used by Dijkstra
	dist float64 // real distance in metres
}

func (s *Simulated) Route(_ context.Context, from, to domain.Point, roads []domain.Road) (domain.Route, error) {
	idx := map[string]int{}
	var nodes []domain.Point
	getNode := func(p domain.Point) int {
		k := key(p)
		if i, ok := idx[k]; ok {
			return i
		}
		idx[k] = len(nodes)
		nodes = append(nodes, p)
		return len(nodes) - 1
	}
	adj := map[int][]edge{}
	add := func(a, b int, w, d float64) {
		adj[a] = append(adj[a], edge{b, w, d})
		adj[b] = append(adj[b], edge{a, w, d})
	}

	anyBlocked := false
	for _, r := range roads {
		if r.Status == domain.RoadBlocked {
			anyBlocked = true
			continue
		}
		a, b := getNode(r.From), getNode(r.To)
		d := domain.DistanceM(r.From, r.To)
		w := d
		if r.Status == domain.RoadCongested {
			w *= s.CongestionFactor
		}
		add(a, b, w, d)
	}
	graphNodes := len(nodes)
	if graphNodes == 0 {
		return straightLine(from, to, s.SpeedMps, "yol qrafı boşdur; düz xətt"), nil
	}

	src := getNode(from)
	dst := getNode(to)
	// Attach origin/destination to their nearest real graph nodes via virtual access edges.
	connect := func(n int) {
		if n < graphNodes {
			return // already part of the graph
		}
		type cand struct {
			i int
			d float64
		}
		var cands []cand
		for i := 0; i < graphNodes; i++ {
			cands = append(cands, cand{i, domain.DistanceM(nodes[n], nodes[i])})
		}
		sort.Slice(cands, func(a, b int) bool { return cands[a].d < cands[b].d })
		for i := 0; i < len(cands) && i < s.ConnectK; i++ {
			add(n, cands[i].i, cands[i].d*1.3, cands[i].d)
		}
	}
	connect(src)
	if dst != src {
		connect(dst)
	}

	// Dijkstra (graph is small: O(n²) is fine and allocation-free).
	n := len(nodes)
	const inf = math.MaxFloat64
	dist := make([]float64, n)
	real := make([]float64, n)
	prev := make([]int, n)
	done := make([]bool, n)
	for i := range dist {
		dist[i] = inf
		prev[i] = -1
	}
	dist[src] = 0
	for {
		u := -1
		for i := 0; i < n; i++ {
			if !done[i] && dist[i] < inf && (u == -1 || dist[i] < dist[u]) {
				u = i
			}
		}
		if u == -1 || u == dst {
			break
		}
		done[u] = true
		for _, e := range adj[u] {
			if nd := dist[u] + e.w; nd < dist[e.to] {
				dist[e.to] = nd
				real[e.to] = real[u] + e.dist
				prev[e.to] = u
			}
		}
	}
	if dist[dst] == inf {
		r := straightLine(from, to, s.SpeedMps, "qrafda əlaqə yoxdur; düz xətt")
		r.Detour = anyBlocked
		return r, nil
	}
	var path []domain.Point
	for v := dst; v != -1; v = prev[v] {
		path = append(path, nodes[v])
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	d := real[dst]
	return domain.Route{
		Source:    "simulated",
		DistanceM: math.Round(d),
		DurationS: math.Round(d / s.SpeedMps),
		Geometry:  path,
		Detour:    anyBlocked,
	}, nil
}
