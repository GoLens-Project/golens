package golens

import (
	"context"
	"sync"
	"time"
)

// memoryStorage keeps recent roll-ups in RAM. It is the default backend and
// requires no external dependencies. Buckets older than ttl are evicted on
// each Store; the ring cap (max) is a hard upper bound.
type memoryStorage struct {
	mu      sync.Mutex
	buckets []AggregatedMetric
	max     int
	ttl     time.Duration
}

func newMemoryStorage(ttl time.Duration) *memoryStorage {
	return &memoryStorage{max: 100_000, ttl: ttl}
}

func (s *memoryStorage) Store(_ context.Context, m AggregatedMetric) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ttl > 0 {
		cutoff := time.Now().Add(-s.ttl)
		// buckets are append-ordered by time; drop from the head while stale.
		for len(s.buckets) > 0 && s.buckets[0].WindowEnd.Before(cutoff) {
			s.buckets = s.buckets[1:]
		}
	}
	if len(s.buckets) >= s.max {
		// Ring-style: drop oldest.
		s.buckets = s.buckets[1:]
	}
	s.buckets = append(s.buckets, m)
	return nil
}

func (s *memoryStorage) Query(_ context.Context, q Query) ([]AggregatedMetric, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AggregatedMetric, 0)
	for _, b := range s.buckets {
		if q.Name != "" && b.Name != q.Name {
			continue
		}
		if !q.From.IsZero() && b.WindowEnd.Before(q.From) {
			continue
		}
		if !q.To.IsZero() && b.WindowStart.After(q.To) {
			continue
		}
		out = append(out, b)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (s *memoryStorage) Close() error { return nil }
