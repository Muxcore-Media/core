package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/module"
	"github.com/Muxcore-Media/core/internal/trace"
	_ "github.com/Muxcore-Media/core/internal/presets" // build-tag-gated module selection
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/storage"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/google/uuid"
)

func main() {
	configPath := os.Getenv("MUXCORE_CONFIG")
	if configPath == "" {
		configPath = "muxcore.json"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("no config file found, using defaults", "path", configPath)
			cfg = config.Default()
		} else {
			slog.Error("load config", "error", err)
			os.Exit(1)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := setupLogger(cfg.Log)
	slog.SetDefault(logger)

	slog.Info("MuxCore starting...")

	tracer, traceShutdown, err := trace.InitProvider(cfg.Trace)
	if err != nil {
		slog.Warn("trace init failed, using noop", "error", err)
		tracer = trace.NewNoopTracer()
		traceShutdown = func(ctx context.Context) error { return nil }
	}

	bus := events.NewMemoryBus()
	slog.Info("event bus ready", "type", "memory")

	reg := registry.New()
	mgr := module.NewManager(reg)

	srv := api.NewServer(cfg.Server.Addr)

	store := storage.NewOrchestrator(reg)
	if err := store.Discover(); err != nil {
		slog.Warn("storage discover", "error", err)
	}
	cache := storage.NewMemoryCache()
	store.SetCache(cache)
	slog.Info("storage orchestrator ready", "providers", store.ProviderCount())

	// Cluster is nil until a cluster module is discovered.
	deps := contracts.ModuleDeps{
		Registry: reg,
		EventBus: bus,
		Routes:   srv,
		Cluster:  nil,
		Storage:  store,
		Tracer:   tracer,
	}

	modules := contracts.LoadRegistered(deps)

	// Discover infrastructure modules.
	var cl contracts.Cluster
	var wp contracts.WorkerPool
	var al contracts.AuditLogger
	for _, mod := range modules {
		if c, ok := mod.(contracts.Cluster); ok {
			cl = c
		}
		if w, ok := mod.(contracts.WorkerPool); ok {
			wp = w
		}
		if a, ok := mod.(contracts.AuditLogger); ok {
			al = a
		}
	}
	deps.Cluster = cl
	deps.WorkerPool = wp
	deps.Audit = al

	for _, mod := range modules {
		if err := mgr.Register(mod, nil); err != nil {
			slog.Error("register module", "id", mod.Info().ID, "error", err)
			os.Exit(1)
		}
	}

	slog.Info("module registry ready", "count", reg.Count())

	authModules := reg.FindByKind(contracts.ModuleKindAuth)
	if len(authModules) > 0 {
		if provider, ok := authModules[0].Module.(contracts.AuthProvider); ok {
			srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
				token := r.Header.Get("Authorization")
				if token == "" {
					return nil, fmt.Errorf("missing Authorization header")
				}
				token = strings.TrimPrefix(token, "Bearer ")
				session, err := provider.Validate(r.Context(), token)
				if err != nil {
					return nil, err
				}
				return &session, nil
			})
			slog.Info("auth middleware enabled", "provider", authModules[0].Info.ID)
		}
	}

	srv.SetHealthChecker(func() map[string]error {
		return mgr.HealthCheck(context.Background())
	})

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			slog.Error("api server", "error", err)
			os.Exit(1)
		}
	}()

	// Start cluster early so other modules can use it during Init/Start.
	if cl != nil {
		if err := cl.Start(ctx); err != nil {
			slog.Error("cluster start", "error", err)
			os.Exit(1)
		}
		slog.Info("cluster started", "node_id", cl.LocalNode().ID)
	}

	if err := mgr.InitAll(ctx); err != nil {
		slog.Error("init modules", "error", err)
		os.Exit(1)
	}
	if err := mgr.StartAll(ctx); err != nil {
		slog.Error("start modules", "error", err)
		os.Exit(1)
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				results := mgr.HealthCheck(context.Background())
				for id, err := range results {
					if err != nil {
						slog.Warn("module degraded", "id", id, "error", err)
						payload, _ := json.Marshal(map[string]string{"module_id": id, "error": err.Error()})
						bus.Publish(context.Background(), contracts.Event{
							ID:        uuid.New().String(),
							Type:      contracts.EventModuleDegraded,
							Source:    "core",
							Payload:   payload,
							Timestamp: time.Now(),
						})
					}
				}
			}
		}
	}()

	slog.Info("MuxCore running", "addr", cfg.Server.Addr)

	<-ctx.Done()
	slog.Info("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("api shutdown", "error", err)
	}
	if err := mgr.StopAll(shutdownCtx); err != nil {
		slog.Error("module shutdown", "error", err)
	}
	if cl != nil {
		if err := cl.Stop(shutdownCtx); err != nil {
			slog.Error("cluster shutdown", "error", err)
		}
	}
	if err := traceShutdown(shutdownCtx); err != nil {
		slog.Error("trace shutdown", "error", err)
	}
	slog.Info("MuxCore stopped.")
}

func setupLogger(lc config.LogConfig) *slog.Logger {
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
