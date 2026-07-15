package golens

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level GoLens configuration. Every field is optional; a
// zero-value Config produces a working, in-memory-only registry.
type Config struct {
	Storage        StorageConfig        `yaml:"storage"`
	OTLP           OTLPConfig           `yaml:"otlp"`
	UI             UIConfig             `yaml:"ui"`
	RuntimeMetrics RuntimeMetricsConfig `yaml:"runtime_metrics"`
	ProjectName    string               `yaml:"project_name"`
	Debug          bool                 `yaml:"debug"`

	// IngestQueueSize bounds the non-blocking ingestion channel. When full,
	// new samples are dropped to protect the request lifecycle.
	IngestQueueSize int `yaml:"ingest_queue_size"`
	// MaxMetrics caps the number of distinct metric series kept in memory.
	MaxMetrics int `yaml:"max_metrics"`
	// MaxEndpoints caps the number of distinct (method,path) endpoints tracked
	// for the per-endpoint latency chart.
	MaxEndpoints int `yaml:"max_endpoints"`
	// MetricTTL is how long a metric may stay idle before eviction.
	MetricTTL time.Duration `yaml:"metric_ttl"`
	// FlushInterval governs background aggregation/SQLite flush cadence.
	FlushInterval time.Duration `yaml:"flush_interval"`

	// IncludePatterns / ExcludePatterns filter which request paths are
	// instrumented. Empty includes means "all"; excludes win over includes.
	IncludePatterns []string `yaml:"include_patterns"`
	ExcludePatterns []string `yaml:"exclude_patterns"`
}

// StorageConfig controls persistence.
type StorageConfig struct {
	// Backend: "" / "memory" (default) or "sqlite".
	Backend string        `yaml:"backend"`
	Path    string        `yaml:"path"`
	TTL     time.Duration `yaml:"ttl"`
}

// OTLPConfig controls push export over OTLP/HTTP (JSON encoding).
type OTLPConfig struct {
	Enabled   bool          `yaml:"enabled"`
	Endpoint  string        `yaml:"endpoint"`
	Protocol  string        `yaml:"protocol"` // "http" (default) — gRPC deferred
	BatchSize int           `yaml:"batch_size"`
	Interval  time.Duration `yaml:"interval"`
	Timeout   time.Duration `yaml:"timeout"`
}

// UIConfig controls the embedded HTMX dashboard.
type UIConfig struct {
	Enabled      bool          `yaml:"enabled"`
	PollInterval time.Duration `yaml:"poll_interval"`
	Auth         AuthConfig    `yaml:"auth"`
}

// RuntimeMetricsConfig controls optional Go runtime metrics collection.
type RuntimeMetricsConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"` // collection interval, default 15s
}

// AuthConfig enables optional admin-only HTTP Basic Auth on the dashboard.
//
// Two secret forms are accepted:
//
//   - Password:   a plaintext password (resolved from the env if it uses the
//     "env:VAR" form). It is bcrypt-hashed in memory at load time
//     so the plaintext is never retained.
//   - PasswordHash: a pre-computed bcrypt hash (recommended for production), so
//     no plaintext secret ever lives in configuration.
//
// At least one of Password / PasswordHash must be set together with a Username
// for auth to be active. Password takes precedence over PasswordHash when both
// are provided.
type AuthConfig struct {
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`      // plaintext or "env:VAR"; hashed at load
	PasswordHash string `yaml:"password_hash"` // pre-computed bcrypt hash
}

// expandEnv resolves a "env:VAR" reference to the variable's value, returning
// the value unchanged otherwise. Empty input yields empty output.
func expandEnv(s string) string {
	if s == "" {
		return ""
	}
	const prefix = "env:"
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return os.Getenv(s[len(prefix):])
	}
	return s
}

// DefaultConfig returns a sensible, working configuration.
func DefaultConfig() Config {
	return Config{
		Storage: StorageConfig{
			Backend: "memory",
			TTL:     24 * time.Hour,
		},
		OTLP: OTLPConfig{
			Enabled:   false,
			Endpoint:  "http://localhost:4318/v1/metrics",
			Protocol:  "http",
			BatchSize: 100,
			Interval:  10 * time.Second,
			Timeout:   5 * time.Second,
		},
		UI: UIConfig{
			Enabled:      true,
			PollInterval: 5 * time.Second,
		},
		ProjectName:     "GoLens",
		IngestQueueSize: 4096,
		MaxMetrics:      10_000,
		MaxEndpoints:    128,
		MetricTTL:       1 * time.Hour,
		FlushInterval:   30 * time.Second,
		// Exclude the dashboard routes so the UI's own HTMX polling doesn't
		// inflate the request/error counters (self-instrumentation feedback).
		ExcludePatterns: []string{"^/metrics"},
	}
}

// LoadConfig reads a YAML config file and overlays it on DefaultConfig. Missing
// fields fall back to defaults, so a partial file is valid.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

// applyDefaults fills zero-value fields that yaml.Unmarshal left blank.
func (c *Config) applyDefaults() {
	if c.Storage.Backend == "" {
		c.Storage.Backend = "memory"
	}
	if c.Storage.TTL == 0 {
		c.Storage.TTL = 24 * time.Hour
	}
	if c.OTLP.Protocol == "" {
		c.OTLP.Protocol = "http"
	}
	if c.OTLP.Endpoint == "" {
		c.OTLP.Endpoint = "http://localhost:4318/v1/metrics"
	}
	if c.OTLP.BatchSize == 0 {
		c.OTLP.BatchSize = 100
	}
	if c.OTLP.Interval == 0 {
		c.OTLP.Interval = 10 * time.Second
	}
	if c.OTLP.Timeout == 0 {
		c.OTLP.Timeout = 5 * time.Second
	}
	if c.UI.PollInterval == 0 {
		c.UI.PollInterval = 5 * time.Second
	}
	if c.IngestQueueSize == 0 {
		c.IngestQueueSize = 4096
	}
	if c.MaxMetrics == 0 {
		c.MaxMetrics = 10_000
	}
	if c.MaxEndpoints == 0 {
		c.MaxEndpoints = 128
	}
	if c.MetricTTL == 0 {
		c.MetricTTL = 1 * time.Hour
	}
	if c.FlushInterval == 0 {
		c.FlushInterval = 30 * time.Second
	}
	if c.RuntimeMetrics.Interval == 0 {
		c.RuntimeMetrics.Interval = 15 * time.Second
	}
}
