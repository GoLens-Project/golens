package golens

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// otlpExporter pushes metric snapshots to an OTLP/HTTP receiver using the
// JSON encoding permitted by the OTLP/HTTP spec. gRPC export is deferred to a
// separate sub-package.
type otlpExporter struct {
	cfg    OTLPConfig
	client *http.Client
}

func newOTLPExporter(cfg OTLPConfig) *otlpExporter {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &otlpExporter{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

// otlpMetricsRequest models the subset of the OTLP/HTTP JSON schema we emit.
type otlpMetricsRequest struct {
	ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
}

type otlpResourceMetrics struct {
	Resource struct {
		Attributes []otlpKV `json:"attributes"`
	} `json:"resource"`
	ScopeMetrics []otlpScopeMetrics `json:"scopeMetrics"`
}

type otlpScopeMetrics struct {
	Scope struct {
		Name string `json:"name"`
	} `json:"scope"`
	Metrics []otlpMetric `json:"metrics"`
}

type otlpMetric struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// One of the following, depending on type.
	Gauge     *otlpGauge     `json:"gauge,omitempty"`
	Sum       *otlpSum       `json:"sum,omitempty"`
	Histogram *otlpHistogram `json:"histogram,omitempty"`
}

type otlpNumberDataPoint struct {
	Attributes []otlpKV `json:"attributes,omitempty"`
	StartTime  int64    `json:"startTimeUnixNano,omitempty"`
	Time       int64    `json:"timeUnixNano"`
	AsDouble   float64  `json:"asDouble"`
}

type otlpGauge struct {
	DataPoints []otlpNumberDataPoint `json:"dataPoints"`
}

type otlpSum struct {
	AggregationTemporality int                   `json:"aggregationTemporality"`
	IsMonotonic            bool                  `json:"isMonotonic"`
	DataPoints             []otlpNumberDataPoint `json:"dataPoints"`
}

type otlpHistogram struct {
	AggregationTemporality int                  `json:"aggregationTemporality"`
	DataPoints             []otlpHistogramPoint `json:"dataPoints"`
}

type otlpHistogramPoint struct {
	Attributes   []otlpKV  `json:"attributes,omitempty"`
	StartTime    int64     `json:"startTimeUnixNano,omitempty"`
	Time         int64     `json:"timeUnixNano"`
	Count        int64     `json:"count"`
	Sum          float64   `json:"sum"`
	Bounds       []float64 `json:"explicitBounds"`
	BucketCounts []uint64  `json:"bucketCounts"`
}

type otlpKV struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue string `json:"stringValue,omitempty"`
}

// export encodes and POSTs the snapshots to the configured OTLP/HTTP endpoint.
func (e *otlpExporter) export(ctx context.Context, snapshots []MetricSnapshot) error {
	req := otlpMetricsRequest{
		ResourceMetrics: []otlpResourceMetrics{{
			ScopeMetrics: []otlpScopeMetrics{{
				Scope: struct {
					Name string `json:"name"`
				}{Name: "golens"},
				Metrics: buildMetrics(snapshots),
			}},
		}},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("golens: marshal otlp: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("golens: otlp request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("golens: otlp post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("golens: otlp receiver returned %s", resp.Status)
	}
	return nil
}

func buildMetrics(snapshots []MetricSnapshot) []otlpMetric {
	out := make([]otlpMetric, 0, len(snapshots))
	for _, s := range snapshots {
		m := otlpMetric{Name: s.Name, Description: s.Description}
		dp := otlpNumberDataPoint{Time: s.UpdatedAt, AsDouble: s.Value}
		switch s.Type {
		case "counter":
			m.Sum = &otlpSum{
				AggregationTemporality: 2, // cumulative
				IsMonotonic:            true,
				DataPoints:             []otlpNumberDataPoint{dp},
			}
		case "gauge":
			m.Gauge = &otlpGauge{DataPoints: []otlpNumberDataPoint{dp}}
		case "histogram":
			bounds := make([]float64, 0, len(s.Buckets))
			counts := make([]uint64, 0, len(s.Buckets))
			for _, b := range s.Buckets {
				if !b.Overflow {
					bounds = append(bounds, b.UpperBound)
				}
				counts = append(counts, uint64(b.Count))
			}
			m.Histogram = &otlpHistogram{
				AggregationTemporality: 2,
				DataPoints: []otlpHistogramPoint{{
					Time:         s.UpdatedAt,
					Count:        s.Count,
					Sum:          s.Sum,
					Bounds:       bounds,
					BucketCounts: counts,
				}},
			}
		}
		out = append(out, m)
	}
	return out
}
