package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/storage"
)

const testAddr = "127.0.0.1:19876"
const testURL = "http://" + testAddr

func TestBareLoomBoot(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")

	cfg := config.Default()
	cfg.Server.Addr = testAddr

	reg := registry.New()
	srv := api.NewServer(cfg.Server.Addr, "", "")

	store := storage.NewOrchestrator(reg)
	store.DiscoverStorage()
	store.DiscoverCache()

	srv.SetHealthChecker(func() map[string]error {
		return nil
	})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()
	time.Sleep(500 * time.Millisecond)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, testURL+"/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health: expected 200, got %d", resp.StatusCode)
	}
	var healthResp struct {
		Status  string            `json:"status"`
		Modules map[string]string `json:"modules"`
	}
	json.NewDecoder(resp.Body).Decode(&healthResp)
	if healthResp.Status != "ok" {
		t.Errorf("health status: expected ok, got %q", healthResp.Status)
	}
	t.Logf("bare loom health: %s (modules: %d)", healthResp.Status, len(healthResp.Modules))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv.Shutdown(ctx)
	<-errCh
	t.Log("bare loom shut down cleanly")
}
