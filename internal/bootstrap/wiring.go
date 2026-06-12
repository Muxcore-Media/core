package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	corehealth "github.com/Muxcore-Media/core/internal/health"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/spool"
	"github.com/Muxcore-Media/core/internal/storage"
	"github.com/Muxcore-Media/core/internal/workerpool"
	"github.com/Muxcore-Media/core/pkg/contracts"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// ExtractBearerToken extracts a Bearer token from the Authorization header.
// Returns "" when the header is missing or uses a scheme other than Bearer.
func ExtractBearerToken(r *http.Request) string {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return ""
	}
	return auth[len(prefix):]
}

// IsLocalhostAddr returns true if the given address is a loopback address.
func IsLocalhostAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == ""
}

// SetupLogger creates a logger from the given config.
func SetupLogger(lc config.LogConfig) *slog.Logger {
	level := slog.LevelInfo
	switch lc.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if lc.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}

// DevTLSSkipCheck returns true when TLS enforcement should be bypassed.
func DevTLSSkipCheck() bool {
	return config.InsecureTLSSkipEnabled()
}

// DialSidecar creates a gRPC connection to a sidecar module at the given
// address. Uses TLS when creds are available; falls back to insecure only
// for localhost addresses in development mode. The maxMsgBytes parameter
// sets the client-side send/recv message size limit.
func DialSidecar(addr string, creds credentials.TransportCredentials, maxMsgBytes int) (*grpc.ClientConn, error) {
	if maxMsgBytes <= 0 {
		maxMsgBytes = 32 * 1024 * 1024
	}
	dialOpts := []grpc.DialOption{
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMsgBytes),
			grpc.MaxCallSendMsgSize(maxMsgBytes),
		),
	}
	if creds != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(creds))
	} else if IsLocalhostAddr(addr) {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		slog.Warn("sidecar connection without TLS", "addr", addr)
	} else {
		return nil, fmt.Errorf("cannot connect to sidecar at %q: TLS is required for non-localhost connections", addr)
	}
	return grpc.NewClient(addr, dialOpts...)
}

// WireCallPolicy discovers a CallPolicyProvider and sets it on the mesh client.
// Checks in-process modules first (type assertion), then falls back to sidecar
// modules registered with "call.policy" capability and an HTTPAddr.
func WireCallPolicy(reg *registry.Registry, meshClient *grpcmesh.Client, storageGrpc *grpcmesh.StorageServer, creds credentials.TransportCredentials, maxMsgBytes int) error {
	entries := reg.FindByCapability("call.policy")
	if len(entries) == 0 {
		return nil
	}
	entry := entries[0]

	// In-process module: type assertion.
	if cp, ok := entry.Module.(contracts.CallPolicyProvider); ok {
		meshClient.SetCallPolicy(cp)
		storageGrpc.SetCallPolicy(cp)
		slog.Info("call policy provider loaded from registry", "module", entry.Info.ID)
		return nil
	}

	// Sidecar module: connect via gRPC to the module's HTTPAddr.
	if addr := entry.Info.HTTPAddr; addr != "" {
		conn, err := DialSidecar(addr, creds, maxMsgBytes)
		if err != nil {
			return fmt.Errorf("dial sidecar call policy %s at %s: %w", entry.Info.ID, addr, err)
		}
		cp := grpcmesh.NewSidecarCallPolicy(conn)
		meshClient.SetCallPolicy(cp)
		storageGrpc.SetCallPolicy(cp)
		slog.Info("call policy provider loaded from sidecar module",
			"module", entry.Info.ID, "addr", addr)
		return nil
	}

	return fmt.Errorf("call policy module %q has no HTTPAddr and is not an in-process provider", entry.Info.ID)
}

