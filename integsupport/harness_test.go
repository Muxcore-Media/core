package integsupport

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

func TestNewCoreHarnessServesOnLoopback(t *testing.T) {
	h := NewCoreHarness(t)
	if h.Addr == "" || h.Bus == nil || h.Context() == nil {
		t.Fatalf("harness not fully initialised: %+v", h)
	}

	conn, err := grpc.NewClient(h.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.Connect()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for s := conn.GetState(); s != connectivity.Ready; s = conn.GetState() {
		if !conn.WaitForStateChange(ctx, s) {
			t.Fatalf("connection never became ready (state %s)", s)
		}
	}
}

func TestCloseCancelsContextAndIsIdempotent(t *testing.T) {
	h := NewCoreHarness(t)
	h.Close()
	select {
	case <-h.Context().Done():
	default:
		t.Fatal("context not cancelled after Close")
	}
	h.Close() // also invoked by t.Cleanup; must not panic
}
