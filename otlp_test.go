package golens

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOTLPExporterPostsJSON(t *testing.T) {
	var received otlpMetricsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if ct := req.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		body, _ := io.ReadAll(req.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := OTLPConfig{
		Enabled: true, Endpoint: srv.URL, Protocol: "http",
		BatchSize: 10, Interval: time.Second, Timeout: time.Second,
	}
	exp := newOTLPExporter(cfg)

	snaps := []MetricSnapshot{
		{Name: "c", Type: "counter", Value: 5, UpdatedAt: 1},
		{Name: "g", Type: "gauge", Value: 3.5, UpdatedAt: 2},
		{Name: "h", Type: "histogram", Count: 3, Sum: 9, UpdatedAt: 3,
			Buckets: []BucketSnapshot{{UpperBound: 1, Count: 2}, {Overflow: true, Count: 1}}},
	}
	if err := exp.export(context.Background(), snaps); err != nil {
		t.Fatalf("export: %v", err)
	}

	rm := received.ResourceMetrics
	if len(rm) != 1 || len(rm[0].ScopeMetrics) != 1 {
		t.Fatalf("unexpected structure: %+v", received)
	}
	metrics := rm[0].ScopeMetrics[0].Metrics
	if len(metrics) != 3 {
		t.Fatalf("metrics = %d, want 3", len(metrics))
	}
	if metrics[0].Sum == nil || metrics[0].Sum.DataPoints[0].AsDouble != 5 {
		t.Errorf("counter not encoded: %+v", metrics[0])
	}
	if metrics[1].Gauge == nil {
		t.Errorf("gauge not encoded: %+v", metrics[1])
	}
	if metrics[2].Histogram == nil {
		t.Errorf("histogram not encoded: %+v", metrics[2])
	}
	hp := metrics[2].Histogram.DataPoints[0]
	if hp.Count != 3 || hp.Sum != 9 {
		t.Errorf("histogram point = %+v", hp)
	}
	if len(hp.BucketCounts) != 2 {
		t.Errorf("bucket counts = %v", hp.BucketCounts)
	}
}

func TestOTLPExporterHandlesErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	cfg := OTLPConfig{Enabled: true, Endpoint: srv.URL, Protocol: "http",
		BatchSize: 1, Interval: time.Second, Timeout: time.Second}
	exp := newOTLPExporter(cfg)
	err := exp.export(context.Background(), []MetricSnapshot{{Name: "x", Type: "counter", Value: 1}})
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestOTLPExporterContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := OTLPConfig{Enabled: true, Endpoint: srv.URL, Protocol: "http",
		BatchSize: 1, Interval: time.Second, Timeout: 5 * time.Second}
	exp := newOTLPExporter(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := exp.export(ctx, []MetricSnapshot{{Name: "x", Type: "counter", Value: 1}})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestRegistryExportBatchPushesToExporter(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.OTLP.Enabled = true
	cfg.OTLP.Endpoint = srv.URL
	cfg.OTLP.Interval = 10 * time.Millisecond
	cfg.FlushInterval = 10 * time.Millisecond
	r := newTestRegistry(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)

	r.Record("http_requests_total", 1)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && hits.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	r.Close()
	if hits.Load() == 0 {
		t.Fatal("exporter never received a batch")
	}
}