// WirePublishPolicy discovers a PublishPolicyProvider and sets it on the event bus.
// Checks in-process modules first, then sidecar modules with "publish.policy" capability.
func WirePublishPolicy(reg *registry.Registry, bus *events.MemoryBus, creds credentials.TransportCredentials, maxMsgBytes int) error {
	entries := reg.FindByCapability("publish.policy")
	if len(entries) == 0 {
		return nil
	}
	entry := entries[0]

	// In-process module: type assertion.
	if pp, ok := entry.Module.(contracts.PublishPolicyProvider); ok {
		bus.SetPublishPolicy(pp)
		slog.Info("publish policy provider loaded from registry", "module", entry.Info.ID)
		return nil
	}

	// Sidecar module: connect via gRPC to the module's HTTPAddr.
	if addr := entry.Info.HTTPAddr; addr != "" {
		conn, err := DialSidecar(addr, creds, maxMsgBytes)
		if err != nil {
			return fmt.Errorf("dial sidecar publish policy %s at %s: %w", entry.Info.ID, addr, err)
		}
		pp := grpcmesh.NewSidecarPublishPolicy(conn)
		bus.SetPublishPolicy(pp)
		slog.Info("publish policy provider loaded from sidecar module",
			"module", entry.Info.ID, "addr", addr)
		return nil
	}

	return fmt.Errorf("publish policy module %q has no HTTPAddr and is not an in-process provider", entry.Info.ID)
}

// WireAuth discovers AuthProvider, Authorizer, and IdentityProvider from
// the registry and wires them into the HTTP server and gRPC auth interceptor.
// Handles both in-process modules (type assertion) and sidecar modules (gRPC).
func WireAuth(reg *registry.Registry, srv *api.Server, authInterceptor *grpcmesh.AuthInterceptor, creds credentials.TransportCredentials, maxMsgBytes int) error {
	// --- Authorizer ---
	if entries := reg.FindByCapability(contracts.CapabilityAuthorizer); len(entries) > 0 {
		entry := entries[0]
		if az, ok := entry.Module.(contracts.Authorizer); ok {
			srv.SetAuthorizer(az)
			authInterceptor.SetAuthorizer(az)
			slog.Info("authorizer loaded from registry", "module", entry.Info.ID)
		} else if addr := entry.Info.HTTPAddr; addr != "" {
			conn, err := DialSidecar(addr, creds, maxMsgBytes)
			if err != nil {
				return fmt.Errorf("dial sidecar authorizer %s at %s: %w", entry.Info.ID, addr, err)
			}
			az := grpcmesh.NewSidecarAuthorizer(conn)
			srv.SetAuthorizer(az)
			authInterceptor.SetAuthorizer(az)
			slog.Info("authorizer loaded from sidecar module", "module", entry.Info.ID, "addr", addr)
		}
	}

	// --- IdentityProvider ---
	if entries := reg.FindByCapability(contracts.CapabilityIdentity); len(entries) > 0 {
		entry := entries[0]
		if ip, ok := entry.Module.(contracts.IdentityProvider); ok {
			authInterceptor.SetIdentityProvider(ip)
			slog.Info("identity provider loaded from registry", "module", entry.Info.ID)
		} else if addr := entry.Info.HTTPAddr; addr != "" {
			conn, err := DialSidecar(addr, creds, maxMsgBytes)
			if err != nil {
				return fmt.Errorf("dial sidecar identity provider %s at %s: %w", entry.Info.ID, addr, err)
			}
			ip := grpcmesh.NewSidecarIdentityProvider(conn)
			authInterceptor.SetIdentityProvider(ip)
			slog.Info("identity provider loaded from sidecar module", "module", entry.Info.ID, "addr", addr)
		}
	}

	// --- AuthProvider ---
	if entries := reg.FindByCapability(contracts.CapabilityAuth); len(entries) > 0 {
		entry := entries[0]
		var ap contracts.AuthProvider
		if p, ok := entry.Module.(contracts.AuthProvider); ok {
			ap = p
			slog.Info("auth provider loaded from registry", "module", entry.Info.ID)
		} else if addr := entry.Info.HTTPAddr; addr != "" {
			conn, err := DialSidecar(addr, creds, maxMsgBytes)
			if err != nil {
				return fmt.Errorf("dial sidecar auth provider %s at %s: %w", entry.Info.ID, addr, err)
			}
			ap = grpcmesh.NewSidecarAuthProvider(conn)
			slog.Info("auth provider loaded from sidecar module", "module", entry.Info.ID, "addr", addr)
		}
		if ap != nil {
			srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
				token := ExtractBearerToken(r)
				if token == "" {
					return nil, fmt.Errorf("missing or malformed Authorization header")
				}
				session, err := ap.Validate(r.Context(), token)
				if err != nil {
					return nil, err
				}
				return &session, nil
			})
		}
	}
	return nil
}

