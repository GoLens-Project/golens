package golens

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFluentHookRecordsCustomMetric(t *testing.T) {
	r, cleanup := startTestRegistry(t)
	defer cleanup()

	mw := r.On("user_signup_event").
		Type(CounterType).
		Description("user signups").
		Labels("plan").
		Extract(func(req *http.Request) (float64, []Label) {
			return 1, []Label{{Name: "plan", Value: req.URL.Query().Get("plan")}}
		})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("POST", "/signup?plan=pro", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	waitForDrain(r)

	s, ok := r.Snapshot("user_signup_event")
	if !ok {
		t.Fatal("custom metric not registered")
	}
	if s.Value != 1 {
		t.Errorf("custom metric value = %v, want 1", s.Value)
	}
	if s.Description != "user signups" {
		t.Errorf("description = %q", s.Description)
	}
}

func TestFluentHookHistogramWithBounds(t *testing.T) {
	r, cleanup := startTestRegistry(t)
	defer cleanup()

	mw := r.On("order_value").
		Type(HistogramType).
		Bounds(10, 50, 100).
		Extract(func(req *http.Request) (float64, []Label) {
			return 42, nil
		})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest("GET", "/order", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	waitForDrain(r)

	s, _ := r.Snapshot("order_value")
	if s.Count != 1 || s.Sum != 42 {
		t.Errorf("histogram = count %d sum %v", s.Count, s.Sum)
	}
}

func TestFluentHookSkipsExcluded(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExcludePatterns = []string{"^/skip$"}
	r := newTestRegistry(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	mw := r.On("m").Extract(func(*http.Request) (float64, []Label) { return 1, nil })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }))

	req := httptest.NewRequest("GET", "/skip", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	waitForDrain(r)

	if s, _ := r.Snapshot("m"); s.Value != 0 {
		t.Errorf("excluded hook recorded: %v", s.Value)
	}
}

func TestRequestCountMiddleware(t *testing.T) {
	r, cleanup := startTestRegistry(t)
	defer cleanup()

	h := r.RequestCountMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }))
	for i := 0; i < 3; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	}
	waitForDrain(r)
	s, _ := r.Snapshot("http_requests_total")
	if s.Value != 3 {
		t.Errorf("count = %v, want 3", s.Value)
	}
}

func TestLatencyMiddleware(t *testing.T) {
	r, cleanup := startTestRegistry(t)
	defer cleanup()

	h := r.LatencyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	waitForDrain(r)
	s, _ := r.Snapshot("http_request_duration_seconds")
	if s.Count != 1 {
		t.Errorf("count = %d, want 1", s.Count)
	}
}

func TestErrorRateMiddleware(t *testing.T) {
	r, cleanup := startTestRegistry(t)
	defer cleanup()

	h := r.ErrorRateMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(404) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	waitForDrain(r)
	s, _ := r.Snapshot("http_request_errors_total")
	if s.Value != 1 {
		t.Errorf("errors = %v, want 1", s.Value)
	}
}
