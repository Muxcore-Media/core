package grpcmesh

import (
	"context"
	"fmt"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// echoBenchHandler returns the payload unchanged.
type echoBenchHandler struct{}

func (echoBenchHandler) HandleCall(_ context.Context, _ string, payload []byte) ([]byte, error) {
	return payload, nil
}

func BenchmarkMeshCall_Local(b *testing.B) {
	srv := NewServer()
	payloadSizes := []int{64, 1024, 64 * 1024, 1024 * 1024}

	for _, size := range payloadSizes {
		b.Run(fmt.Sprintf("payload=%d", size), func(b *testing.B) {
			srv.RegisterHandler("bench-mod", echoBenchHandler{})
			client := NewClient(srv)
			client.SetCallPolicy(allowAllCallPolicy{})

			payload := make([]byte, size)
			ctx := context.Background()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := client.Call(ctx, "bench-mod", "Echo", payload)
				if err != nil {
					b.Fatal(err)
				}
				if len(resp) != size {
					b.Fatalf("expected response length %d, got %d", size, len(resp))
				}
			}
		})
	}
}

// allowAllCallPolicy permits every inter-module call.
type allowAllCallPolicy struct{}

func (allowAllCallPolicy) AllowCall(_ context.Context, _, _, _ string) (bool, error) {
	return true, nil
}

var _ contracts.CallPolicyProvider = allowAllCallPolicy{}
