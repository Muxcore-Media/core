package config

import (
	"fmt"
	"log/slog"
	"reflect"
	"strings"
)

// ChangedFields describes which config fields changed during a reload.
// The caller can use this to apply only the changed subset to subsystems.
type ChangedFields struct {
	LogLevel      bool
	LogFormat     bool
	AuditPath     bool
	StrictCall    bool
	StrictPublish bool
	SeedNodes     bool
	JoinToken     bool
	// Unsafe indicates changes that require a restart (addresses, certs, etc.).
	Unsafe       bool
	UnsafeFields []string
}

// ReloadResult is the outcome of a config reload.
type ReloadResult struct {
	Config  *Config
	Changes *ChangedFields
}

// Reload re-reads the config file and returns the new config along with
// a description of which fields changed. Safe changes (log level, audit path,
// etc.) can be applied live. Unsafe changes (server address, TLS certs, ports)
// require a restart.
//
// Returns the existing config unchanged if the file cannot be read or parsed.
func Reload(previous *Config, path string) (*ReloadResult, error) {
	if path == "" {
		return nil, fmt.Errorf("config reload: no config path specified")
	}

	// Read new config using the standard Load() path.
	newCfg, err := Load(path)
	if err != nil {
		return nil, fmt.Errorf("config reload: %w", err)
	}

	changes := diffConfigs(previous, newCfg)

	if changes.Unsafe {
		slog.Warn("config reload contains unsafe changes — restart required for full effect",
			"unsafe_fields", strings.Join(changes.UnsafeFields, ", "),
		)
	}

	return &ReloadResult{
		Config:  newCfg,
		Changes: changes,
	}, nil
}

// diffConfigs compares two configs and identifies which safe fields changed.
func diffConfigs(old, new *Config) *ChangedFields {
	c := &ChangedFields{}

	if old.Log.Level != new.Log.Level {
		c.LogLevel = true
	}
	if old.Log.Format != new.Log.Format {
		c.LogFormat = true
	}
	if old.Audit.Path != new.Audit.Path {
		c.AuditPath = true
	}

	// Seed nodes comparison.
	if !stringSlicesEqual(old.GRPC.SeedNodes, new.GRPC.SeedNodes) {
		c.SeedNodes = true
	}

	// Join token.
	if old.GRPC.JoinToken != new.GRPC.JoinToken {
		c.JoinToken = true
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "grpc.join_token")
	}

	// Unsafe changes (require restart).
	if old.Server.Addr != new.Server.Addr {
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "server.addr")
	}
	if old.Server.CertFile != new.Server.CertFile {
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "server.cert_file")
	}
	if old.Server.KeyFile != new.Server.KeyFile {
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "server.key_file")
	}
	if old.GRPC.Addr != new.GRPC.Addr {
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "grpc.addr")
	}
	if old.GRPC.CertFile != new.GRPC.CertFile {
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "grpc.cert_file")
	}
	if old.GRPC.KeyFile != new.GRPC.KeyFile {
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "grpc.key_file")
	}
	if old.GRPC.CACertFile != new.GRPC.CACertFile {
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "grpc.ca_cert_file")
	}
	if old.GRPC.MTLSEnabled != new.GRPC.MTLSEnabled {
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "grpc.mtls_enabled")
	}

	// Database and cache URL changes are unsafe (connections already established).
	if !reflect.DeepEqual(old.Database, new.Database) {
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "database")
	}
	if !reflect.DeepEqual(old.Cache, new.Cache) {
		c.Unsafe = true
		c.UnsafeFields = append(c.UnsafeFields, "cache")
	}

	return c
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
