package bootstrap

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{"valid", "Bearer abc123", "abc123"},
		{"missing", "", ""},
		{"basic", "Basic abc123", ""},
		{"empty bearer", "Bearer ", ""},
		{"case insensitive", "bearer abc123", "abc123"},
		{"BEARING uppercase", "BEARER abc123", "abc123"},
		{"no space", "Bearerabc123", ""},
		{"only bearer", "Bearer", ""},
		{"with spaces in token", "Bearer abc 123", "abc 123"},
		{"special chars", "Bearer abc!@#$%", "abc!@#$%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			got := ExtractBearerToken(req)
			if got != tt.expected {
				t.Errorf("ExtractBearerToken() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestIsLocalhostAddr(t *testing.T) {
	tests := []struct {
		addr     string
		expected bool
	}{
		{"localhost:8080", true},
		{"127.0.0.1:9090", true},
		{"[::1]:8080", true},
		{"0.0.0.0:8080", false},
		{"192.168.1.1:8080", false},
		{"", true},
		{"localhost", true},
		{"127.0.0.1", true},
		{"example.com:443", false},
		{"10.0.0.1:80", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got := IsLocalhostAddr(tt.addr)
			if got != tt.expected {
				t.Errorf("IsLocalhostAddr(%q) = %v, want %v", tt.addr, got, tt.expected)
			}
		})
	}
}

func TestSetupLogger(t *testing.T) {
	tests := []struct {
		name   string
		config config.LogConfig
	}{
		{"default", config.LogConfig{}},
		{"debug", config.LogConfig{Level: "debug"}},
		{"warn", config.LogConfig{Level: "warn"}},
		{"error", config.LogConfig{Level: "error"}},
		{"json", config.LogConfig{Format: "json"}},
		{"text", config.LogConfig{Format: "text"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := SetupLogger(tt.config)
			if logger == nil {
				t.Error("SetupLogger() returned nil")
			}
		})
	}
}

func TestDevTLSSkipCheck(t *testing.T) {
	// Save and restore env
	old1 := os.Getenv("MUXCORE_INSECURE_DISABLE_TLS")
	old2 := os.Getenv("MUXCORE_DEV_TLS_SKIP")
	defer func() {
		os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", old1)
		os.Setenv("MUXCORE_DEV_TLS_SKIP", old2)
	}()

	tests := []struct {
		name     string
		env1     string
		env2     string
		expected bool
	}{
		{"default", "", "", false},
		{"canonical true", "true", "", true},
		{"canonical 1", "1", "", true},
		{"deprecated true", "", "true", true},
		{"deprecated 1", "", "1", true},
		{"canonical false", "false", "", false},
		{"both set", "true", "true", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", tt.env1)
			os.Setenv("MUXCORE_DEV_TLS_SKIP", tt.env2)
			got := DevTLSSkipCheck()
			if got != tt.expected {
				t.Errorf("DevTLSSkipCheck() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDialSidecar(t *testing.T) {
	t.Run("localhost without TLS", func(t *testing.T) {
		conn, err := DialSidecar("localhost:8080", nil, 32*1024*1024)
		if err != nil {
			t.Errorf("DialSidecar() error = %v", err)
		}
		if conn != nil {
			conn.Close()
		}
	})

	t.Run("non-localhost without TLS", func(t *testing.T) {
		_, err := DialSidecar("example.com:443", nil, 32*1024*1024)
		if err == nil {
			t.Error("DialSidecar() expected error for non-localhost without TLS")
		}
	})

	t.Run("custom message size", func(t *testing.T) {
		conn, err := DialSidecar("localhost:8080", nil, 64*1024*1024)
		if err != nil {
			t.Errorf("DialSidecar() with custom size error = %v", err)
		}
		if conn != nil {
			conn.Close()
		}
	})

	t.Run("zero message size uses default", func(t *testing.T) {
		conn, err := DialSidecar("localhost:8080", nil, 0)
		if err != nil {
			t.Errorf("DialSidecar() with zero size error = %v", err)
		}
		if conn != nil {
			conn.Close()
		}
	})
}

func TestGRPCInterceptors(t *testing.T) {
	t.Run("panic recovery unary", func(t *testing.T) {
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			panic("test panic")
		}
		info := &grpc.UnaryServerInfo{FullMethod: "/test"}
		_, err := GRPCPanicRecoveryInterceptor(context.Background(), nil, info, handler)
		if err == nil {
			t.Error("expected error from panic recovery")
		}
		if status.Code(err) != codes.Internal {
			t.Errorf("expected Internal code, got %v", status.Code(err))
		}
	})

	t.Run("panic recovery stream", func(t *testing.T) {
		handler := func(srv interface{}, stream grpc.ServerStream) error {
			panic("test panic")
		}
		info := &grpc.StreamServerInfo{FullMethod: "/test"}
		err := GRPCPanicRecoveryStreamInterceptor(nil, nil, info, handler)
		if err == nil {
			t.Error("expected error from panic recovery")
		}
		if status.Code(err) != codes.Internal {
			t.Errorf("expected Internal code, got %v", status.Code(err))
		}
	})

	t.Run("logging unary", func(t *testing.T) {
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			return "response", nil
		}
		info := &grpc.UnaryServerInfo{FullMethod: "/test"}
		resp, err := GRPCLoggingInterceptor(context.Background(), nil, info, handler)
		if err != nil {
			t.Errorf("GRPCLoggingInterceptor() error = %v", err)
		}
		if resp != "response" {
			t.Errorf("expected 'response', got %v", resp)
		}
	})
}

func TestWireCallPolicy(t *testing.T) {
	t.Run("no modules", func(t *testing.T) {
		reg := registry.New()
		meshClient := grpcmesh.NewClient(grpcmesh.NewServer())
		storageGrpc := grpcmesh.NewStorageServer(nil)
		err := WireCallPolicy(reg, meshClient, storageGrpc, nil, 32*1024*1024)
		if err != nil {
			t.Errorf("WireCallPolicy() error = %v", err)
		}
	})
}

func TestWirePublishPolicy(t *testing.T) {
	t.Run("no modules", func(t *testing.T) {
		reg := registry.New()
		bus := events.NewMemoryBus()
		err := WirePublishPolicy(reg, bus, nil, 32*1024*1024)
		if err != nil {
			t.Errorf("WirePublishPolicy() error = %v", err)
		}
	})
}

func TestWireAuth(t *testing.T) {
	t.Run("no modules", func(t *testing.T) {
		reg := registry.New()
		srv := api.NewServer(":0", "", "")
		authInterceptor := grpcmesh.NewAuthInterceptor()
		err := WireAuth(reg, srv, authInterceptor, nil, 32*1024*1024)
		if err != nil {
			t.Errorf("WireAuth() error = %v", err)
		}
	})
}

func TestLoadAndSpawnModules(t *testing.T) {
	t.Run("empty tag", func(t *testing.T) {
		err := LoadAndSpawnModules(context.Background(), "", "http://example.com", nil, nil)
		if err != nil {
			t.Errorf("LoadAndSpawnModules() error = %v", err)
		}
	})
}

func TestRunConfigReloadLoop(t *testing.T) {
	t.Run("stops on context cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		sighupCh := make(chan os.Signal, 1)
		cfg := &config.Config{}
		cfgMu := &sync.Mutex{}
		bus := events.NewMemoryBus()

		RunConfigReloadLoop(ctx, sighupCh, "test.json", cfg, cfgMu, bus)
		cancel()
		time.Sleep(10 * time.Millisecond)
	})
}

func TestInitHealthProbes(t *testing.T) {
	t.Run("returns function", func(t *testing.T) {
		ctx := context.Background()
		bus := events.NewMemoryBus()
		discoveryGrpc := grpcmesh.NewDiscoveryServer("node1", ":0", ":0", "", func() ([]string, map[string]string) {
			return nil, nil
		})
		store := storage.NewOrchestrator(registry.New())
		cfg := &config.Config{}
		reg := registry.New()
		cfgMu := &sync.Mutex{}

		checker := InitHealthProbes(ctx, bus, discoveryGrpc, store, cfg, reg, cfgMu)
		if checker == nil {
			t.Error("InitHealthProbes() returned nil")
		}

		results := checker()
		if results == nil {
			t.Error("health checker returned nil results")
		}
		if _, ok := results["_version"]; !ok {
			t.Error("expected _version in results")
		}
	})
}