// GRPCPanicRecoveryInterceptor catches panics in gRPC handlers and returns
// an Internal error instead of crashing the process.
func GRPCPanicRecoveryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (_ interface{}, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("gRPC panic recovered",
				"method", info.FullMethod,
			)
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(ctx, req)
}

// GRPCPanicRecoveryStreamInterceptor catches panics in gRPC stream handlers.
func GRPCPanicRecoveryStreamInterceptor(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("gRPC stream panic recovered",
				"method", info.FullMethod,
			)
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(srv, stream)
}

// GRPCLoggingInterceptor logs every gRPC unary call with method, duration, and peer.
// Error messages are sanitized to avoid leaking internal details into logs.
func GRPCLoggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(start)
	level := slog.LevelDebug
	if err != nil {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "gRPC call",
		"method", info.FullMethod,
		"duration", duration,
	)
	return resp, err
}

// GRPCLoggingStreamInterceptor logs gRPC stream start.
func GRPCLoggingStreamInterceptor(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	slog.Debug("gRPC stream started",
		"method", info.FullMethod,
		"is_server_stream", info.IsServerStream,
	)
	err := handler(srv, stream)
	if err != nil {
		slog.Warn("gRPC stream ended",
			"method", info.FullMethod,
		)
	}
	return err
}

// RunConfigReloadLoop starts a goroutine that reloads config on SIGHUP.
func RunConfigReloadLoop(ctx context.Context, sighupCh <-chan os.Signal, configPath string, cfg *config.Config, cfgMu *sync.Mutex, bus contracts.EventBus) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("config reload loop panic recovered", "panic", r)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighupCh:
			}
			slog.Info("SIGHUP received — reloading config", "path", configPath)
			result, err := config.Reload(cfg, configPath)
			if err != nil {
				slog.Error("config reload failed", "error", err)
				continue
			}

			changes := result.Changes
			if changes.LogLevel || changes.LogFormat {
				newLogger := SetupLogger(result.Config.Log)
				slog.SetDefault(newLogger)
				slog.Info("logger reconfigured after reload",
					"level", result.Config.Log.Level,
					"format", result.Config.Log.Format,
				)
			}
			if changes.AuditPath {
				result.Config.Audit.Path = cfg.Audit.Path
				slog.Warn("audit.path changed — restart required for this to take effect",
					"current_path", cfg.Audit.Path,
					"new_path_on_restart", result.Config.Audit.Path,
				)
			}
			if changes.SeedNodes {
				slog.Info("seed nodes changed in config reload", "new_seeds", result.Config.GRPC.SeedNodes)
			}
			if changes.Unsafe {
				slog.Warn("config reload has unsafe changes — restart required",
					"unsafe_fields", changes.UnsafeFields,
				)
			}

			payload, _ := json.Marshal(map[string]any{
				"changes": changes,
			})
			eventCtx := callerid.Set(ctx, "system:muxcored")
			bus.Publish(eventCtx, contracts.Event{
				Type:    contracts.EventConfigReloaded,
				Source:  "muxcored",
				Payload: payload,
			})

			cfgMu.Lock()
			*cfg = *result.Config
			cfgMu.Unlock()
		}
	}()
}

