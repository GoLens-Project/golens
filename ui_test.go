package golens

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMountUIRegistersRoutes(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	mux := http.NewServeMux()
	r.MountUI(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "GoLens") || !strings.Contains(body, "discovery dashboard") {
		t.Error("dashboard shell not rendered")
	}
	if !strings.Contains(body, "alpinejs") {
		t.Error("Alpine.js not included")
	}
	if !strings.Contains(body, "tailwindcss") {
		t.Error("Tailwind not included")
	}
	if !strings.Contains(body, "hx-trigger") {
		t.Error("HTMX polling trigger missing")
	}
	if !strings.Contains(body, "grid-cols-1") {
		t.Error("responsive grid missing")
	}
	if !strings.Contains(body, "COUNTER") || !strings.Contains(body, "HISTOGRAM") {
		t.Error("type badges not rendered")
	}
}

func TestMountUIDisabledDoesNothing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UI.Enabled = false
	r := newTestRegistry(t, cfg)
	mux := http.NewServeMux()
	r.MountUI(mux)
	// No routes registered -> default 404
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("UI disabled should not register routes; got %d", rec.Code)
	}
}

func TestMetricsDataJSON(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	r.Record("http_requests_total", 7)
	waitForDrain(r)

	mux := http.NewServeMux()
	r.MountUI(mux)

	req := httptest.NewRequest("GET", "/metrics/data", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}
	var snaps []MetricSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snaps); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, s := range snaps {
		if s.Name == "http_requests_total" && s.Value == 7 {
			found = true
		}
	}
	if !found {
		t.Errorf("snapshot not in JSON output: %+v", snaps)
	}
}

func TestMetricsDataHTMLFragment(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	mux := http.NewServeMux()
	r.MountUI(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics/data", nil))
	if !strings.Contains(rec.Body.String(), `data-metric="http_requests_total"`) {
		t.Errorf("HTML fragment missing metric card: %q", rec.Body.String())
	}
}

func TestMetricsDataSingleMetric(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	mux := http.NewServeMux()
	r.MountUI(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics/data?metric=http_requests_total", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var s MetricSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Name != "http_requests_total" {
		t.Errorf("name = %q", s.Name)
	}
}

func TestMetricsDataUnknownMetric404(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	mux := http.NewServeMux()
	r.MountUI(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics/data?metric=nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestFormatFloat(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{1.23456789, "1.23"},
		{50, "50"},
		{0.5, "0.50"},
		{42, "42"},
	}
	for _, c := range cases {
		if got := formatFloat(c.in); got != c.want {
			t.Errorf("formatFloat(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFmtBound(t *testing.T) {
	cases := map[float64]string{
		0.005: "0.005",
		0.01:  "0.01",
		0.025: "0.025",
		0.1:   "0.1",
		1:     "1",
		2.5:   "2.5",
		10:    "10",
	}
	for in, want := range cases {
		if got := fmtBound(in); got != want {
			t.Errorf("fmtBound(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestWantsJSON(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept", "application/json")
	if !wantsJSON(req) {
		t.Error("application/json Accept should be JSON")
	}
	req2 := httptest.NewRequest("GET", "/?format=json", nil)
	if !wantsJSON(req2) {
		t.Error("?format=json should be JSON")
	}
	req3 := httptest.NewRequest("GET", "/", nil)
	if wantsJSON(req3) {
		t.Error("plain request should not be JSON")
	}
}
