package mgr

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	goLineRe      = regexp.MustCompile(`^go\s+(\S+)\s*$`)
	goVersionRe   = regexp.MustCompile(`^\d+\.\d+(\.\d+)?([a-z]+\d+)?$`)
	goMajorMinorR = regexp.MustCompile(`^\d+\.\d+$`)
)

// goToolchainFromGoMod returns the GOTOOLCHAIN value ("go<V>") for the go.mod
// content: V is the version on the `go` line, with ".0" appended when the line
// has no patch number (ADR-0012 §1). It returns an error when there is no
// valid go line.
func goToolchainFromGoMod(content string) (string, error) {
	for _, line := range strings.Split(content, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		m := goLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v := m[1]
		if !goVersionRe.MatchString(v) {
			return "", fmt.Errorf("invalid go version %q in go.mod", v)
		}
		if goMajorMinorR.MatchString(v) {
			v += ".0"
		}
		return "go" + v, nil
	}
	return "", fmt.Errorf("go.mod has no go directive")
}

// canonicalBuildCmd returns the canonical build command for the module cloned
// in dir, writing the binary to out (ADR-0012 §1, FR-EXT-002):
//
//	CGO_ENABLED=0 GOFLAGS=-mod=readonly GOTOOLCHAIN=go<V> \
//	  go build -trimpath -buildvcs=false -ldflags=-buildid= -o <out> ./cmd/module/
//
// V comes from the go line of dir/go.mod. The rest of the process environment
// (HOME, PATH, GOPATH, GOMODCACHE, GOPRIVATE, proxies) is preserved; the three
// pinned variables override any inherited values.
func canonicalBuildCmd(dir, out string) (*exec.Cmd, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod")) //nolint:gosec // dir is an internally constructed build dir
	if err != nil {
		return nil, fmt.Errorf("read go.mod: %w", err)
	}
	tc, err := goToolchainFromGoMod(string(data))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-buildid=", "-o", out, "./cmd/module/") //nolint:gosec,noctx // out is internally constructed
	cmd.Dir = dir
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CGO_ENABLED=") || strings.HasPrefix(kv, "GOFLAGS=") || strings.HasPrefix(kv, "GOTOOLCHAIN=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "CGO_ENABLED=0", "GOFLAGS=-mod=readonly", "GOTOOLCHAIN="+tc)
	cmd.Env = env
	return cmd, nil
}