// RunClusterEventListener starts a goroutine that handles cluster events.
func RunClusterEventListener(ctx context.Context, cluster contracts.Cluster, nodeID string, workerPool *workerpool.Pool, modMgr *modulemgr.Manager) {
	go func() { //nolint:gosec // shutdown path — parent ctx is cancelled, need fresh context
		defer func() {
			if r := recover(); r != nil {
				slog.Error("cluster event listener panic recovered", "panic", r)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				workerPool.Shutdown(context.Background())
				return
			case evt, ok := <-cluster.Events():
				if !ok {
					return
				}
				switch evt.Type {
				case contracts.ClusterNodeLeft:
					count := workerPool.FailNodeTasks(evt.Node.ID)
					if count > 0 {
						slog.Warn("workerpool: tasks failed due to node departure",
							"node", evt.Node.ID,
							"count", count,
						)
					}
					if leader := cluster.Leader(); leader != nil && leader.ID == nodeID {
						for _, modID := range evt.Node.ModuleIDs {
							if err := modMgr.ResurrectOrphan(ctx, modID); err != nil {
								slog.Warn("module resurrection failed",
									"module", modID,
									"departed_node", evt.Node.ID,
									"error", err,
								)
							}
						}
					}

				case contracts.ClusterNodeDegraded:
					count := workerPool.FailNodeTasks(evt.Node.ID)
					if count > 0 {
						slog.Warn("workerpool: tasks failed due to node degradation",
							"node", evt.Node.ID,
							"count", count,
						)
					}

				case contracts.ClusterLeaderChanged:
					if evt.Node.ID == nodeID {
						slog.Info("this node is now the cluster leader")
						modMgr.ResurrectPendingOrphans(ctx)
					}
				}
			}
		}
	}()
}

// InitHealthProbes creates the health check function for the HTTP server.
func InitHealthProbes(ctx context.Context, bus *events.MemoryBus, discoveryGrpc *grpcmesh.DiscoveryServer, store *storage.Orchestrator, cfg *config.Config, reg *registry.Registry, cfgMu *sync.Mutex) func() map[string]error {
	startTime := time.Now()
	coreH := corehealth.New()
	coreH.RegisterProbe("event_bus", func(ctx context.Context) error {
		sc := bus.SubscriberCount()
		if sc < 0 {
			return fmt.Errorf("event bus not initialized")
		}
		if dc := bus.DroppedEvents(); dc > 0 {
			return fmt.Errorf("event bus dropped %d events", dc)
		}
		return nil
	})
	coreH.RegisterProbe("discovery", func(ctx context.Context) error {
		ms := discoveryGrpc.MembersSnapshot()
		if len(cfg.GRPC.SeedNodes) > 0 && len(ms) == 0 {
			return fmt.Errorf("seed nodes configured but no cluster peers discovered")
		}
		return nil
	})
	coreH.RegisterProbe("storage", func(ctx context.Context) error {
		pc := store.ProviderCount()
		if pc == 0 {
			return fmt.Errorf("no storage providers registered")
		}
		return nil
	})
	coreH.RegisterProbe("audit", func(ctx context.Context) error {
		cfgMu.Lock()
		auditPath := cfg.Audit.Path
		cfgMu.Unlock()
		if auditPath != "" {
			dir := filepath.Dir(auditPath)
			f, err := os.CreateTemp(dir, ".healthcheck-*")
			if err != nil {
				return errors.New("audit log directory not writable")
			}
			f.Close()
			os.Remove(f.Name())
		}
		return nil
	})

	return func() map[string]error {
		results := make(map[string]error)
		for name, err := range coreH.Check(context.Background()) {
			results["core."+name] = err
		}
		for _, entry := range reg.ListAll() {
			if entry.State == contracts.ModuleStateDegraded {
				results[entry.Info.ID] = fmt.Errorf("module degraded")
			}
		}
		results["_uptime"] = fmt.Errorf("%s", time.Since(startTime).Round(time.Second))
		results["_version"] = fmt.Errorf("ok")
		return results
	}
}

