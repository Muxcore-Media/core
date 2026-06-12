package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const moduleConnectTimeout = 5 * time.Second

func main() {
	var (
		modulePath    string
		moduleID      string
		meshAddrs     string
		checkInterval time.Duration
		disconnectTO  time.Duration
	)
	flag.StringVar(&modulePath, "module-path", "", "path to the module binary (required)")
	flag.StringVar(&moduleID, "module-id", "", "module identifier (required)")
	flag.StringVar(&meshAddrs, "mesh-addrs", "", "comma-separated core gRPC addresses (required)")
	flag.DurationVar(&checkInterval, "check-interval", 5*time.Second, "how often to check core connectivity")
	flag.DurationVar(&disconnectTO, "disconnect-timeout", 15*time.Second, "core disconnection timeout before failover")
	flag.Parse()

	if modulePath == "" || moduleID == "" || meshAddrs == "" {
		fmt.Fprintf(os.Stderr, "usage: muxcore-watchdog --module-path <path> --module-id <id> --mesh-addrs <addrs>\n")
		os.Exit(1)
	}

	addrs := strings.Split(meshAddrs, ",")
	if len(addrs) == 0 {
		fmt.Fprintf(os.Stderr, "at least one mesh address is required\n")
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	w := &watchdog{
		modulePath:     modulePath,
		moduleID:       moduleID,
		addrs:          addrs,
		checkInterval:  checkInterval,
		disconnectTO:   disconnectTO,
		cmd:            new(atomic.Value),
		currentAddrIdx: 0,
	}

	if err := w.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("watchdog failed", "error", err)
		os.Exit(1)
	}
}

type watchdog struct {
	modulePath     string
	moduleID       string
	addrs          []string
	checkInterval  time.Duration
	disconnectTO   time.Duration
	cmd            *atomic.Value // stores *exec.Cmd
	currentAddrIdx int
	failoverCount  atomic.Int64
}

func (w *watchdog) run(ctx context.Context) error {
	if err := w.spawnModule(ctx, w.addrs[w.currentAddrIdx]); err != nil {
		return fmt.Errorf("initial module spawn: %w", err)
	}

	monitorCtx, monitorCancel := context.WithCancel(ctx)
	defer monitorCancel()

	moduleDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				moduleDone <- fmt.Errorf("module wait panic: %v", r)
			}
		}()
		cmdVal := w.cmd.Load()
		cmd, ok := cmdVal.(*exec.Cmd)
		if !ok {
			moduleDone <- fmt.Errorf("unexpected cmd type: %T", cmdVal)
			return
		}
		moduleDone <- cmd.Wait()
	}()

	go w.monitorConnectivity(monitorCtx)

	select {
	case <-ctx.Done():
		slog.Info("watchdog shutting down")
		w.killModule()
		return ctx.Err()
	case err := <-moduleDone:
		return fmt.Errorf("module exited: %w", err)
	}
}

func (w *watchdog) spawnModule(ctx context.Context, addr string) error {
	slog.Info("watchdog: spawning module", "module", w.moduleID, "addr", addr, "path", w.modulePath)

	cmd := exec.CommandContext(ctx, w.modulePath, //nolint:gosec // modulePath from operator CLI flags
		"--muxcore-mesh-addr", addr,
		"--muxcore-module-id", w.moduleID,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start module: %w", err)
	}

	// Wait briefly for the module to start before checking core connectivity.
	// The module needs time to establish its own gRPC connection with core.
	time.Sleep(moduleConnectTimeout)

	w.cmd.Store(cmd)
	slog.Info("watchdog: module spawned", "module", w.moduleID, "pid", cmd.Process.Pid)
	return nil
}

func (w *watchdog) killModule() {
	cmdVal := w.cmd.Load()
	if cmdVal == nil {
		return
	}
	cmd, ok := cmdVal.(*exec.Cmd)
	if !ok {
		slog.Error("watchdog: unexpected cmd type", "type", fmt.Sprintf("%T", cmdVal))
		return
	}
	if cmd.Process == nil {
		return
	}
	slog.Info("watchdog: stopping module", "module", w.moduleID, "pid", cmd.Process.Pid)
	cmd.Process.Signal(os.Interrupt)
	go func() {
		cmd.Wait()
	}()
}

func (w *watchdog) monitorConnectivity(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("watchdog connectivity monitor panic recovered", "module", w.moduleID, "panic", r)
		}
	}()
	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	disconnectedSince := time.Time{}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		connected := w.checkCore(ctx)

		if connected {
			disconnectedSince = time.Time{}
			w.failoverCount.Store(0)
			continue
		}

		if disconnectedSince.IsZero() {
			disconnectedSince = time.Now()
			slog.Warn("watchdog: lost connection to core", "module", w.moduleID)
			continue
		}

		if time.Since(disconnectedSince) >= w.disconnectTO {
			slog.Warn("watchdog: core disconnected for too long, failing over",
				"module", w.moduleID,
				"timeout", w.disconnectTO,
			)
			w.failover(ctx)
			disconnectedSince = time.Time{}
		}
	}
}

func (w *watchdog) checkCore(ctx context.Context) bool {
	addr := w.addrs[w.currentAddrIdx]
	dialer := net.Dialer{Timeout: w.checkInterval / 2}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	defer conn.Close()

	// Write HTTP/2 connection preface to verify this is a gRPC server.
	// A gRPC server responds with a SETTINGS frame; anything else means
	// the port is open but not serving gRPC.
	preface := []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(preface); err != nil {
		return false
	}
	// Read enough for a minimal gRPC SETTINGS response.
	buf := make([]byte, 32)
	if _, err := conn.Read(buf); err != nil {
		return false
	}
	// HTTP/2 frame starts with a length and type byte; frame type 4 = SETTINGS.
	if len(buf) < 4 || buf[3] != 4 {
		return false
	}
	return true
}

func (w *watchdog) failover(ctx context.Context) {
	w.killModule()

	// Exponential backoff on failover: 1s, 2s, 4s, 8s, capped at 30s.
	attempts := w.failoverCount.Add(1)
	backoff := time.Duration(math.Min(float64(int(1)<<(attempts-1)), 30)) * time.Second

	// Move to next address.
	w.currentAddrIdx = (w.currentAddrIdx + 1) % len(w.addrs)
	addr := w.addrs[w.currentAddrIdx]

	slog.Info("watchdog: failing over",
		"module", w.moduleID,
		"new_addr", addr,
		"attempt", attempts,
		"backoff", backoff,
	)

	select {
	case <-ctx.Done():
		return
	case <-time.After(backoff):
	}

	if err := w.spawnModule(ctx, addr); err != nil {
		slog.Error("watchdog: failover spawn failed",
			"module", w.moduleID,
			"error", err,
			"attempt", attempts,
		)
	}
}
