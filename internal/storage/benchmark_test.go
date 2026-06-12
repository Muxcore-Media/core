package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func BenchmarkOrchestratorPut(b *testing.B) {
	for _, size := range []string{"1KB", "10KB", "100KB"} {
		b.Run(fmt.Sprintf("size=%s", size), func(b *testing.B) {
			var dataSize int
			switch size {
			case "1KB":
				dataSize = 1024
			case "10KB":
				dataSize = 10 * 1024
			case "100KB":
				dataSize = 100 * 1024
			}

			reg := newMockRegistry()
			prov := newMockProvider("local")
			reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
			orch := NewOrchestrator(reg)
			orch.DiscoverStorage()
			ctx := context.Background()

			data := make([]byte, dataSize)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := orch.Put(ctx, "bench/key", bytes.NewReader(data), int64(len(data))); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkOrchestratorGet(b *testing.B) {
	for _, size := range []string{"1KB", "10KB", "100KB"} {
		b.Run(fmt.Sprintf("size=%s", size), func(b *testing.B) {
			reg := newMockRegistry()
			prov := newMockProvider("local")
			reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
			orch := NewOrchestrator(reg)
			orch.DiscoverStorage()
			ctx := context.Background()

			var dataSize int
			switch size {
			case "1KB":
				dataSize = 1024
			case "10KB":
				dataSize = 10 * 1024
			case "100KB":
				dataSize = 100 * 1024
			}
			data := make([]byte, dataSize)
			if err := orch.Put(ctx, "bench/key", bytes.NewReader(data), int64(len(data))); err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rc, err := orch.Get(ctx, "bench/key")
				if err != nil {
					b.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, rc)
				rc.Close()
			}
		})
	}
}

func BenchmarkOrchestratorPutGet(b *testing.B) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	data := []byte("benchmark data for put-get round trip")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("bench/key-%d", i)
		if err := orch.Put(ctx, key, bytes.NewReader(data), int64(len(data))); err != nil {
			b.Fatal(err)
		}
		rc, err := orch.Get(ctx, key)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, rc)
		rc.Close()
	}
}

func BenchmarkOrchestratorExists(b *testing.B) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	orch.Put(ctx, "bench/exists", bytes.NewReader([]byte("x")), 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := orch.Exists(ctx, "bench/exists")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOrchestratorList(b *testing.B) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("bench/list/item-%d", i)
		orch.Put(ctx, key, bytes.NewReader([]byte("data")), 4)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := orch.List(ctx, "bench/list/")
		if err != nil {
			b.Fatal(err)
		}
	}
}
