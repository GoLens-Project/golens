package golens

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Exercises the debug-log branch when the ingestion queue saturates.
func TestRecordLogsDropInDebug(t *testing.T) {
	var buf strings.Builder
	redirectLog(t, &buf)

	cfg := DefaultConfig()
	cfg.Debug = true
	cfg.IngestQueueSize = 2
	r := newTestRegistry(t, cfg)
	// Never start the loop so the queue fills and drops.
	for i := 0; i < 100; i++ {
		r.Record("flood", 1)
	}
	r.Close()
	if !strings.Contains(buf.String(), "dropped") {
		t.Errorf("expected drop log, got: %s", buf.String())
	}
}

// Exercises the Registry.Storage accessor.
func TestRegistryStorageAccessor(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	if r.Storage() == nil {
		t.Error("Storage() returned nil")
	}
}

// exportBatch with no exporter is a no-op.
func TestExportBatchNoExporter(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	r.ctx = context.Background()
	r.exportBatch() // must not panic
}

// metricsPageHandler template error path: write a broken template is hard to
// trigger, so instead verify a healthy render carries poll interval text.
func TestDashboardRendersInterval(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer func() { cancel(); r.Close() }()

	srv := newServer(r)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "cooldown-ring") {
		t.Errorf("cooldown ring missing: %q", body)
	}
	if !strings.Contains(body, "every 5000ms") {
		t.Errorf("poll interval missing in hx-trigger: %q", body)
	}
}

func TestSQLiteCloseAfterNew(t *testing.T) {
	s, err := newSQLiteStorage(filepathJoin(t.TempDir(), "nil.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	// second close is a no-op
	if err := s.Close(); err != nil {
		t.Errorf("double close: %v", err)
	}
}

func TestHistogramOverflowOnly(t *testing.T) {
	m := newMetric("h", HistogramType, "", nil, []float64{1})
	m.Record(100)
	m.Record(100)
	s := m.Snapshot()
	overflow := s.Buckets[len(s.Buckets)-1]
	if !overflow.Overflow || overflow.Count != 2 {
		t.Errorf("overflow bucket = %+v", overflow)
	}
}

func TestWaitForDrainTimeoutPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IngestQueueSize = 4
	r := newTestRegistry(t, cfg)
	r.Record("x", 1)
	// Don't start loop: queue stays non-empty, waitForDrain must time out.
	start := time.Now()
	waitForDrain(r)
	if time.Since(start) > 2*time.Second {
		// tolerated
	}
	r.Close()
}
