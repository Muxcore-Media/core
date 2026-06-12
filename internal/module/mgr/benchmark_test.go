package mgr

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkResolve_WarmCache(b *testing.B) {
	dir := b.TempDir()
	cacheDir := filepath.Join(dir, "modules")

	// Seed the cache with a pre-built binary at the expected path.
	modID := "test-module"
	version := "v1.0.0"
	binDir := filepath.Join(cacheDir, modID, version)
	if err := os.MkdirAll(binDir, 0700); err != nil {
		b.Fatal(err)
	}
	cachedBin := filepath.Join(binDir, "muxcore-module")
	if err := os.WriteFile(cachedBin, []byte("mock binary content"), 0600); err != nil {
		b.Fatal(err)
	}

	m := &Manager{cacheDir: cacheDir}
	repoURL := fmt.Sprintf("https://github.com/owner/%s", modID)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bin, err := m.Resolve(repoURL, version)
		if err != nil {
			b.Fatal(err)
		}
		if bin.Path != cachedBin {
			b.Fatalf("expected cached path %q, got %q", cachedBin, bin.Path)
		}
	}
}

func BenchmarkResolve_InvalidVersion(b *testing.B) {
	m := &Manager{}
	badVersions := []string{"", "not-semver", "latest", "master"}

	for _, v := range badVersions {
		b.Run(fmt.Sprintf("version=%q", v), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, err := m.Resolve("https://github.com/owner/mod", v)
				if err == nil {
					b.Fatal("expected error for invalid version")
				}
			}
		})
	}
}

func BenchmarkResolve_DisallowedHost(b *testing.B) {
	m := &Manager{
		allowedRepoHosts: []string{"github.com"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := m.Resolve("https://evil.com/owner/mod", "v1.0.0")
		if err == nil {
			b.Fatal("expected error for disallowed host")
		}
	}
}

func BenchmarkPruneCache(b *testing.B) {
	for _, keep := range []int{1, 3, 5} {
		b.Run(fmt.Sprintf("keep=%d", keep), func(b *testing.B) {
			dir := b.TempDir()
			cacheDir := filepath.Join(dir, "modules")

			// Create 10 cached versions of a module.
			modID := "test-module"
			for v := 0; v < 10; v++ {
				version := fmt.Sprintf("v1.%d.0", v)
				binDir := filepath.Join(cacheDir, modID, version)
				os.MkdirAll(binDir, 0700)
				os.WriteFile(filepath.Join(binDir, "muxcore-module"), []byte("content"), 0600) //nolint:errcheck // benchmark setup
			}

			m := &Manager{cacheDir: cacheDir}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := m.PruneCache(keep); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