// LoadAndSpawnModules fetches a tag from the spool and spawns all modules.
func LoadAndSpawnModules(ctx context.Context, tagName string, spoolURL string, modMgr *modulemgr.Manager, peerAddrs []string) error {
	if tagName == "" {
		return nil
	}
	slog.Info("fetching tag from spool", "spool", spoolURL, "tag", tagName)
	tag, err := spool.FetchTag(ctx, spoolURL, tagName)
	if err != nil {
		return fmt.Errorf("fetch tag: %w", err)
	}
	slog.Info("tag loaded", "name", tag.Name, "version", tag.Version, "modules", len(tag.Modules))

	// Store the tag on the manager so it can resurrect orphaned modules
	// when cluster nodes depart.
	modMgr.SetTag(tag)

	useWatchdog := len(peerAddrs) > 0

	for _, tm := range tag.Modules {
		bin, err := modMgr.ResolveTagModule(tm)
		if err != nil {
			if tm.Required {
				return fmt.Errorf("resolve required module %s: %w", tm.Repo, err)
			}
			slog.Warn("resolve optional module failed, skipping", "repo", tm.Repo, "error", err)
			continue
		}
		if len(tm.Config) > 0 {
			bin.Config = tm.Config
		}
		if err := modMgr.VerifyChecksum(bin, tm.Checksum); err != nil {
			if tm.Required {
				return fmt.Errorf("checksum verification failed for required module %s: %w", tm.Repo, err)
			}
			slog.Warn("checksum verification failed, skipping optional module",
				"repo", tm.Repo, "id", bin.ID, "error", err)
			continue
		}
		if useWatchdog {
			if err := modMgr.SpawnWithWatchdog(ctx, bin, peerAddrs); err != nil {
				if tm.Required {
					return fmt.Errorf("spawn required module %s via watchdog: %w", bin.ID, err)
				}
				slog.Warn("spawn optional module via watchdog failed, skipping", "id", bin.ID, "error", err)
				continue
			}
		} else {
			if err := modMgr.Spawn(ctx, bin); err != nil {
				if tm.Required {
					return fmt.Errorf("spawn required module %s: %w", bin.ID, err)
				}
				slog.Warn("spawn optional module failed, skipping", "id", bin.ID, "error", err)
				continue
			}
		}
	}
	return nil
}

// AutoJoinSeedNodes joins the cluster via seed nodes.
func AutoJoinSeedNodes(ctx context.Context, cfg *config.Config, creds credentials.TransportCredentials, discoveryGrpc *grpcmesh.DiscoveryServer) {
	if creds == nil {
		if len(cfg.GRPC.SeedNodes) > 0 {
			slog.Warn("auto-join skipped: TLS is required for cluster seed node connections")
		}
		return
	}
	if len(cfg.GRPC.SeedNodes) == 0 {
		return
	}

	slog.Info("auto-joining seed nodes", "seeds", cfg.GRPC.SeedNodes)
	maxMsgBytes := cfg.GRPC.MaxMessageSizeMB * 1024 * 1024
	if maxMsgBytes <= 0 {
		maxMsgBytes = 32 * 1024 * 1024
	}
	localNode := discoveryGrpc.LocalNode()
	for _, seed := range cfg.GRPC.SeedNodes {
		go func(seedAddr string) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("auto-join seed panic recovered", "seed", seedAddr, "panic", r)
				}
			}()
			joinCtx, joinCancel := context.WithTimeout(ctx, 10*time.Second)
			defer joinCancel()
			dialOpts := []grpc.DialOption{
				grpc.WithTransportCredentials(creds),
				grpc.WithDefaultCallOptions(
					grpc.MaxCallRecvMsgSize(maxMsgBytes),
					grpc.MaxCallSendMsgSize(maxMsgBytes),
				),
			}
			conn, err := grpc.NewClient(seedAddr, dialOpts...)
			if err != nil {
				slog.Warn("auto-join: dial seed node", "seed", seedAddr, "error", err)
				return
			}
			defer conn.Close()
			client := discoveryv1.NewDiscoveryServiceClient(conn)
			resp, err := client.Join(joinCtx, &discoveryv1.JoinRequest{Node: localNode})
			if err != nil {
				slog.Warn("auto-join: join request failed", "seed", seedAddr, "error", err)
				return
			}
			slog.Info("auto-join: joined cluster via seed node",
				"seed", seedAddr,
				"cluster_id", resp.ClusterId,
				"leader_id", resp.LeaderId,
				"members", len(resp.Members),
			)
		}(seed)
	}
}
