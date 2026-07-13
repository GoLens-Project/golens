package golens

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Memory storage with a TTL evicts buckets whose WindowEnd is older than ttl.
func TestMemoryStorageTTLEvicts(t *testing.T) {
	s := newMemoryStorage(time.Millisecond)
	ctx := context.Background()

	old := AggregatedMetric{Name: "old", WindowStart: time.Unix(0, 0), WindowEnd: time.Unix(0, 0)}
	s.Store(ctx, old)

	// Wait until "old" is beyond the TTL cutoff.
	time.Sleep(10 * time.Millisecond)
	s.Store(ctx, AggregatedMetric{Name: "new", WindowStart: time.Now(), WindowEnd: time.Now().Add(time.Second)})

	res, _ := s.Query(ctx, Query{})
	for _, b := range res {
		if b.Name == "old" {
			t.Error("stale bucket was not evicted by TTL")
		}
	}
}

// Labels passed to Record are surfaced on the snapshot (last-seen).
func TestRegistryRecordSurfacesLabels(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	r.Record("http_requests_total", 1,
		Label{Name: "method", Value: "GET"},
		Label{Name: "path", Value: "/x"},
		Label{Name: "status", Value: "200"},
	)
	waitForDrain(r)

	s, _ := r.Snapshot("http_requests_total")
	got := map[string]string{}
	for _, l := range s.LabelValues {
		got[l.Name] = l.Value
	}
	if got["method"] != "GET" || got["path"] != "/x" || got["status"] != "200" {
		t.Errorf("label values not surfaced: %+v", got)
	}
}

// Handler must start the background loop so recorded metrics are not dropped.
func TestHandlerStartsRegistry(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) })
	r, h := Handler(DefaultConfig(), inner)
	defer r.Close()

	// Drive a few requests; without Start the queue would saturate and drop.
	for i := 0; i < 50; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}
	waitForDrain(r)

	s, _ := r.Snapshot("http_requests_total")
	if s.Value != 50 {
		t.Errorf("Handler did not start loop; requests_total = %v, want 50", s.Value)
	}
}

// Close is safe to call concurrently (no close-of-closed-channel panic).
func TestCloseConcurrentSafe(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Close()
		}()
	}
	cancel()
	wg.Wait()
}

// Close without Start does a one-shot flush and does not hang.
func TestCloseWithoutStart(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	done := make(chan struct{})
	go func() {
		_ = r.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close without Start hung")
	}
}
