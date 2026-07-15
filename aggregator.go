package golens

import (
	"context"
	"log"
	"sync"
	"time"
)

// aggregator maintains in-flight tumbling-window roll-ups. On each flush it
// computes count/sum/min/max per metric window and writes the result to the
// configured Storage, then starts a fresh window.
type aggregator struct {
	mu      sync.Mutex
	windows map[string]*window
}

type window struct {
	count            int64
	sum              float64
	min              float64
	max              float64
	start            time.Time
	histogramBuckets map[float64]int64 // histogram bucket counts (boundary -> count)
}

func newAggregator(_ Storage, _ time.Duration) *aggregator {
	return &aggregator{windows: make(map[string]*window)}
}

func (a *aggregator) add(name string, v float64, m *Metric) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w, ok := a.windows[name]
	if !ok {
		w = &window{start: time.Now(), min: v, max: v, histogramBuckets: make(map[float64]int64)}
		a.windows[name] = w
	}
	if v < w.min {
		w.min = v
	}
	if v > w.max {
		w.max = v
	}
	w.count++
	w.sum += v

	// Capture histogram bucket counts for histogram metrics
	if m != nil && m.Type == HistogramType {
		m.value.mu.RLock()
		for bound, counter := range m.value.hist {
			w.histogramBuckets[bound] = counter.Load()
		}
		m.value.mu.RUnlock()
	}
}

// flushAll materializes every open window into the storage backend.
func (a *aggregator) flushAll(r *Registry) {
	a.mu.Lock()
	wins := a.windows
	a.windows = make(map[string]*window, len(wins))
	a.mu.Unlock()

	if len(wins) == 0 {
		return
	}
	now := time.Now()
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for name, w := range wins {
		// Convert histogram buckets map to slice format for storage
		histogramBuckets := make([]HistogramBucket, 0, len(w.histogramBuckets))
		for bound, count := range w.histogramBuckets {
			histogramBuckets = append(histogramBuckets, HistogramBucket{
				UpperBound: bound,
				Count:      count,
			})
		}

		agg := AggregatedMetric{
			Name:            name,
			Count:           w.count,
			Sum:             w.sum,
			Min:             w.min,
			Max:             w.max,
			WindowStart:     w.start,
			WindowEnd:       now,
			HistogramBuckets: histogramBuckets,
		}
		if err := r.storage.Store(ctx, agg); err != nil {
			if r.debug {
				log.Printf("[golens] storage flush failed for %s: %v", name, err)
			}
		}
	}
}
