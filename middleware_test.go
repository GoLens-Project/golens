package golens

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func startTestRegistry(t *testing.T) (*Registry, func()) {
	t.Helper()
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	cleanup := func() {
		cancel()
		r.Close()
	}
	return r, cleanup
}

func TestMiddlewareRecordsRequestCount(t *testing.T) {
	r, cleanup := startTestRegistry(t)
	defer cleanup()

	h := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/orders", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	waitForDrain(r)

	s, _ := r.Snapshot("http_requests_total")
	if s.Value != 1 {
		t.Errorf("requests_total = %v, want 1", s.Value)
	}
}

func TestMiddlewareRecordsErrors(t *testing.T) {
	r, cleanup := startTestRegistry(t)
	defer cleanup()

	h := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(500)
	}))

	req := httptest.NewRequest("GET", "/fail", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	waitForDrain(r)

	errs, _ := r.Snapshot("http_request_errors_total")
	if errs.Value != 1 {
		t.Errorf("errors_total = %v, want 1", errs.Value)
	}
}

func TestMiddlewareRecordsDuration(t *testing.T) {
	r, cleanup := startTestRegistry(t)
	defer cleanup()

	h := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/slow", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	waitForDrain(r)

	dur, _ := r.Snapshot("http_request_duration_seconds")
	if dur.Count != 1 {
		t.Errorf("duration count = %d, want 1", dur.Count)
	}
	if dur.Sum <= 0 {
		t.Errorf("duration sum = %v, want > 0", dur.Sum)
	}
}

func TestMiddlewareExcludesPaths(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExcludePatterns = []string{"^/health$"}
	r := newTestRegistry(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	h := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	waitForDrain(r)

	s, _ := r.Snapshot("http_requests_total")
	if s.Value != 0 {
		t.Errorf("excluded path recorded: value = %v", s.Value)
	}
}

func TestMiddlewarePassesThroughBody(t *testing.T) {
	r, cleanup := startTestRegistry(t)
	defer cleanup()

	h := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("POST", "/create", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestMiddlewareFuncEquivalent(t *testing.T) {
	r, cleanup := startTestRegistry(t)
	defer cleanup()

	h := r.MiddlewareFunc(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/x", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	waitForDrain(r)

	s, _ := r.Snapshot("http_requests_total")
	if s.Value != 1 {
		t.Errorf("MiddlewareFunc did not record: %v", s.Value)
	}
}
