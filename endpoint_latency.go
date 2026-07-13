package golens

import (
	"sort"
	"strings"
	"sync"
)

// endpointBounds are the histogram bucket boundaries (seconds) used for
// per-endpoint response-latency tracking.
var endpointBounds = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// endpointTracker records a response-latency histogram per normalized endpoint
// (method + path template). The number of distinct endpoints is bounded by max
// to protect against path-cardinality explosion (e.g. /users/123, /users/456).
type endpointTracker struct {
	mu     sync.Mutex
	max    int
	bounds []float64
	series map[string]*latencySeries
}

type latencySeries struct {
	method string
	path   string
	counts []int64 // len(bounds) + 1; last bucket is +Inf
	total  int64
	sum    float64
}

func newEndpointTracker(max int) *endpointTracker {
	if max <= 0 {
		max = 128
	}
	return &endpointTracker{max: max, bounds: endpointBounds, series: make(map[string]*latencySeries)}
}

// idSegment matches high-cardinality path segments: pure numbers, UUIDs, and
// long hex hashes. These are collapsed to ":id" so per-endpoint series stay
// bounded regardless of how many distinct IDs hit the route.
func idSegment(seg string) bool {
	if seg == "" {
		return false
	}
	digits, hex := 0, 0
	for _, r := range seg {
		switch {
		case r >= '0' && r <= '9':
			digits++
			hex++
		case (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F'):
			hex++
		case r == '-':
			// allow UUID-style separators
		default:
			return false
		}
	}
	// all-digit of any length, or hex/hash/uuid of length (>=8 hex chars)
	return digits == len(seg) || hex >= 8
}

// normalizePath collapses id-like segments to ":id".
func normalizePath(path string) string {
	if path == "" {
		return "/"
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range parts {
		if idSegment(seg) {
			parts[i] = ":id"
		}
	}
	return "/" + strings.Join(parts, "/")
}

// Observe records a latency sample for the given endpoint.
func (t *endpointTracker) Observe(method, path string, seconds float64) {
	path = normalizePath(path)
	key := method + "\t" + path

	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.series[key]
	if !ok {
		if len(t.series) >= t.max {
			return // bound reached: drop the new endpoint
		}
		s = &latencySeries{method: method, path: path, counts: make([]int64, len(t.bounds)+1)}
		t.series[key] = s
	}
	idx := sort.SearchFloat64s(t.bounds, seconds) // len(bounds) → +Inf bucket
	s.counts[idx]++
	s.total++
	s.sum += seconds
}

// percentile returns the approximate p-th quantile (0..1) via linear
// interpolation across histogram buckets.
func (s *latencySeries) percentile(bounds []float64, p float64) float64 {
	if s.total == 0 {
		return 0
	}
	target := p * float64(s.total)
	var cum, prev int64
	for i := 0; i < len(bounds); i++ {
		ci := s.counts[i]
		cum += ci
		if float64(cum) >= target {
			if ci == 0 {
				return bounds[i]
			}
			lower := 0.0
			if i > 0 {
				lower = bounds[i-1]
			}
			upper := bounds[i]
			return lower + (upper-lower)*(target-float64(prev))/float64(ci)
		}
		prev = cum
	}
	return bounds[len(bounds)-1] // landed in +Inf bucket
}

// EndpointLatencySnapshot is a serializable per-endpoint latency summary.
type EndpointLatencySnapshot struct {
	Method string  `json:"method"`
	Path   string  `json:"path"`
	Count  int64   `json:"count"`
	Avg    float64 `json:"avg"`
	P50    float64 `json:"p50"`
	P95    float64 `json:"p95"`
	P99    float64 `json:"p99"`
}

// Snapshots returns all tracked endpoints, sorted by path then method.
func (t *endpointTracker) Snapshots() []EndpointLatencySnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]EndpointLatencySnapshot, 0, len(t.series))
	for _, s := range t.series {
		avg := 0.0
		if s.total > 0 {
			avg = s.sum / float64(s.total)
		}
		out = append(out, EndpointLatencySnapshot{
			Method: s.method, Path: s.path, Count: s.total, Avg: avg,
			P50: s.percentile(t.bounds, 0.50),
			P95: s.percentile(t.bounds, 0.95),
			P99: s.percentile(t.bounds, 0.99),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// Reset clears all tracked series (used in tests).
func (t *endpointTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.series = make(map[string]*latencySeries)
}
