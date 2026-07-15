package golens

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"/":                "/",
		"":                 "/",
		"/users":           "/users",
		"/users/123":       "/users/:id",
		"/users/123/posts": "/users/:id/posts",
		"/users/abc":       "/users/abc",
		"/u/550e8400-e29b-41d4-a716-446655440000": "/u/:id",
		"/order/abc123def456":                     "/order/:id", // 12+ hex → :id
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEndpointTrackerPercentiles(t *testing.T) {
	tr := newEndpointTracker(64)
	// 10 samples of 0.001s, 10 of 0.1s → p50 ~ small, p95/p99 ~ 0.1
	for i := 0; i < 10; i++ {
		tr.Observe("GET", "/fast", 0.001)
	}
	for i := 0; i < 10; i++ {
		tr.Observe("GET", "/fast", 0.1)
	}
	snaps := tr.Snapshots()
	if len(snaps) != 1 {
		t.Fatalf("want 1 endpoint, got %d", len(snaps))
	}
	s := snaps[0]
	if s.Count != 20 {
		t.Errorf("count = %d, want 20", s.Count)
	}
	if s.P50 > 0.05 {
		t.Errorf("p50 = %v, want <= 0.05 (half are 0.001)", s.P50)
	}
	if s.P95 < 0.05 {
		t.Errorf("p95 = %v, want >= 0.05 (top decile is 0.1)", s.P95)
	}
	if s.P99 < 0.05 {
		t.Errorf("p99 = %v, want >= 0.05", s.P99)
	}
}

func TestEndpointTrackerPerEndpoint(t *testing.T) {
	tr := newEndpointTracker(64)
	tr.Observe("GET", "/a", 0.01)
	tr.Observe("POST", "/a", 0.02)
	tr.Observe("GET", "/b", 0.5)
	snaps := tr.Snapshots()
	// 3 distinct (method,path) combos
	if len(snaps) != 3 {
		t.Fatalf("want 3 endpoints, got %d (%+v)", len(snaps), snaps)
	}
}

func TestEndpointTrackerCardinalityBound(t *testing.T) {
	tr := newEndpointTracker(2)
	tr.Observe("GET", "/a", 0.01)
	tr.Observe("GET", "/b", 0.01)
	tr.Observe("GET", "/c", 0.01) // dropped: bound reached
	tr.Observe("GET", "/a", 0.01) // still recorded into existing
	if len(tr.Snapshots()) != 2 {
		t.Errorf("want 2 endpoints after bound, got %d", len(tr.Snapshots()))
	}
}

func TestEndpointTrackerCollapsesIds(t *testing.T) {
	tr := newEndpointTracker(64)
	tr.Observe("GET", "/users/1", 0.01)
	tr.Observe("GET", "/users/2", 0.01)
	tr.Observe("GET", "/users/999", 0.01)
	snaps := tr.Snapshots()
	if len(snaps) != 1 || snaps[0].Path != "/users/:id" {
		t.Errorf("ids not collapsed: %+v", snaps)
	}
	if snaps[0].Count != 3 {
		t.Errorf("count = %d, want 3", snaps[0].Count)
	}
}

func TestEndpointPercentileEmpty(t *testing.T) {
	s := &latencySeries{counts: make([]int64, len(endpointBounds)+1)}
	// no samples → percentile 0
	if got := s.percentile(endpointBounds, 0.95); got != 0 {
		t.Errorf("empty percentile = %v, want 0", got)
	}
}

func TestRegistryEndpointLatencyViaMiddleware(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	h := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(200)
	}))
	for i := 0; i < 5; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/orders", nil))
	}
	waitForDrain(r)

	snaps := r.EndpointLatency()
	if len(snaps) != 1 || snaps[0].Path != "/orders" || snaps[0].Count != 5 {
		t.Errorf("endpoint latency = %+v", snaps)
	}
}

func TestEndpointsHTTPHandler(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	h := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	waitForDrain(r)

	mux := http.NewServeMux()
	r.MountUI(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics/endpoints", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/x") {
		t.Errorf("endpoint missing in response: %s", body)
	}
}

func TestEndpointTrackerReset(t *testing.T) {
	tr := newEndpointTracker(64)
	tr.Observe("GET", "/a", 0.01)
	tr.Observe("POST", "/b", 0.02)
	if len(tr.Snapshots()) != 2 {
		t.Fatalf("pre-reset: want 2 endpoints, got %d", len(tr.Snapshots()))
	}

	tr.Reset()
	if len(tr.Snapshots()) != 0 {
		t.Errorf("post-reset: want 0 endpoints, got %d", len(tr.Snapshots()))
	}
}
