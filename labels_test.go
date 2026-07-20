package golens

import (
	"context"
	"strings"
	"testing"
)

func TestSanitizeLabelValueNormalizePaths(t *testing.T) {
	cfg := LabelsConfig{NormalizePaths: true}
	cases := map[string]string{
		"/users/123":       "/users/:id",
		"/users/abc":       "/users/abc",
		"/order/abc123def": "/order/:id",
		"/static/app.js":   "/static/app.js",
	}
	for in, want := range cases {
		got := SanitizeLabelValue("path", in, cfg)
		if got != want {
			t.Errorf("SanitizeLabelValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeLabelValueNormalizePathsDisabled(t *testing.T) {
	cfg := LabelsConfig{NormalizePaths: false}
	got := SanitizeLabelValue("path", "/users/123", cfg)
	if got != "/users/123" {
		t.Errorf("expected no normalization, got %q", got)
	}
}

func TestSanitizeLabelValueMaxLength(t *testing.T) {
	cfg := LabelsConfig{MaxLength: 5}
	got := SanitizeLabelValue("key", "abcdefghij", cfg)
	if got != "abcde" {
		t.Errorf("SanitizeLabelValue with MaxLength=5: got %q, want %q", got, "abcde")
	}
}

func TestSanitizeLabelValueMaxLengthZero(t *testing.T) {
	cfg := LabelsConfig{MaxLength: 0}
	got := SanitizeLabelValue("key", "abcdefghij", cfg)
	if got != "abcdefghij" {
		t.Errorf("SanitizeLabelValue with MaxLength=0: got %q, want %q", got, "abcdefghij")
	}
}

func TestSanitizeLabelValueEmpty(t *testing.T) {
	cfg := LabelsConfig{NormalizePaths: true, MaxLength: 5}
	got := SanitizeLabelValue("path", "", cfg)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestSanitizeLabelValueNonPathLabel(t *testing.T) {
	cfg := LabelsConfig{NormalizePaths: true}
	got := SanitizeLabelValue("method", "/users/123", cfg)
	// "method" is not "path", so normalization should not apply
	if got != "/users/123" {
		t.Errorf("expected no normalization for non-path label, got %q", got)
	}
}

func TestSanitizeLabels(t *testing.T) {
	cfg := LabelsConfig{NormalizePaths: true, MaxLength: 10}
	labels := []Label{
		{Name: "path", Value: "/users/12345678"},
		{Name: "method", Value: "GET"},
		{Name: "key", Value: "very-long-value-that-exceeds-limit"},
	}
	result := SanitizeLabels(labels, cfg)
	if len(result) != 3 {
		t.Fatalf("expected 3 labels, got %d", len(result))
	}
	if result[0].Value != "/users/:id" {
		t.Errorf("path label: got %q, want %q", result[0].Value, "/users/:id")
	}
	if result[1].Value != "GET" {
		t.Errorf("method label: got %q, want %q", result[1].Value, "GET")
	}
	if result[2].Value != "very-long-" {
		t.Errorf("truncated label: got %q, want %q", result[2].Value, "very-long-")
	}
}

func TestSanitizeLabelsFastPath(t *testing.T) {
	// When no sanitization is configured, returns the original slice
	cfg := LabelsConfig{}
	labels := []Label{{Name: "k", Value: "v"}}
	result := SanitizeLabels(labels, cfg)
	if len(result) != 1 || result[0].Value != "v" {
		t.Errorf("fast path failed: got %+v", result)
	}
}

// --- cardinalityTracker tests ---

func TestCardinalityTrackerAllow(t *testing.T) {
	ct := newCardinalityTracker(3, false)

	labels1 := []Label{{Name: "tenant", Value: "a"}}
	labels2 := []Label{{Name: "tenant", Value: "b"}}
	labels3 := []Label{{Name: "tenant", Value: "c"}}
	labels4 := []Label{{Name: "tenant", Value: "d"}}

	if !ct.Allow("metric", labels1) {
		t.Error("first combo should be allowed")
	}
	if !ct.Allow("metric", labels2) {
		t.Error("second combo should be allowed")
	}
	if !ct.Allow("metric", labels3) {
		t.Error("third combo should be allowed")
	}
	if ct.Allow("metric", labels4) {
		t.Error("fourth combo should be dropped (cap=3)")
	}
}

func TestCardinalityTrackerAllowDuplicate(t *testing.T) {
	ct := newCardinalityTracker(2, false)
	labels := []Label{{Name: "k", Value: "v"}}

	if !ct.Allow("m", labels) {
		t.Error("first should be allowed")
	}
	if !ct.Allow("m", labels) {
		t.Error("duplicate should be allowed (already seen)")
	}
}

func TestCardinalityTrackerUnlimited(t *testing.T) {
	ct := newCardinalityTracker(0, false) // 0 = unlimited
	for i := 0; i < 1000; i++ {
		labels := []Label{{Name: "i", Value: string(rune('a' + i%26)) + string(rune('0'+i/26))}}
		if !ct.Allow("m", labels) {
			t.Errorf("combo %d should be allowed (unlimited)", i)
		}
	}
}

func TestCardinalityTrackerSeparateMetrics(t *testing.T) {
	ct := newCardinalityTracker(2, false)
	labels := []Label{{Name: "k", Value: "v"}}

	if !ct.Allow("m1", labels) {
		t.Error("m1 should allow")
	}
	if !ct.Allow("m2", labels) {
		t.Error("m2 should allow (separate series)")
	}
}

func TestCardinalityTrackerDroppedCount(t *testing.T) {
	ct := newCardinalityTracker(1, false)
	ct.Allow("m", []Label{{Name: "k", Value: "a"}})
	ct.Allow("m", []Label{{Name: "k", Value: "b"}}) // dropped
	ct.Allow("m", []Label{{Name: "k", Value: "c"}}) // dropped

	if ct.DroppedCount("m") != 2 {
		t.Errorf("dropped = %d, want 2", ct.DroppedCount("m"))
	}
	if ct.DroppedCount("other") != 0 {
		t.Errorf("dropped for unknown metric = %d, want 0", ct.DroppedCount("other"))
	}
}

func TestCardinalityTrackerSeriesCount(t *testing.T) {
	ct := newCardinalityTracker(10, false)
	ct.Allow("m", []Label{{Name: "k", Value: "a"}})
	ct.Allow("m", []Label{{Name: "k", Value: "b"}})
	ct.Allow("m", []Label{{Name: "k", Value: "a"}}) // duplicate

	if ct.SeriesCount("m") != 2 {
		t.Errorf("series = %d, want 2", ct.SeriesCount("m"))
	}
}

func TestCardinalityTrackerAllSeriesCounts(t *testing.T) {
	ct := newCardinalityTracker(10, false)
	ct.Allow("m1", []Label{{Name: "k", Value: "a"}})
	ct.Allow("m1", []Label{{Name: "k", Value: "b"}})
	ct.Allow("m2", []Label{{Name: "k", Value: "x"}})

	counts := ct.AllSeriesCounts()
	if counts["m1"] != 2 {
		t.Errorf("m1 series = %d, want 2", counts["m1"])
	}
	if counts["m2"] != 1 {
		t.Errorf("m2 series = %d, want 1", counts["m2"])
	}
}

func TestCardinalityTrackerAllDroppedCounts(t *testing.T) {
	ct := newCardinalityTracker(1, false)
	ct.Allow("m", []Label{{Name: "k", Value: "a"}})
	ct.Allow("m", []Label{{Name: "k", Value: "b"}})
	ct.Allow("m", []Label{{Name: "k", Value: "c"}})

	dropped := ct.AllDroppedCounts()
	if dropped["m"] != 2 {
		t.Errorf("m dropped = %d, want 2", dropped["m"])
	}
}

func TestFingerprintLabels(t *testing.T) {
	labels := []Label{
		{Name: "a", Value: "1"},
		{Name: "b", Value: "2"},
	}
	fp := fingerprintLabels(labels)
	if !strings.Contains(fp, "a=1") || !strings.Contains(fp, "b=2") {
		t.Errorf("fingerprint %q missing expected content", fp)
	}
}

func TestFingerprintLabelsEmpty(t *testing.T) {
	fp := fingerprintLabels(nil)
	if fp != "" {
		t.Errorf("expected empty fingerprint, got %q", fp)
	}
}

func TestFormatCardinalityValue(t *testing.T) {
	cases := map[int]string{
		0:       "0",
		42:      "42",
		999:     "999",
		1000:    "1.0k",
		1500:    "1.5k",
		999999:  "1000.0k",
		1000000: "1.0M",
		2500000: "2.5M",
	}
	for in, want := range cases {
		got := formatCardinalityValue(in)
		if got != want {
			t.Errorf("formatCardinalityValue(%d) = %q, want %q", in, got, want)
		}
	}
}

// --- Integration: cardinality through the Registry ---

func TestRegistryCardinalityGuardDropsExcess(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxLabelSeriesPerMetric = 2
	cfg.Debug = true
	r := newTestRegistry(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	// Record 3 distinct label combos for the same metric
	r.Record("test_metric", 1, Label{Name: "k", Value: "a"})
	r.Record("test_metric", 2, Label{Name: "k", Value: "b"})
	r.Record("test_metric", 3, Label{Name: "k", Value: "c"}) // should be dropped
	waitForDrain(r)

	s, ok := r.Snapshot("test_metric")
	if !ok {
		t.Fatal("metric not found")
	}
	// Only 2 of the 3 should have been recorded
	if s.Value != 3 {
		t.Errorf("value = %v, want 3 (a=1 + b=2, c dropped)", s.Value)
	}

	snapshots := r.CardinalitySnapshots()
	found := false
	for _, cs := range snapshots {
		if cs.MetricName == "test_metric" {
			found = true
			if cs.Series != 2 {
				t.Errorf("series = %d, want 2", cs.Series)
			}
			if cs.Dropped != 1 {
				t.Errorf("dropped = %d, want 1", cs.Dropped)
			}
			if cs.MaxSeries != 2 {
				t.Errorf("max = %d, want 2", cs.MaxSeries)
			}
		}
	}
	if !found {
		t.Error("test_metric not found in cardinality snapshots")
	}
	r.Close()
}

func TestRegistryCardinalityGuardUnlimited(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxLabelSeriesPerMetric = 0 // unlimited
	r := newTestRegistry(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	for i := 0; i < 100; i++ {
		r.Record("unlimited_metric", float64(i), Label{Name: "i", Value: string(rune('a' + i%26))})
	}
	waitForDrain(r)

	snaps := r.CardinalitySnapshots()
	for _, cs := range snaps {
		if cs.MetricName == "unlimited_metric" {
			if cs.Dropped != 0 {
				t.Errorf("dropped = %d, want 0 (unlimited)", cs.Dropped)
			}
		}
	}
	r.Close()
}

func TestRegistryCardinalityGuardNoLabels(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxLabelSeriesPerMetric = 1
	r := newTestRegistry(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	// Samples without labels should always pass (no cardinality check)
	r.Record("no_labels", 1)
	r.Record("no_labels", 2)
	r.Record("no_labels", 3)
	waitForDrain(r)

	s, ok := r.Snapshot("no_labels")
	if !ok {
		t.Fatal("metric not found")
	}
	if s.Value != 6 {
		t.Errorf("value = %v, want 6 (all recorded, no labels)", s.Value)
	}
	r.Close()
}

func TestRegistrySanitizeLabelsOnIngest(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Labels.NormalizePaths = true
	cfg.Labels.MaxLength = 10
	r := newTestRegistry(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	r.Record("path_test", 1, Label{Name: "path", Value: "/users/1234567890"})
	waitForDrain(r)

	s, ok := r.Snapshot("path_test")
	if !ok {
		t.Fatal("metric not found")
	}
	// Path should be normalized and truncated
	if len(s.LabelValues) != 1 {
		t.Fatalf("expected 1 label, got %d", len(s.LabelValues))
	}
	if s.LabelValues[0].Value != "/users/:id" {
		t.Errorf("label value = %q, want %q", s.LabelValues[0].Value, "/users/:id")
	}
	r.Close()
}

func TestCardinalitySnapshotsEmpty(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	snaps := r.CardinalitySnapshots()
	if len(snaps) != 0 {
		t.Errorf("expected empty snapshots, got %d", len(snaps))
	}
}

func TestCardinalityTrackerConcurrent(t *testing.T) {
	ct := newCardinalityTracker(100, false)
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(n int) {
			for j := 0; j < 100; j++ {
				labels := []Label{{Name: "g", Value: string(rune('a'+n)) + string(rune('0'+j%10))}}
				ct.Allow("concurrent_metric", labels)
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	// Should not panic; series count should be bounded
	counts := ct.AllSeriesCounts()
	if counts["concurrent_metric"] > 100 {
		t.Errorf("series = %d, want <= 100", counts["concurrent_metric"])
	}
}
