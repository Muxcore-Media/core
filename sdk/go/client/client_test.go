package client

import (
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestDial_WithInsecure(t *testing.T) {
	// grpc.NewClient doesn't eagerly connect, so this works without a server.
	c, err := Dial("localhost:19999", WithInsecure())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer c.Close()

	if c.Discovery == nil {
		t.Error("expected Discovery client to be initialised")
	}
	if c.Events == nil {
		t.Error("expected Events client to be initialised")
	}
	if c.Storage == nil {
		t.Error("expected Storage client to be initialised")
	}
	if c.Health == nil {
		t.Error("expected Health client to be initialised")
	}
	if c.Mesh == nil {
		t.Error("expected Mesh client to be initialised")
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
