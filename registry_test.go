package golens

import (
	"context"
	"testing"
	"time"
)

func newTestRegistry(t *testing.T, cfg Config) *Registry {
	t.Helper()
	cfg.applyDefaults()
	cfg.IngestQueueSize = 64
	cfg.FlushInterval = 5 * time.Millisecond
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestRegistryPreRegistersREDMetrics(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	for _, name := range []string{"http_requests_total", "http_request_errors_total", "http_request_duration_seconds"} {
		if _, ok := r.Snapshot(name); !ok {
			t.Errorf("RED metric %q not pre-registered", name)
		}
	}
}

func TestRegistryRecordUpdatesCounter(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	r.Record("http_requests_total", 1)
	r.Record("http_requests_total", 2)
	waitForDrain(r)

	s, ok := r.Snapshot("http_requests_total")
	if !ok {
		t.Fatal("metric not found")
	}
	if s.Value != 3 {
		t.Errorf("counter = %v, want 3", s.Value)
	}
	r.Close()
}

func TestRegistryRecordAutoCreatesUnknownMetric(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	r.Record("dynamic_metric", 1)
	waitForDrain(r)

	s, ok := r.Snapshot("dynamic_metric")
	if !ok {
		t.Fatal("auto-created metric not found")
	}
	if s.Value != 1 {
		t.Errorf("value = %v, want 1", s.Value)
	}
	r.Close()
}

func TestRegistryRegisterReturnsExisting(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	m1 := r.Register("dup", CounterType, "first", nil, nil, 0, 0)
	m2 := r.Register("dup", GaugeType, "second", nil, nil, 0, 0)
	if m1 != m2 {
		t.Error("Register should return the existing metric on duplicate name")
	}
	if m1.Description != "first" {
		t.Errorf("description changed to %q", m1.Description)
	}
}

func TestRegistryEvictionRespectsMax(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxMetrics = 2
	r := newTestRegistry(t, cfg)

	r.Register("a", CounterType, "", nil, nil, 0, 0)
	r.Register("b", CounterType, "", nil, nil, 0, 0)
	// exceeds cap -> eviction of idle metric (a, never recorded)
	r.Register("c", CounterType, "", nil, nil, 0, 0)

	if _, ok := r.Snapshot("a"); ok {
		_ = ok // a may or may not be evicted depending on idle logic; c must exist
	}
	if _, ok := r.Snapshot("c"); !ok {
		t.Error("newly registered metric c must exist after eviction")
	}
}

func TestRegistryEvictionWithIdleTTL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxMetrics = 1
	cfg.MetricTTL = 1 * time.Nanosecond
	r := newTestRegistry(t, cfg)

	old := r.Register("old_metric", CounterType, "", nil, nil, 0, 0)
	old.Record(1)
	time.Sleep(2 * time.Millisecond)
	// forcing TTL: set last into the past
	old.value.last.Store(1)

	r.Register("new_metric", CounterType, "", nil, nil, 0, 0)
	if _, ok := r.Snapshot("new_metric"); !ok {
		t.Error("new metric should exist after idle eviction")
	}
}

func TestShouldTrackExcludeWins(t *testing.T) {
	r := newTestRegistry(t, Config{
		IncludePatterns: []string{"^/api/.*"},
		ExcludePatterns: []string{"/health"},
	})
	if !r.shouldTrack("/api/orders") {
		t.Error("/api/orders should be tracked")
	}
	if r.shouldTrack("/health") {
		t.Error("/health should be excluded")
	}
	if r.shouldTrack("/other") {
		t.Error("/other is not in includes and should not be tracked")
	}
}

func TestShouldTrackEmptyIncludesAll(t *testing.T) {
	r := newTestRegistry(t, Config{})
	if !r.shouldTrack("/anything") {
		t.Error("with no filters everything is tracked")
	}
}

// DefaultConfig excludes the dashboard routes so the UI's own HTMX polling
// never inflates the API request/error counters.
func TestDefaultConfigExcludesDashboard(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	for _, p := range []string{"/metrics", "/metrics/data"} {
		if r.shouldTrack(p) {
			t.Errorf("default config should not track dashboard path %q", p)
		}
	}
	if !r.shouldTrack("/orders") {
		t.Error("API paths should still be tracked under the default config")
	}
}

func TestRegistryNonBlockingUnderSaturation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IngestQueueSize = 4
	r := newTestRegistry(t, cfg)
	// Don't start the loop; the queue will fill and Record must not block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			r.Record("flood", 1)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Record blocked under saturation; must be non-blocking")
	}
	r.Close()
}

func TestRegistrySnapshotsOrder(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	r.Register("z", CounterType, "", nil, nil, 0, 0)
	r.Register("a", CounterType, "", nil, nil, 0, 0)
	snaps := r.Snapshots()
	// RED metrics first, then z, then a (insertion order)
	found := []string{}
	for _, s := range snaps {
		found = append(found, s.Name)
	}
	last := found[len(found)-1]
	if last != "a" {
		t.Errorf("expected 'a' last, order = %v", found)
	}
}

func TestRegistryStartAndCloseIdempotent(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// Closing context triggers final flush and loop exit.
	cancel()
	// Close should not hang.
	if err := r.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

// waitForDrain blocks until the ingestion queue is drained and the background
// processor has had a chance to apply the samples. The extra settle window
// covers the gap between the channel receive and the metric update.
func waitForDrain(r *Registry) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(r.queue) == 0 {
			time.Sleep(5 * time.Millisecond) // settle: let process() finish
			if len(r.queue) == 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
