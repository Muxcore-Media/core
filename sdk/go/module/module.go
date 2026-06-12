// Package module provides a framework for building MuxCore sidecar modules.
//
// The SDK handles the full module lifecycle: connecting to core, registering,
// handling shutdown signals, and unregistering on exit.
//
// # Basic usage (sidecar module)
//
//	func main() {
//	    modulesdk.Run(modulesdk.Config{
//	        Module: myModule,
//	    })
//	}
//
// # CLI tool usage (client only, no registration)
//
//	conn, cleanup := modulesdk.Connect(modulesdk.ConnectConfig{
//	    OnConnect: func(c *grpc.ClientConn) {
//	        // use c to create service clients
//	    },
//	})
//	defer cleanup()
//
// Config resolution priority:
//  1. Explicit Config field
//  2. Environment variable (MUXCORE_GRPC_ADDR, MUXCORE_MODULE_ID)
//  3. CLI flag (--muxcore-mesh-addr, --muxcore-module-id)
//  4. Module.Info().ID (for module ID only)
package module

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Muxcore-Media/core/pkg/contracts"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
)

const (
	envGRPCAddr = "MUXCORE_GRPC_ADDR"
	envModuleID = "MUXCORE_MODULE_ID"

	flagGRPCAddr = "muxcore-mesh-addr"
	flagModuleID = "muxcore-module-id"

	defaultShutdownTimeout = 10 * time.Second
)

// Config configures a sidecar module's lifecycle.
type Config struct {
	// Module is the module implementation. Required.
	Module contracts.Module

	// GRPCAddr is the core gRPC address (host:port).
	// If empty, reads from MUXCORE_GRPC_ADDR or --muxcore-mesh-addr.
	GRPCAddr string

	// ModuleID overrides the module identifier.
	// If empty, reads from MUXCORE_MODULE_ID, --muxcore-module-id,
	// then falls back to Module.Info().ID.
	ModuleID string

	// Insecure disables TLS. Use for development only.
	Insecure bool
}

// Run starts the module, connects to core, registers, and blocks until
// SIGTERM/SIGINT. Returns nil on clean shutdown.
func Run(cfg Config) error {
	if cfg.Module == nil {
		return fmt.Errorf("module: Module interface must not be nil")
	}
	grpcAddr := resolveString(cfg.GRPCAddr, envGRPCAddr, flagGRPCAddr, "")
	moduleID := resolveString(cfg.ModuleID, envModuleID, flagModuleID, cfg.Module.Info().ID)

	if grpcAddr == "" {
		return fmt.Errorf("module: core gRPC address is required — set %s or --%s", envGRPCAddr, flagGRPCAddr)
	}
	if moduleID == "" {
		return fmt.Errorf("module: module ID is required — set %s or --%s", envModuleID, flagModuleID)
	}

	conn, err := dialGRPC(grpcAddr, cfg.Insecure)
	if err != nil {
		return fmt.Errorf("module: connect to core at %s: %w", grpcAddr, err)
	}
	defer conn.Close()

	reg := modulev1.NewModuleRegistrationClient(conn)

	info := cfg.Module.Info()
	info.ID = moduleID

	resp, err := reg.Register(context.Background(), &modulev1.RegisterRequest{
		ModuleId: moduleID,
		ModuleInfo: &modulev1.ModuleInfo{
			Id:             info.ID,
			Name:           info.Name,
			Version:        info.Version,
			Roles:          info.Roles,
			Description:    info.Description,
			Author:         info.Author,
			Capabilities:   info.Capabilities,
			DependsOn:      info.DependsOn,
			MinCoreVersion: info.MinCoreVersion,
			HttpAddr:       info.HTTPAddr,
		},
	})
	if err != nil {
		return fmt.Errorf("module: register RPC: %w", err)
	}
	if !resp.GetAccepted() {
		return fmt.Errorf("module: core rejected registration: %s", resp.GetError())
	}

	slog.Info("module registered with core",
		"id", moduleID,
		"version", info.Version,
		"mesh_addr", resp.GetMeshAddr(),
		"node_id", resp.GetNodeId(),
	)

	if err := cfg.Module.Init(context.Background()); err != nil {
		return fmt.Errorf("module: init: %w", err)
	}
	slog.Info("module initialized", "id", moduleID)

	if err := cfg.Module.Start(context.Background()); err != nil {
		return fmt.Errorf("module: start: %w", err)
	}
	slog.Info("module started", "id", moduleID)

	sig := waitForSignal()
	slog.Info("module shutting down", "id", moduleID, "signal", sig)

	stopCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()

	if err := cfg.Module.Stop(stopCtx); err != nil {
		slog.Error("module stop error", "id", moduleID, "error", err)
	}

	if _, err := reg.Unregister(context.Background(), &modulev1.UnregisterRequest{ModuleId: moduleID}); err != nil {
		slog.Error("module unregister error", "id", moduleID, "error", err)
	}

	slog.Info("module stopped", "id", moduleID)
	return nil
}

// ConnectConfig configures a client-only connection to core.
type ConnectConfig struct {
	// GRPCAddr is the core gRPC address (host:port).
	// If empty, reads from MUXCORE_GRPC_ADDR or --muxcore-mesh-addr.
	GRPCAddr string

	// Insecure disables TLS for development.
	Insecure bool
}

// Connect opens a gRPC connection to core and returns it.
// The caller must close the connection when done.
//
// Config is resolved from the same sources as Run (env vars, CLI flags).
func Connect(cfg ConnectConfig) (*grpc.ClientConn, error) {
	grpcAddr := resolveString(cfg.GRPCAddr, envGRPCAddr, flagGRPCAddr, "")
	if grpcAddr == "" {
		return nil, fmt.Errorf("module: core gRPC address is required — set %s or --%s", envGRPCAddr, flagGRPCAddr)
	}
	return dialGRPC(grpcAddr, cfg.Insecure)
}

// WaitForShutdown blocks until SIGTERM or SIGINT, then returns the signal.
// Useful for CLI tools that connect to core and need to block until shutdown.
func WaitForShutdown() os.Signal {
	return waitForSignal()
}

func dialGRPC(addr string, plaintext bool) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption
	if plaintext {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	return grpc.NewClient(addr, opts...)
}

func waitForSignal() os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	once.Do(func() {
		// Ensure flag.Parse has been called so Lookup works for registered flags.
		// Safe to call multiple times; subsequent calls are no-ops.
		if !flag.Parsed() {
			flag.Parse()
		}
	})
	return <-sigCh
}

var once sync.Once

func resolveString(val, env, flagName, fallback string) string {
	if val != "" {
		return val
	}
	if env != "" {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	if flagName != "" {
		if f := flag.Lookup(flagName); f != nil {
			if v := f.Value.String(); v != "" {
				return v
			}
		}
	}
	return fallback
}
