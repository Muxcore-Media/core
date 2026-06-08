package config

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
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
	Audit    AuditConfig    `json:"audit"`
	Storage  StorageConfig  `json:"storage"`
	Modules  map[string]any `json:"modules"` // per-module arbitrary config
}

// StorageConfig controls per-operation timeouts for the storage orchestrator.
// Timeouts wrap provider calls so a hung provider cannot block callers indefinitely.
type StorageConfig struct {
	// ReadTimeoutSeconds caps Get, Exists, Stat, List, Stream operations. Default: 30.
	ReadTimeoutSeconds int `json:"read_timeout_seconds"`
	// WriteTimeoutSeconds caps Put operations (larger files need more time). Default: 300.
	WriteTimeoutSeconds int `json:"write_timeout_seconds"`
	// DeleteTimeoutSeconds caps Delete and Move operations. Default: 30.
	DeleteTimeoutSeconds int `json:"delete_timeout_seconds"`
}

// RedactedJSON returns the config as JSON with secrets removed.
// Credential URLs (database, cache) are redacted, and TLS key paths
// are replaced with "<set>" to avoid leaking filesystem layout.
func (c *Config) RedactedJSON() (json.RawMessage, error) {
	// Create a copy to avoid mutating the live config.
	redacted := *c
	redacted.Database = DatabaseConfig{
		Driver: c.Database.Driver,
		URL:    redactURL(c.Database.URL),
	}
	redacted.Cache = CacheConfig{
		Driver: c.Cache.Driver,
		URL:    redactURL(c.Cache.URL),
	}
	// Hide TLS key paths (cert paths are less sensitive but we redact both).
	if redacted.Server.KeyFile != "" {
		redacted.Server.KeyFile = "<set>"
	}
	if redacted.Server.CertFile != "" {
		redacted.Server.CertFile = "<set>"
	}
	if redacted.GRPC.KeyFile != "" {
		redacted.GRPC.KeyFile = "<set>"
	}
	if redacted.GRPC.CertFile != "" {
		redacted.GRPC.CertFile = "<set>"
	}
	// Hide join token.
	if redacted.GRPC.JoinToken != "" {
		redacted.GRPC.JoinToken = "<set>"
	}

	data, err := json.Marshal(redacted)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
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
	// MaxMessageSizeMB is the maximum gRPC message size in megabytes.
	// Applies to both send and receive. Default 32MB. Increase for large storage objects.
	MaxMessageSizeMB int `json:"max_message_size_mb"`
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
// The URL field may contain credentials (e.g., postgres://user:pass@host/db).
// LogValue redacts the URL when logged via slog to prevent credential exposure.
type DatabaseConfig struct {
	Driver string `json:"driver"`
	URL    string `json:"url"`
}

// LogValue implements slog.LogValuer to redact credentials from the URL
// when this config is logged. Returns the URL with the userinfo portion
// replaced with "***".
func (d DatabaseConfig) LogValue() slog.Value {
	redacted := DatabaseConfig{Driver: d.Driver, URL: redactURL(d.URL)}
	return slog.GroupValue(
		slog.String("driver", redacted.Driver),
		slog.String("url", redacted.URL),
	)
}

// CacheConfig holds cache connection settings (driver provided by cache module).
// The URL field may contain credentials (e.g., redis://user:pass@host:6379).
// LogValue redacts the URL when logged via slog to prevent credential exposure.
type CacheConfig struct {
	Driver string `json:"driver"`
	URL    string `json:"url"`
}

// LogValue implements slog.LogValuer to redact credentials from the URL
// when this config is logged.
func (c CacheConfig) LogValue() slog.Value {
	redacted := CacheConfig{Driver: c.Driver, URL: redactURL(c.URL)}
	return slog.GroupValue(
		slog.String("driver", redacted.Driver),
		slog.String("url", redacted.URL),
	)
}

// redactURL returns the URL with the userinfo portion replaced with "***".
// If the URL cannot be parsed, returns "<redacted>" to avoid leaking raw credentials.
func redactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<redacted>"
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	return u.String()
}

// AuditConfig holds audit logging settings.
type AuditConfig struct {
	Path            string `json:"path"`              // file path for JSONL audit log; empty = disabled
	MaxSizeMB       int    `json:"max_size_mb"`       // rotate when file exceeds this size (default: 100 MB)
	MaxRotatedFiles int    `json:"max_rotated_files"` // number of rotated files to keep (default: 5)
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
			Addr:             ":9090",
			MaxMessageSizeMB: 32,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Database: DatabaseConfig{},
		Cache:    CacheConfig{},
		Storage: StorageConfig{
			ReadTimeoutSeconds:   30,
			WriteTimeoutSeconds:  300,
			DeleteTimeoutSeconds: 30,
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
	if v := os.Getenv("MUXCORE_AUDIT_PATH"); v != "" {
		cfg.Audit.Path = v
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
	if c.Server.Addr != "" {
		if _, err := net.ResolveTCPAddr("tcp", c.Server.Addr); err != nil {
			errs = append(errs, fmt.Sprintf("server.addr %q is not a valid TCP address: %v", c.Server.Addr, err))
		}
	}
	if c.GRPC.Addr != "" {
		if _, err := net.ResolveTCPAddr("tcp", c.GRPC.Addr); err != nil {
			errs = append(errs, fmt.Sprintf("grpc.addr %q is not a valid TCP address: %v", c.GRPC.Addr, err))
		}
	}
	if c.GRPC.MaxMessageSizeMB <= 0 {
		errs = append(errs, "grpc.max_message_size_mb must be positive (default 32)")
	}

	// Validate already-normalized values (case normalization done in Load()).
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Log.Level] {
		errs = append(errs, fmt.Sprintf("log.level must be one of: debug, info, warn, error (got %q)", c.Log.Level))
	}

	validFormats := map[string]bool{"text": true, "json": true}
	if !validFormats[c.Log.Format] {
		errs = append(errs, fmt.Sprintf("log.format must be one of: text, json (got %q)", c.Log.Format))
	}

	// Validate TLS certificate files can be loaded at startup (fail-fast on misconfiguration).
	if c.Server.CertFile != "" || c.Server.KeyFile != "" {
		if _, err := tls.LoadX509KeyPair(c.Server.CertFile, c.Server.KeyFile); err != nil {
			errs = append(errs, fmt.Sprintf("server TLS cert/key invalid: %v", err))
		}
	}

	// Validate storage timeouts. Zero means "use default" — negative is invalid.
	if c.Storage.ReadTimeoutSeconds < 0 {
		errs = append(errs, "storage.read_timeout_seconds must be >= 0 (0 = default 30s)")
	}
	if c.Storage.WriteTimeoutSeconds < 0 {
		errs = append(errs, "storage.write_timeout_seconds must be >= 0 (0 = default 300s)")
	}
	if c.Storage.DeleteTimeoutSeconds < 0 {
		errs = append(errs, "storage.delete_timeout_seconds must be >= 0 (0 = default 30s)")
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}