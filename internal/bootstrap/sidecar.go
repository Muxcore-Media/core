package bootstrap

import (
	"log/slog"

	"github.com/Muxcore-Media/core/internal/grpcmesh"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"google.golang.org/grpc/credentials"
)

// DialSecretsProvider looks up a sidecar module with the "secrets" capability
// and returns a SecretsProvider backed by its gRPC service. Returns nil if
// no secrets module is registered.
func DialSecretsProvider(reg *registry.Registry, creds credentials.TransportCredentials) contracts.SecretsProvider {
	entries := reg.FindByCapability(contracts.CapabilitySecrets)
	if len(entries) == 0 {
		return nil
	}
	entry := entries[0]
	addr := entry.Info.HTTPAddr
	if addr == "" {
		slog.Warn("secrets module has no HTTPAddr, cannot dial", "module", entry.Info.ID)
		return nil
	}
	conn, err := DialSidecar(addr, creds, 32*1024*1024)
	if err != nil {
		slog.Warn("dial secrets module", "module", entry.Info.ID, "error", err)
		return nil
	}
	return grpcmesh.NewSidecarSecrets(conn)
}

// DialCacheProvider looks up a sidecar module with the "cache" capability
// and returns a CacheProvider backed by its gRPC service. Returns nil if
// no cache module is registered.
func DialCacheProvider(reg *registry.Registry, creds credentials.TransportCredentials) contracts.CacheProvider {
	entries := reg.FindByCapability(contracts.CapabilityCache)
	if len(entries) == 0 {
		return nil
	}
	entry := entries[0]
	addr := entry.Info.HTTPAddr
	if addr == "" {
		slog.Warn("cache module has no HTTPAddr, cannot dial", "module", entry.Info.ID)
		return nil
	}
	conn, err := DialSidecar(addr, creds, 32*1024*1024)
	if err != nil {
		slog.Warn("dial cache module", "module", entry.Info.ID, "error", err)
		return nil
	}
	return grpcmesh.NewSidecarCache(conn)
}

// DialDatabaseProvider looks up a sidecar module with the "database" capability
// and returns a DatabaseProvider backed by its gRPC service. Returns nil if
// no database module is registered.
func DialDatabaseProvider(reg *registry.Registry, creds credentials.TransportCredentials) contracts.DatabaseProvider {
	entries := reg.FindByCapability(contracts.CapabilityDatabase)
	if len(entries) == 0 {
		return nil
	}
	entry := entries[0]
	addr := entry.Info.HTTPAddr
	if addr == "" {
		slog.Warn("database module has no HTTPAddr, cannot dial", "module", entry.Info.ID)
		return nil
	}
	conn, err := DialSidecar(addr, creds, 32*1024*1024)
	if err != nil {
		slog.Warn("dial database module", "module", entry.Info.ID, "error", err)
		return nil
	}
	return grpcmesh.NewSidecarDatabase(conn)
}
