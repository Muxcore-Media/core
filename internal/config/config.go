package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Config is the top-level configuration for MuxCore.
type Config struct {
	Server   ServerConfig   `json:"server"`
	GRPC     GRPCConfig     `json:"grpc"`
	Log      LogConfig      `json:"log"`
	Database DatabaseConfig `json:"database"`
	Cache    CacheConfig    `json:"cache"`
	Modules  map[string]any `json:"modules"` // per-module arbitrary config
}

// GRPCConfig holds gRPC server settings.
type GRPCConfig struct {
	Addr        string   `json:"addr"`         // listen address, e.g. ":9090"
	CertFile    string   `json:"cert_file"`    // path to TLS certificate file
	KeyFile     string   `json:"key_file"`     // path to TLS key file
	MTLSEnabled bool     `json:"mtls_enabled"` // require mutual TLS
	CACertFile  string   `json:"ca_cert_file"`  // path to CA cert for mTLS client verification
	SeedNodes   []string `json:"seed_nodes"`   // comma-separated host:port of existing cluster nodes to join
	JoinToken   string   `json:"join_token"`    // pre-shared token required to join the cluster
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr         string `json:"addr"`          // listen address, e.g. ":8080"
	ReadTimeout  int    `json:"read_timeout"`  // seconds
	WriteTimeout int    `json:"write_timeout"` // seconds
	CertFile     string `json:"cert_file"`     // path to TLS certificate PEM
	KeyFile      string `json:"key_file"`      // path to TLS private key PEM
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

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Addr:         ":8080",
			ReadTimeout:  15,
			WriteTimeout: 15,
		},
		GRPC: GRPCConfig{
			Addr: ":9090",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Database: DatabaseConfig{},
		Cache:    CacheConfig{},
		Modules:  make(map[string]any),
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
	if v := os.Getenv("MUXCORE_TLS_CERT"); v != "" {
		cfg.Server.CertFile = v
	}
	if v := os.Getenv("MUXCORE_TLS_KEY"); v != "" {
		cfg.Server.KeyFile = v
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
	if v := os.Getenv("MUXCORE_GRPC_TLS_CERT"); v != "" {
		cfg.GRPC.CertFile = v
	}
	if v := os.Getenv("MUXCORE_GRPC_TLS_KEY"); v != "" {
		cfg.GRPC.KeyFile = v
	}
	if v := os.Getenv("MUXCORE_GRPC_MTLS_CA"); v != "" {
		cfg.GRPC.CACertFile = v
	}
	if v := os.Getenv("MUXCORE_GRPC_MTLS_ENABLED"); v != "" {
		cfg.GRPC.MTLSEnabled = strings.ToLower(v) == "true" || v == "1"
	}
	if v := os.Getenv("MUXCORE_CLUSTER_JOIN_TOKEN"); v != "" {
		cfg.GRPC.JoinToken = v
	}
	if v := os.Getenv("MUXCORE_GRPC_SEED_NODES"); v != "" {
		seeds := strings.Split(v, ",")
		for _, s := range seeds {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			// Basic host:port validation — must contain exactly one colon
			parts := strings.Split(s, ":")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				slog.Warn("skipping invalid seed node address", "address", s)
				continue
			}
			cfg.GRPC.SeedNodes = append(cfg.GRPC.SeedNodes, s)
		}
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

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
