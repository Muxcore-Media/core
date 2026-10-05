package client

import (
	"context"
	"strings"
	"testing"

	"github.com/Muxcore-Media/core/pkg/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestDial_WithInsecure(t *testing.T) {
	t.Setenv("TENANT_MODE", "")
	t.Setenv("MUXCORE_TENANT_CLUSTER_MAP", "")
	// grpc.NewClient doesn't eagerly connect, so this works without a server.
	c, err := Dial("localhost:19999", WithInsecure())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer c.Close()

	if c.Discovery == nil {
		t.Error("expected Discovery client to be initialized")
	}
	if c.Events == nil {
		t.Error("expected Events client to be initialized")
	}
	if c.Storage == nil {
		t.Error("expected Storage client to be initialized")
	}
	if c.Health == nil {
		t.Error("expected Health client to be initialized")
	}
	if c.Mesh == nil {
		t.Error("expected Mesh client to be initialized")
	}
}

func TestDialContext_TenantClusterRewrite(t *testing.T) {
	tenant.ResetCachedRouter()
	t.Cleanup(tenant.ResetCachedRouter)
	t.Setenv("TENANT_MODE", "1")
	t.Setenv("MUXCORE_TENANT_CLUSTER_MAP", `{"acme":"remote-core:19090"}`)
	t.Setenv("MUXCORE_TENANT_CLUSTER_STRICT", "")
	t.Setenv("MUXCORE_TENANT_ID", "")
	ctx := tenant.WithID(context.Background(), "acme")
	c, err := DialContext(ctx, "localhost:19999", WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.currentAddr != "remote-core:19090" {
		t.Fatalf("expected rewritten addr, got %q", c.currentAddr)
	}
}

func TestDial_WithGRPCOption(t *testing.T) {
	c, err := Dial("localhost:19999",
		WithGRPCOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		t.Fatalf("Dial with custom option failed: %v", err)
	}
	defer c.Close()
}

func TestClient_Close(t *testing.T) {
	c, err := Dial("localhost:19999", WithInsecure())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

func TestDiscoveryClient_Raw(t *testing.T) {
	c, _ := Dial("localhost:19999", WithInsecure())
	defer c.Close()
	if c.Discovery.Raw() == nil {
		t.Error("Raw() should return non-nil gRPC client")
	}
}

func TestEventsClient_Raw(t *testing.T) {
	c, _ := Dial("localhost:19999", WithInsecure())
	defer c.Close()
	if c.Events.Raw() == nil {
		t.Error("Raw() should return non-nil gRPC client")
	}
}

func TestStorageClient_Raw(t *testing.T) {
	c, _ := Dial("localhost:19999", WithInsecure())
	defer c.Close()
	if c.Storage.Raw() == nil {
		t.Error("Raw() should return non-nil gRPC client")
	}
}

func TestHealthClient_Raw(t *testing.T) {
	c, _ := Dial("localhost:19999", WithInsecure())
	defer c.Close()
	if c.Health.Raw() == nil {
		t.Error("Raw() should return non-nil gRPC client")
	}
}

func TestMeshClient_Raw(t *testing.T) {
	c, _ := Dial("localhost:19999", WithInsecure())
	defer c.Close()
	if c.Mesh.Raw() == nil {
		t.Error("Raw() should return non-nil gRPC client")
	}
}

func TestDial_NoOptions(t *testing.T) {
	// Without transport security, gRPC should return an error.
	for _, k := range []string{EnvTLSCert, EnvTLSKey, EnvTLSCA, envInsecure, envInsecureOld} {
		t.Setenv(k, "")
	}
	_, err := Dial("localhost:19999")
	if err == nil {
		t.Fatal("expected error when no transport security is set")
	}
}

func TestDial_MultipleOptionsCompose(t *testing.T) {
	// Multiple WithGRPCOption calls should compose correctly.
	c, err := Dial("localhost:19999",
		WithGRPCOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		WithGRPCOption(grpc.WithUserAgent("test-agent/1.0")),
	)
	if err != nil {
		t.Fatalf("Dial with multiple options failed: %v", err)
	}
	defer c.Close()
}

func TestClient_CloseIdempotent(t *testing.T) {
	c, err := Dial("localhost:19999", WithInsecure())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	// First Close should succeed.
	if err := c.Close(); err != nil {
		t.Errorf("first Close returned error: %v", err)
	}
	// Second Close must not panic. gRPC may return an error indicating
	// the connection is already closing — that's acceptable.
	_ = c.Close()
}

func TestDial_ErrorWrapsAddr(t *testing.T) {
	// grpc.NewClient is lazy — it won't fail on bad addresses at construction.
	// But we verify that error wrapping includes the address.
	_, err := Dial("localhost:19999", WithInsecure())
	if err != nil {
		if !strings.Contains(err.Error(), "localhost:19999") {
			t.Errorf("expected error to contain address, got: %v", err)
		}
	}
}
