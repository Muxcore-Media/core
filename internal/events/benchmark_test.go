package events

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

func BenchmarkMemoryBusPublish(b *testing.B) {
	bus := NewMemoryBus()
	defer bus.Close()
	bus.SetPublishPolicy(permissivePolicy{})

	ctx := callerid.Set(context.Background(), "bench")
	event := contracts.Event{
		Type:   "bench.test",
		Source: "bench",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := bus.Publish(ctx, event); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryBusSubscribe(b *testing.B) {
	bus := NewMemoryBus()
	defer bus.Close()
	bus.SetPublishPolicy(permissivePolicy{})

	handler := func(_ context.Context, _ contracts.Event) error { return nil }

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cancel, err := bus.Subscribe(context.Background(), "bench.test", handler)
		if err != nil {
			b.Fatal(err)
		}
		cancel()
	}
}

func BenchmarkMemoryBusPublishWithSubscribers(b *testing.B) {
	for _, n := range []int{0, 1, 10, 100} {
		b.Run(fmt.Sprintf("subscribers=%d", n), func(b *testing.B) {
			bus := NewMemoryBus()
			defer bus.Close()
			bus.SetPublishPolicy(permissivePolicy{})

			for i := 0; i < n; i++ {
				_, err := bus.Subscribe(context.Background(), "bench.*",
					func(_ context.Context, _ contracts.Event) error { return nil },
				)
				if err != nil {
					b.Fatal(err)
				}
			}

			ctx := callerid.Set(context.Background(), "bench")
			event := contracts.Event{
				Type:   "bench.test",
				Source: "bench",
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := bus.Publish(ctx, event); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMemoryBusPublishWithPayload(b *testing.B) {
	for _, size := range []string{"64B", "1KB", "16KB"} {
		b.Run(fmt.Sprintf("payload=%s", size), func(b *testing.B) {
			bus := NewMemoryBus()
			defer bus.Close()
			bus.SetPublishPolicy(permissivePolicy{})

			var payload []byte
			switch size {
			case "64B":
				payload = make([]byte, 64)
			case "1KB":
				payload = make([]byte, 1024)
			case "16KB":
				payload = make([]byte, 16*1024)
			}

			ctx := callerid.Set(context.Background(), "bench")
			event := contracts.Event{
				Type:    "bench.payload",
				Source:  "bench",
				Payload: payload,
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := bus.Publish(ctx, event); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMemoryBusRequestReply(b *testing.B) {
	bus := NewMemoryBus()
	defer bus.Close()
	bus.SetPublishPolicy(permissivePolicy{})

	_, err := bus.Subscribe(context.Background(), "bench.req",
		func(ctx context.Context, e contracts.Event) error {
			return bus.Publish(ctx, contracts.Event{
				Type: contracts.ReplyEventType("bench.req"),
			})
		},
	)
	if err != nil {
		b.Fatal(err)
	}

	ctx := callerid.Set(context.Background(), "bench")
	event := contracts.Event{Type: "bench.req", Source: "bench"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := bus.Request(ctx, event, 5*time.Second)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWALWrite(b *testing.B) {
	dir := b.TempDir()
	w, err := NewWALWriter(filepath.Join(dir, "wal"))
	if err != nil {
		b.Fatalf("NewWALWriter: %v", err)
	}
	defer w.Close()

	for _, size := range []string{"64B", "1KB", "64KB"} {
		b.Run(fmt.Sprintf("payload=%s", size), func(b *testing.B) {
			var payload []byte
			switch size {
			case "64B":
				payload = make([]byte, 64)
			case "1KB":
				payload = make([]byte, 1024)
			case "64KB":
				payload = make([]byte, 64*1024)
			}

			event := contracts.Event{
				Type:    "bench.wal",
				Source:  "bench",
				Payload: payload,
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := w.Write(event); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkWALWriteBatched(b *testing.B) {
	batchSizes := []int{1, 10, 100}
	for _, batch := range batchSizes {
		b.Run(fmt.Sprintf("batch=%d", batch), func(b *testing.B) {
			dir := b.TempDir()
			w, err := NewWALWriter(filepath.Join(dir, "wal"))
			if err != nil {
				b.Fatalf("NewWALWriter: %v", err)
			}
			defer w.Close()

			event := contracts.Event{
				Type:    "bench.wal.batch",
				Source:  "bench",
				Payload: make([]byte, 256),
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < batch; j++ {
					if _, err := w.Write(event); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
