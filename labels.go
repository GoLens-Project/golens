package golens

import (
	"fmt"
	"strings"
	"sync"
)

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

// SanitizeLabelValue applies configured label sanitization rules to a value.
// It normalizes paths (if enabled), enforces max length, and fingerprints
// high-cardinality values to keep series bounded.
func SanitizeLabelValue(name, value string, cfg LabelsConfig) string {
	if value == "" {
		return value
	}
	// Normalize path labels when enabled.
	if cfg.NormalizePaths && name == "path" {
		value = normalizePath(value)
	}
	// Truncate long values.
	if cfg.MaxLength > 0 && len(value) > cfg.MaxLength {
		value = value[:cfg.MaxLength]
	}
	return value
}

// SanitizeLabels applies sanitization to all labels in a slice.
func SanitizeLabels(labels []Label, cfg LabelsConfig) []Label {
	if cfg.MaxLength == 0 && !cfg.NormalizePaths {
		return labels // fast path: no sanitization configured
	}
	out := make([]Label, len(labels))
	for i, l := range labels {
		out[i] = Label{Name: l.Name, Value: SanitizeLabelValue(l.Name, l.Value, cfg)}
	}
	return out
}

// cardinalityTracker monitors unique label combinations per metric name and
// drops samples when a configurable threshold is exceeded. This prevents
// unbounded cardinality growth from high-cardinality label values like user
// IDs, raw error messages, or dynamic route segments.
type cardinalityTracker struct {
	mu       sync.Mutex
	seen     map[string]map[string]struct{} // metricName -> set of label fingerprints
	maxPer   int                            // max unique series per metric (0 = unlimited)
	dropped  map[string]int64               // metricName -> count of dropped samples
	warnOnce map[string]bool                // track which metrics have logged a warning
	debug    bool
}

func newCardinalityTracker(maxPer int, debug bool) *cardinalityTracker {
	return &cardinalityTracker{
		seen:     make(map[string]map[string]struct{}),
		maxPer:   maxPer,
		dropped:  make(map[string]int64),
		warnOnce: make(map[string]bool),
		debug:    debug,
	}
}

// Allow reports whether the given label combination is within the cardinality
// cap for the named metric. Returns true if allowed, false if dropped.
// When maxPer is 0, all combinations are allowed (unlimited).
func (ct *cardinalityTracker) Allow(metricName string, labels []Label) bool {
	if ct.maxPer == 0 {
		return true // unlimited
	}
	fp := fingerprintLabels(labels)
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if _, ok := ct.seen[metricName]; !ok {
		ct.seen[metricName] = make(map[string]struct{})
	}
	if _, exists := ct.seen[metricName][fp]; exists {
		return true // already seen
	}
	if len(ct.seen[metricName]) >= ct.maxPer {
		ct.dropped[metricName]++
		return false // cap reached
	}
	ct.seen[metricName][fp] = struct{}{}
	return true
}

// DroppedCount returns the total number of dropped samples for a metric.
func (ct *cardinalityTracker) DroppedCount(metricName string) int64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.dropped[metricName]
}

// SeriesCount returns the number of unique label series for a metric.
func (ct *cardinalityTracker) SeriesCount(metricName string) int {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return len(ct.seen[metricName])
}

// AllSeriesCounts returns a snapshot of series counts for all tracked metrics.
func (ct *cardinalityTracker) AllSeriesCounts() map[string]int {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	out := make(map[string]int, len(ct.seen))
	for name, series := range ct.seen {
		out[name] = len(series)
	}
	return out
}

// AllDroppedCounts returns a snapshot of dropped counts for all tracked metrics.
func (ct *cardinalityTracker) AllDroppedCounts() map[string]int64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	out := make(map[string]int64, len(ct.dropped))
	for name, count := range ct.dropped {
		out[name] = count
	}
	return out
}

// CardinalitySnapshot is a per-metric view of cardinality state for the UI.
type CardinalitySnapshot struct {
	MetricName string `json:"metric_name"`
	Series     int    `json:"series"`
	MaxSeries  int    `json:"max_series"`
	Dropped    int64  `json:"dropped"`
}

// fingerprintLabels produces a deterministic string fingerprint for a label
// set so it can be used as a map key for deduplication.
func fingerprintLabels(labels []Label) string {
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	for i, l := range labels {
		if i > 0 {
			b.WriteByte('\x00')
		}
		b.WriteString(l.Name)
		b.WriteByte('=')
		b.WriteString(l.Value)
	}
	return b.String()
}

// formatCardinalityValue formats a cardinality count with a human-readable
// suffix for the dashboard (e.g., "47", "1.2k", "3.4M").
func formatCardinalityValue(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
