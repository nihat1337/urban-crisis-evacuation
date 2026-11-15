package api

import (
	"sort"
	"sync"
	"time"
)

// metrics keeps a 60-second sliding window of request latencies so the
// dispatcher panel and /api/v1/metrics can show avg/p95 under load.
type metrics struct {
	mu      sync.Mutex
	samples []sample // ring buffer
	next    int
	full    bool
	counts  map[int]int64 // status class → total
	total   int64
}

type sample struct {
	at     time.Time
	ms     float64
	status int
	path   string
}

func newMetrics(size int) *metrics {
	return &metrics{samples: make([]sample, size), counts: map[int]int64{}}
}

func (m *metrics) record(path string, status int, d time.Duration) {
	m.mu.Lock()
	m.samples[m.next] = sample{at: time.Now(), ms: float64(d.Microseconds()) / 1000, status: status, path: path}
	m.next = (m.next + 1) % len(m.samples)
	if m.next == 0 {
		m.full = true
	}
	m.counts[status/100]++
	m.total++
	m.mu.Unlock()
}

type MetricsSnapshot struct {
	WindowS      int              `json:"window_s"`
	Requests     int              `json:"requests"`
	RPS          float64          `json:"rps"`
	AvgMs        float64          `json:"avg_ms"`
	P50Ms        float64          `json:"p50_ms"`
	P95Ms        float64          `json:"p95_ms"`
	MaxMs        float64          `json:"max_ms"`
	ByStatus     map[string]int   `json:"by_status"`
	SOS          *MetricsEndpoint `json:"sos,omitempty"`
	TotalServed  int64            `json:"total_served"`
	TotalByClass map[string]int64 `json:"total_by_class"`
}

type MetricsEndpoint struct {
	Requests int     `json:"requests"`
	AvgMs    float64 `json:"avg_ms"`
	P95Ms    float64 `json:"p95_ms"`
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * p)
	return sorted[i]
}

func (m *metrics) snapshot(window time.Duration) MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	cut := time.Now().Add(-window)
	n := m.next
	if m.full {
		n = len(m.samples)
	}
	var all, sos []float64
	byStatus := map[string]int{}
	var sum, sumSOS, max float64
	for i := 0; i < n; i++ {
		s := m.samples[i]
		if s.at.Before(cut) {
			continue
		}
		all = append(all, s.ms)
		sum += s.ms
		if s.ms > max {
			max = s.ms
		}
		byStatus[statusClass(s.status)]++
		if s.path == "/api/v1/sos" {
			sos = append(sos, s.ms)
			sumSOS += s.ms
		}
	}
	snap := MetricsSnapshot{WindowS: int(window.Seconds()), Requests: len(all), ByStatus: byStatus, TotalServed: m.total, TotalByClass: map[string]int64{}}
	for k, v := range m.counts {
		snap.TotalByClass[statusClass(k*100)] = v
	}
	if len(all) > 0 {
		sort.Float64s(all)
		snap.RPS = round1(float64(len(all)) / window.Seconds())
		snap.AvgMs = round1(sum / float64(len(all)))
		snap.P50Ms = round1(pct(all, 0.5))
		snap.P95Ms = round1(pct(all, 0.95))
		snap.MaxMs = round1(max)
	}
	if len(sos) > 0 {
		sort.Float64s(sos)
		snap.SOS = &MetricsEndpoint{Requests: len(sos), AvgMs: round1(sumSOS / float64(len(sos))), P95Ms: round1(pct(sos, 0.95))}
	}
	return snap
}

func statusClass(code int) string {
	switch code / 100 {
	case 2:
		return "2xx"
	case 3:
		return "3xx"
	case 4:
		return "4xx"
	default:
		return "5xx"
	}
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
