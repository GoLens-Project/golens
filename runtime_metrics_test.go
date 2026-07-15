package golens

import (
	"context"
	"testing"
	"time"
)

func TestRuntimeMetricsRegistersWhenEnabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RuntimeMetrics.Enabled = true
	cfg.RuntimeMetrics.Interval = 10 * time.Millisecond
	r := newTestRegistry(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	// Wait for two collection cycles so CPU delta is computed.
	time.Sleep(80 * time.Millisecond)

	metrics := []string{
		MetricGoAllocBytes,
		MetricGoSysBytes,
		MetricGoHeapInuse,
		MetricGoHeapObjects,
		MetricGoGoroutines,
	}
	for _, name := range metrics {
		snap, ok := r.Snapshot(name)
		if !ok {
			t.Errorf("metric %q not registered", name)
			continue
		}
		if snap.Value <= 0 {
			t.Errorf("metric %q = %f, want > 0", name, snap.Value)
		}
	}

	// CPU metric may be 0 on first cycle, just verify it exists.
	if _, ok := r.Snapshot(MetricCPUUsagePercent); !ok {
		t.Errorf("metric %q not registered", MetricCPUUsagePercent)
	}
}

func TestRuntimeMetricsNotRegisteredWhenDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RuntimeMetrics.Enabled = false
	r := newTestRegistry(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	time.Sleep(20 * time.Millisecond)

	for _, name := range []string{
		MetricGoAllocBytes,
		MetricGoSysBytes,
		MetricGoHeapInuse,
		MetricGoHeapObjects,
		MetricGoGoroutines,
		MetricCPUUsagePercent,
	} {
		if _, ok := r.Snapshot(name); ok {
			t.Errorf("metric %q should not be registered when disabled", name)
		}
	}
}
