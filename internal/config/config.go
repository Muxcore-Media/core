package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the top-level configuration for MuxCore.
type Config struct {
	Server   ServerConfig   `json:"server"`
	Log      LogConfig      `json:"log"`
	Database DatabaseConfig `json:"database"`
	Cache    CacheConfig    `json:"cache"`
	Trace    TraceConfig    `json:"trace"`
	Modules  map[string]any `json:"modules"` // per-module arbitrary config
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr         string `json:"addr"`          // listen address, e.g. ":8080"
	ReadTimeout  int    `json:"read_timeout"`  // seconds
	WriteTimeout int    `json:"write_timeout"` // seconds
}

// LogConfig controls structured logging output.
type LogConfig struct {
	Level  string `json:"level"`  // debug, info, warn, error
	Format string `json:"format"` // text, json
}

// DatabaseConfig holds database connection settings (driver provided by database module).
type DatabaseConfig struct {
	Driver string `json:"driver"`
	URL    string `json:"url"`
}

// CacheConfig holds cache connection settings (driver provided by cache module).
type CacheConfig struct {
	Driver string `json:"driver"`
	URL    string `json:"url"`
}

// TraceConfig controls OpenTelemetry tracing export.
// Default exporter is "" (noop) — zero telemetry leaves the process.
// Set exporter to "otlp" and endpoint to send spans via OTLP gRPC.
// Set exporter to "stdout" for development-local console output.
type TraceConfig struct {
	Exporter   string  `json:"exporter"`    // "" (noop), "otlp", "stdout"
	Endpoint   string  `json:"endpoint"`    // OTLP gRPC endpoint (e.g. "localhost:4317")
	SampleRate float64 `json:"sample_rate"` // 0.0–1.0, default 1.0
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Addr:         ":8080",
			ReadTimeout:  15,
			WriteTimeout: 15,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Database: DatabaseConfig{},
		Cache:    CacheConfig{},
		Trace: TraceConfig{
			Exporter:   "",
			SampleRate: 1.0,
		},
		Modules: make(map[string]any),
	}
}

// Load reads configuration from a JSON file, overlays environment variable
// overrides, and validates the result. If path is empty, only defaults and
// env vars are used.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, err // let the caller decide how to handle
			}
			return nil, fmt.Errorf("read config file: %w", err)
		}

		if len(data) > 0 {
			if err := json.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("parse config file: %w", err)
			}
		}
	}

	// Environment variable overrides — highest precedence.
	if v := os.Getenv("MUXCORE_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("MUXCORE_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("MUXCORE_LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
	if v := os.Getenv("MUXCORE_DATABASE_DRIVER"); v != "" {
		cfg.Database.Driver = v
	}
	if v := os.Getenv("MUXCORE_DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
	if v := os.Getenv("MUXCORE_CACHE_DRIVER"); v != "" {
		cfg.Cache.Driver = v
	}
	if v := os.Getenv("MUXCORE_CACHE_URL"); v != "" {
		cfg.Cache.URL = v
	}
	if v := os.Getenv("MUXCORE_TRACE_EXPORTER"); v != "" {
		cfg.Trace.Exporter = v
	}
	if v := os.Getenv("MUXCORE_TRACE_ENDPOINT"); v != "" {
		cfg.Trace.Endpoint = v
	}
	if v := os.Getenv("MUXCORE_TRACE_SAMPLE_RATE"); v != "" {
		rate, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("MUXCORE_TRACE_SAMPLE_RATE must be a float between 0.0 and 1.0, got %q", v)
		}
		cfg.Trace.SampleRate = rate
	}
	// Normalize case-sensitive fields so consumers don't need to handle
	// mixed case from env vars or config files.
	cfg.Log.Level = strings.ToLower(cfg.Log.Level)
	cfg.Log.Format = strings.ToLower(cfg.Log.Format)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate checks that the config is internally consistent.
func (c *Config) validate() error {
	var errs []string

	if c.Server.Addr == "" {
		errs = append(errs, "server.addr must not be empty")
	}
	if c.Server.ReadTimeout <= 0 {
		errs = append(errs, "server.read_timeout must be positive")
	}
	if c.Server.WriteTimeout <= 0 {
		errs = append(errs, "server.write_timeout must be positive")
	}

	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[strings.ToLower(c.Log.Level)] {
		errs = append(errs, fmt.Sprintf("log.level must be one of: debug, info, warn, error (got %q)", c.Log.Level))
	}

	validFormats := map[string]bool{"text": true, "json": true}
	if !validFormats[strings.ToLower(c.Log.Format)] {
		errs = append(errs, fmt.Sprintf("log.format must be one of: text, json (got %q)", c.Log.Format))
	}

	validExporters := map[string]bool{"": true, "otlp": true, "stdout": true}
	if !validExporters[strings.ToLower(c.Trace.Exporter)] {
		errs = append(errs, fmt.Sprintf("trace.exporter must be one of: (empty), otlp, stdout (got %q)", c.Trace.Exporter))
	}
	if c.Trace.Exporter == "otlp" && c.Trace.Endpoint == "" {
		errs = append(errs, "trace.endpoint is required when trace.exporter is otlp")
	}
	if c.Trace.SampleRate < 0.0 || c.Trace.SampleRate > 1.0 {
		errs = append(errs, fmt.Sprintf("trace.sample_rate must be between 0.0 and 1.0 (got %f)", c.Trace.SampleRate))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
