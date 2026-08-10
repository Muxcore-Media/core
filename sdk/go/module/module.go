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
// Config resolution priority:
//  1. Explicit Config field
//  2. Environment variable (MUXCORE_GRPC_ADDR, MUXCORE_MODULE_ID, MUXCORE_TLS_*)
//  3. CLI flag (--muxcore-mesh-addr, --muxcore-module-id, --muxcore-tls-*)
//  4. Module.Info().ID (for module ID only)
package module

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Muxcore-Media/core/pkg/contracts"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
)

const (
	envGRPCAddr = "MUXCORE_GRPC_ADDR"
	envModuleID = "MUXCORE_MODULE_ID"
	envTLSCert  = "MUXCORE_TLS_CERT"
	envTLSKey   = "MUXCORE_TLS_KEY"
	envTLSCA    = "MUXCORE_TLS_CA"

	flagGRPCAddr = "muxcore-mesh-addr"
	flagModuleID = "muxcore-module-id"
	flagTLSCert  = "muxcore-tls-cert"
	flagTLSKey   = "muxcore-tls-key"
	flagTLSCA    = "muxcore-tls-ca"

	defaultShutdownTimeout = 10 * time.Second
)

func init() {
	// Register mesh/TLS flags so core-spawned modules accept --muxcore-* args
	// without "flag provided but not defined". Safe if callers already defined them.
	registerStringFlag(flagGRPCAddr, "", "MuxCore mesh gRPC address (host:port)")
	registerStringFlag(flagModuleID, "", "MuxCore module ID")
	registerStringFlag(flagTLSCert, "", "Client TLS certificate PEM path")
	registerStringFlag(flagTLSKey, "", "Client TLS private key PEM path")
	registerStringFlag(flagTLSCA, "", "CA certificate PEM path (verify core)")
}

func registerStringFlag(name, value, usage string) {
	if flag.Lookup(name) == nil {
		flag.String(name, value, usage)
	}
}

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

	// TLSCertFile / TLSKeyFile are the client certificate for mTLS.
	// If empty, reads MUXCORE_TLS_CERT / MUXCORE_TLS_KEY or --muxcore-tls-cert/key.
	TLSCertFile string
	TLSKeyFile  string

	// TLSCAFile is the CA used to verify core's server certificate.
	// If empty, reads MUXCORE_TLS_CA or --muxcore-tls-ca.
	TLSCAFile string
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

	conn, err := dialGRPC(grpcAddr, dialTLSConfig{
		plaintext: cfg.Insecure,
		certFile:  resolveString(cfg.TLSCertFile, envTLSCert, flagTLSCert, ""),
		keyFile:   resolveString(cfg.TLSKeyFile, envTLSKey, flagTLSKey, ""),
		caFile:    resolveString(cfg.TLSCAFile, envTLSCA, flagTLSCA, ""),
	})
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

	TLSCertFile string
	TLSKeyFile  string
	TLSCAFile   string
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
	return dialGRPC(grpcAddr, dialTLSConfig{
		plaintext: cfg.Insecure,
		certFile:  resolveString(cfg.TLSCertFile, envTLSCert, flagTLSCert, ""),
		keyFile:   resolveString(cfg.TLSKeyFile, envTLSKey, flagTLSKey, ""),
		caFile:    resolveString(cfg.TLSCAFile, envTLSCA, flagTLSCA, ""),
	})
}

// WaitForShutdown blocks until SIGTERM or SIGINT, then returns the signal.
// Useful for CLI tools that connect to core and need to block until shutdown.
func WaitForShutdown() os.Signal {
	return waitForSignal()
}

type dialTLSConfig struct {
	plaintext bool
	certFile  string
	keyFile   string
	caFile    string
}

func dialGRPC(addr string, tlsCfg dialTLSConfig) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption
	if tlsCfg.plaintext {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		creds, err := loadClientTLS(tlsCfg.certFile, tlsCfg.keyFile, tlsCfg.caFile)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	}
	return grpc.NewClient(addr, opts...)
}

func loadClientTLS(certFile, keyFile, caFile string) (credentials.TransportCredentials, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("TLS required — set %s/%s (or --%s/--%s), or enable insecure with MUXCORE_INSECURE_DISABLE_TLS=true",
			envTLSCert, envTLSKey, flagTLSCert, flagTLSKey)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client TLS cert/key: %w", err)
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if caFile != "" {
		pemBytes, err := os.ReadFile(caFile) //nolint:gosec // path from operator config
		if err != nil {
			return nil, fmt.Errorf("read TLS CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("parse TLS CA from %q", caFile)
		}
		tlsConfig.RootCAs = pool
	}
	return credentials.NewTLS(tlsConfig), nil
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
