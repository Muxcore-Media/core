//nolint:govet // struct field alignment
package config

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
)

// Config is the top-level configuration for MuxCore.
type Config struct {
	// Version is the config schema version. Currently only "1" is valid.
	// If set, Load validates it matches the expected schema version.
	// If empty, the config is treated as version 1 for backward compatibility.
	Version  string         `json:"version"`
	Server   ServerConfig   `json:"server"`
	GRPC     GRPCConfig     `json:"grpc"`
	Log      LogConfig      `json:"log"`
	Database DatabaseConfig `json:"database"`
	Cache    CacheConfig    `json:"cache"`
	Audit    AuditConfig    `json:"audit"`
	Storage  StorageConfig  `json:"storage"`
	Spool    SpoolConfig    `json:"spool"`
	Modules  map[string]any `json:"modules"` // per-module arbitrary config
}

// SpoolConfig controls spool URL validation to prevent SSRF.
type SpoolConfig struct {
	// AllowedHosts restricts spool URLs to specific hosts.
	// Empty means all hosts are allowed (backward compatible).
	// Set to e.g., ["github.com"] to restrict fetching to GitHub-hosted spools.
	AllowedHosts []string `json:"allowed_hosts"`
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
	Addr        string `json:"addr"`         // listen address, e.g. ":9090"
	CertFile    string `json:"cert_file"`    // path to TLS certificate file
	KeyFile     string `json:"key_file"`     // path to TLS key file
	MTLSEnabled bool   `json:"mtls_enabled"` // require mutual TLS
	CACertFile  string `json:"ca_cert_file"` // path to CA cert for mTLS client verification
	// CACertDir is the directory for the internal CA's key and certificate.
	// When mTLS is enabled and no CACertFile is set, the core generates an
	// ephemeral CA in this directory on first startup. Defaults to <data-dir>/ca.
	CACertDir string   `json:"ca_cert_dir"`
	SeedNodes []string `json:"seed_nodes"` // comma-separated host:port of existing cluster nodes to join
	JoinToken string   `json:"join_token"` // pre-shared token required to join the cluster
	// MaxMessageSizeMB is the maximum gRPC message size in megabytes.
	// Applies to both send and receive. Default 32MB. Increase for large storage objects.
	MaxMessageSizeMB int `json:"max_message_size_mb"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr           string   `json:"addr"`            // listen address, e.g. ":8080"
	ReadTimeout    int      `json:"read_timeout"`    // seconds
	WriteTimeout   int      `json:"write_timeout"`   // seconds
	CertFile       string   `json:"cert_file"`       // path to TLS certificate PEM
	KeyFile        string   `json:"key_file"`        // path to TLS private key PEM
	TrustedProxies []string `json:"trusted_proxies"` // CIDRs whose X-Forwarded-For is trusted; empty = loopback only
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
// parseInt is a helper for env var parsing that returns 0 on failure.
func parseInt(s string) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0
	}
	return n
}

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

const configSchemaVersion = "1"

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		Version: configSchemaVersion,
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
		Spool:    SpoolConfig{},
		Storage: StorageConfig{
			ReadTimeoutSeconds:   30,
			WriteTimeoutSeconds:  300,
			DeleteTimeoutSeconds: 30,
		},
		Modules: make(map[string]any),
	}
}

// InsecureTLSSkipEnabled returns true when TLS enforcement should be bypassed
// for development. Checks MUXCORE_INSECURE_DISABLE_TLS first (canonical name),
// then falls back to the deprecated MUXCORE_DEV_TLS_SKIP with a warning.
func InsecureTLSSkipEnabled() bool {
	if v := os.Getenv("MUXCORE_INSECURE_DISABLE_TLS"); v == "true" || v == "1" {
		return true
	}
	if v := os.Getenv("MUXCORE_DEV_TLS_SKIP"); v == "true" || v == "1" {
		slog.Warn("MUXCORE_DEV_TLS_SKIP is deprecated — use MUXCORE_INSECURE_DISABLE_TLS instead")
		return true
	}
	return false
}

// Load reads configuration from a JSON file, overlays environment variable
// overrides, and validates the result. If path is empty, only defaults and
// env vars are used.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // path is user-provided config file
		if err != nil {
			if os.IsNotExist(err) {
				return nil, err // propagate file-not-found to caller
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
	ApplyEnvOverrides(cfg)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ApplyEnvOverrides overrides config fields from environment variables and
// normalizes case-sensitive fields. Exported so callers that bypass Load
// (e.g. falling back to Default() when no config file exists) still pick up
// env var overrides.
func ApplyEnvOverrides(cfg *Config) { //nolint:gocyclo // one branch per supported env var
	// Prefer MUXCORE_SERVER_ADDR over deprecated MUXCORE_ADDR.
	if v := os.Getenv("MUXCORE_SERVER_ADDR"); v != "" {
		cfg.Server.Addr = v
	} else if v := os.Getenv("MUXCORE_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	// Prefer MUXCORE_SERVER_TLS_CERT over deprecated MUXCORE_TLS_CERT.
	if v := os.Getenv("MUXCORE_SERVER_TLS_CERT"); v != "" {
		cfg.Server.CertFile = v
	} else if v := os.Getenv("MUXCORE_TLS_CERT"); v != "" {
		cfg.Server.CertFile = v
	}
	// Prefer MUXCORE_SERVER_TLS_KEY over deprecated MUXCORE_TLS_KEY.
	if v := os.Getenv("MUXCORE_SERVER_TLS_KEY"); v != "" {
		cfg.Server.KeyFile = v
	} else if v := os.Getenv("MUXCORE_TLS_KEY"); v != "" {
		cfg.Server.KeyFile = v
	}
	if v := os.Getenv("MUXCORE_SERVER_READ_TIMEOUT"); v != "" {
		cfg.Server.ReadTimeout = parseInt(v)
	}
	if v := os.Getenv("MUXCORE_SERVER_WRITE_TIMEOUT"); v != "" {
		cfg.Server.WriteTimeout = parseInt(v)
	}
	if v := os.Getenv("MUXCORE_SERVER_TRUSTED_PROXIES"); v != "" {
		parts := strings.Split(v, ",")
		cfg.Server.TrustedProxies = cfg.Server.TrustedProxies[:0]
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				cfg.Server.TrustedProxies = append(cfg.Server.TrustedProxies, p)
			}
		}
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
	if v := os.Getenv("MUXCORE_AUDIT_PATH_FILE"); v != "" {
		if data, err := os.ReadFile(v); err == nil { //nolint:gosec // path from operator env var
			cfg.Audit.Path = strings.TrimSpace(string(data))
		} else {
			slog.Warn("MUXCORE_AUDIT_PATH_FILE: read failed", "path", v, "error", err)
		}
	}
	if v := os.Getenv("MUXCORE_AUDIT_MAX_SIZE_MB"); v != "" {
		cfg.Audit.MaxSizeMB = parseInt(v)
	}
	if v := os.Getenv("MUXCORE_AUDIT_MAX_ROTATED_FILES"); v != "" {
		cfg.Audit.MaxRotatedFiles = parseInt(v)
	}
	if v := os.Getenv("MUXCORE_STORAGE_READ_TIMEOUT"); v != "" {
		cfg.Storage.ReadTimeoutSeconds = parseInt(v)
	}
	if v := os.Getenv("MUXCORE_STORAGE_WRITE_TIMEOUT"); v != "" {
		cfg.Storage.WriteTimeoutSeconds = parseInt(v)
	}
	if v := os.Getenv("MUXCORE_STORAGE_DELETE_TIMEOUT"); v != "" {
		cfg.Storage.DeleteTimeoutSeconds = parseInt(v)
	}
	if v := os.Getenv("MUXCORE_SPOOL_ALLOWED_HOSTS"); v != "" {
		cfg.Spool.AllowedHosts = strings.Split(v, ",")
		for i := range cfg.Spool.AllowedHosts {
			cfg.Spool.AllowedHosts[i] = strings.TrimSpace(cfg.Spool.AllowedHosts[i])
		}
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
		cfg.GRPC.MTLSEnabled = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("MUXCORE_GRPC_MAX_MESSAGE_SIZE_MB"); v != "" {
		cfg.GRPC.MaxMessageSizeMB = parseInt(v)
	}
	if v := os.Getenv("MUXCORE_GRPC_CA_CERT_DIR"); v != "" {
		cfg.GRPC.CACertDir = v
	}
	// Prefer MUXCORE_GRPC_JOIN_TOKEN over deprecated MUXCORE_CLUSTER_JOIN_TOKEN.
	if v := os.Getenv("MUXCORE_GRPC_JOIN_TOKEN"); v != "" {
		cfg.GRPC.JoinToken = v
	} else if v := os.Getenv("MUXCORE_CLUSTER_JOIN_TOKEN"); v != "" {
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
}

// validate returns an error if the config has conflicting or invalid fields.
func (c *Config) validate() error {
	if c == nil {
		return fmt.Errorf("config: nil config")
	}
	var errs []error

	if c.Version != "" && c.Version != configSchemaVersion {
		errs = append(errs, fmt.Errorf("config version %q is not supported (expected %s)", c.Version, configSchemaVersion))
	}

	if c.Server.Addr == "" {
		errs = append(errs, errors.New("server.addr must not be empty"))
	}
	if c.Server.ReadTimeout <= 0 {
		errs = append(errs, errors.New("server.read_timeout must be positive"))
	}
	if c.Server.WriteTimeout <= 0 {
		errs = append(errs, errors.New("server.write_timeout must be positive"))
	}
	if c.Server.Addr != "" {
		if _, err := net.ResolveTCPAddr("tcp", c.Server.Addr); err != nil {
			errs = append(errs, fmt.Errorf("server.addr %q: %w", c.Server.Addr, err))
		}
	}
	if c.GRPC.Addr != "" {
		if _, err := net.ResolveTCPAddr("tcp", c.GRPC.Addr); err != nil {
			errs = append(errs, fmt.Errorf("grpc.addr %q: %w", c.GRPC.Addr, err))
		}
	}
	if c.GRPC.MaxMessageSizeMB <= 0 {
		errs = append(errs, errors.New("grpc.max_message_size_mb must be positive (default 32)"))
	}
	if c.GRPC.MaxMessageSizeMB > 512 {
		errs = append(errs, errors.New("grpc.max_message_size_mb must not exceed 512"))
	}

	// Validate already-normalized values (case normalization done in Load()).
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Log.Level] {
		errs = append(errs, fmt.Errorf("log.level must be one of: debug, info, warn, error (got %q)", c.Log.Level))
	}

	validFormats := map[string]bool{"text": true, "json": true}
	if !validFormats[c.Log.Format] {
		errs = append(errs, fmt.Errorf("log.format must be one of: text, json (got %q)", c.Log.Format))
	}

	// Validate TLS certificate files can be loaded at startup (fail-fast on misconfiguration).
	if c.Server.CertFile != "" || c.Server.KeyFile != "" {
		if _, err := tls.LoadX509KeyPair(c.Server.CertFile, c.Server.KeyFile); err != nil {
			errs = append(errs, fmt.Errorf("server TLS cert/key: %w", err))
		}
	}

	// Validate storage timeouts. Zero means "use default" — negative is invalid.
	if c.Storage.ReadTimeoutSeconds < 0 {
		errs = append(errs, errors.New("storage.read_timeout_seconds must be >= 0 (0 = default 30s)"))
	}
	if c.Storage.WriteTimeoutSeconds < 0 {
		errs = append(errs, errors.New("storage.write_timeout_seconds must be >= 0 (0 = default 300s)"))
	}
	if c.Storage.DeleteTimeoutSeconds < 0 {
		errs = append(errs, errors.New("storage.delete_timeout_seconds must be >= 0 (0 = default 30s)"))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s",
			strings.ReplaceAll(errors.Join(errs...).Error(), "\n", "\n  - "))
	}
	return nil
}
