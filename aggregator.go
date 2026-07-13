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
	count int64
	sum   float64
	min   float64
	max   float64
	start time.Time
}

func newAggregator(_ Storage, _ time.Duration) *aggregator {
	return &aggregator{windows: make(map[string]*window)}
}

func (a *aggregator) add(name string, v float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w, ok := a.windows[name]
	if !ok {
		w = &window{start: time.Now(), min: v, max: v}
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
		agg := AggregatedMetric{
			Name:        name,
			Count:       w.count,
			Sum:         w.sum,
			Min:         w.min,
			Max:         w.max,
			WindowStart: w.start,
			WindowEnd:   now,
		}
		if err := r.storage.Store(ctx, agg); err != nil {
			if r.debug {
				log.Printf("[golens] storage flush failed for %s: %v", name, err)
			}
		}
	}
}
