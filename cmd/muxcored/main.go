package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/module"
	_ "github.com/Muxcore-Media/core/internal/presets" // build-tag-gated module selection
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := setupLogger()
	slog.SetDefault(logger)

	slog.Info("MuxCore starting...")

	// Event bus
	bus := events.NewMemoryBus()
	slog.Info("event bus ready", "type", "memory")

	// Module registry + manager
	reg := registry.New()
	mgr := module.NewManager(reg)

	// API server (core provides only /health — all other routes come from modules)
	srv := api.NewServer(envOrDefault("MUXCORE_ADDR", ":8080"))

	// Build module dependencies
	deps := contracts.ModuleDeps{
		Registry: reg,
		EventBus: bus,
		Routes:   srv,
	}

	// Auto-load modules registered via init()
	for _, mod := range contracts.LoadRegistered(deps) {
		if err := mgr.Register(mod, nil); err != nil {
			slog.Error("register module", "id", mod.Info().ID, "error", err)
			os.Exit(1)
		}
	}

	slog.Info("module registry ready", "count", reg.Count())

	// Start API server
	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			slog.Error("api server", "error", err)
			os.Exit(1)
		}
	}()

	// Init + start all modules
	if err := mgr.InitAll(ctx); err != nil {
		slog.Error("init modules", "error", err)
		os.Exit(1)
	}
	if err := mgr.StartAll(ctx); err != nil {
		slog.Error("start modules", "error", err)
		os.Exit(1)
	}

	slog.Info("MuxCore running", "addr", envOrDefault("MUXCORE_ADDR", ":8080"))

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
	slog.Info("MuxCore stopped.")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func setupLogger() *slog.Logger {
	level := slog.LevelInfo
	if v := os.Getenv("MUXCORE_LOG_LEVEL"); v != "" {
		switch v {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if os.Getenv("MUXCORE_LOG_FORMAT") == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
