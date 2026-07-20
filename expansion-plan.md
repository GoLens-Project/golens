# GoLens Expansion Plan: Cardinality Management

## Status: Implemented

All core features have been implemented and tested.

---

## What was implemented

### 1. Label normalization (`labels.go` — new file)

- Extracted `normalizePath()` and `idSegment()` from `endpoint_latency.go` into a shared utility.
- Added `SanitizeLabelValue()` and `SanitizeLabels()` — apply path normalization (configurable) and max-length truncation to label values.
- Added `cardinalityTracker` — tracks unique label combinations per metric name, drops samples when the cap is exceeded.
- Added `CardinalitySnapshot` struct for the dashboard.
- Added `fingerprintLabels()` for deterministic label deduplication.

### 2. Config additions (`config.go`)

- Added `LabelsConfig` struct with `NormalizePaths` (default: `true`) and `MaxLength` (default: `0` = unlimited).
- Added `MaxLabelSeriesPerMetric` to `Config` (default: `256`, `0` = unlimited).
- YAML support: `labels.normalize_paths`, `labels.max_length`, `max_label_series_per_metric`.

### 3. Registry integration (`registry.go`)

- Added `cardinality *cardinalityTracker` field to `Registry`.
- Initialized in `New()` with configured cap.
- `process()` now calls `SanitizeLabels()` before recording and checks the cardinality guard — drops samples when the cap is exceeded.
- Added `CardinalitySnapshots()` method for the dashboard.

### 4. HTMX dashboard (`ui.go`)

- Added `/metrics/cardinality` JSON endpoint.
- Added `CardinalityHTTPHandler()` public method.
- Added "GoLens Internals" collapsible section to the dashboard with:
  - Summary cards: total series, total dropped, series cap
  - Per-metric detail table: metric name, series count, cap, dropped count, usage bar
  - Color-coded usage bars (green < 70%, amber 70-90%, red ≥ 90%)
  - Auto-polls when open

### 5. Examples

- Added `/tenant` endpoint to both `stdlibmux` and `gin` examples — demonstrates cardinality-bounded labels.
- Mounted `/metrics/cardinality` in the gin example.

### 6. Documentation (`README.md`)

- Updated YAML config example with new fields.
- Added "Cardinality management" section.
- Updated Core Features to mention the new safety mechanisms.
- Updated Dashboard section to mention GoLens Internals.

---

## Files changed

| File | Change |
|---|---|
| `labels.go` | **New** — shared normalization, sanitization, cardinality tracker |
| `config.go` | Added `LabelsConfig`, `MaxLabelSeriesPerMetric`, defaults |
| `registry.go` | Added cardinality tracker, `CardinalitySnapshots()`, sanitization in `process()` |
| `endpoint_latency.go` | Removed duplicate `idSegment`/`normalizePath` (now in `labels.go`) |
| `ui.go` | Added `/metrics/cardinality` endpoint, `CardinalityHTTPHandler`, GoLens Internals dashboard section |
| `examples/api/stdlibmux/main.go` | Added `/tenant` cardinality demo endpoint |
| `examples/api/gin/main.go` | Added `/tenant` cardinality demo endpoint, mounted `/metrics/cardinality` |
| `README.md` | Documented new config fields, cardinality management, dashboard section |

---

## How to use

### Config

```yaml
labels:
  normalize_paths: true   # /users/123 → /users/:id (default: true)
  max_length: 64          # truncate long values (0 = no limit)

max_label_series_per_metric: 256  # 0 = unlimited
```

### Code

```go
cfg := golens.DefaultConfig()
cfg.Labels.NormalizePaths = true
cfg.Labels.MaxLength = 64
cfg.MaxLabelSeriesPerMetric = 256
```

### Dashboard

Open `/metrics` and expand the **GoLens Internals** section to see per-metric cardinality stats.

### Demo

```bash
make run-stdlib
# then:
curl http://localhost:8080/tenant?tenant=acme
curl http://localhost:8080/tenant?tenant=beta
# visit http://localhost:8080/metrics → expand "GoLens Internals"
```
