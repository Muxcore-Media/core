// Cross-cluster tenant storage sync: mirror per-tenant partitions to remote
// targets (file:// for fixture/offline, s3:// for production storage-s3 peers).
//
// Env:
//
//	MUXCORE_TENANT_STORAGE_SYNC — JSON per-tenant sync policy (see StorageSyncPolicy)
package tenant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StorageSyncPolicy describes how a tenant partition is replicated.
type StorageSyncPolicy struct {
	// Targets are destination URIs: file:///path or s3://bucket/prefix
	Targets []string `json:"targets"`
	// Mode is mirror (push all files) or pull (reserved; mirror only today).
	Mode string `json:"mode"`
	// IntervalSec is suggested sync cadence for operators/cron (0 = manual only).
	IntervalSec int `json:"interval_sec"`
}

// ParseStorageSyncMap parses MUXCORE_TENANT_STORAGE_SYNC JSON.
func ParseStorageSyncMap(raw string) (map[string]StorageSyncPolicy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]StorageSyncPolicy{}, nil
	}
	var obj map[string]StorageSyncPolicy
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, fmt.Errorf("MUXCORE_TENANT_STORAGE_SYNC: %w", err)
	}
	out := make(map[string]StorageSyncPolicy, len(obj))
	for k, v := range obj {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if v.Mode == "" {
			v.Mode = "mirror"
		}
		out[k] = v
	}
	return out, nil
}

// StorageSyncMapFromEnv loads the sync map from the environment.
func StorageSyncMapFromEnv() (map[string]StorageSyncPolicy, error) {
	return ParseStorageSyncMap(os.Getenv("MUXCORE_TENANT_STORAGE_SYNC"))
}

// PolicyFor returns the sync policy for tenantID (empty policy if unset).
func PolicyFor(m map[string]StorageSyncPolicy, tenantID string) StorageSyncPolicy {
	if len(m) == 0 {
		return StorageSyncPolicy{}
	}
	id := OrDefault(tenantID)
	if p, ok := m[id]; ok {
		return p
	}
	return StorageSyncPolicy{}
}

// SyncReport summarizes one mirror run.
type SyncReport struct {
	TenantID   string    `json:"tenant_id"`
	Source     string    `json:"source"`
	Copied     int       `json:"copied"`
	Skipped    int       `json:"skipped"`
	Errors     []string  `json:"errors,omitempty"`
	FinishedAt time.Time `json:"finished_at"`
}

// MirrorTenant copies files from sourceDir to each file:// target under policy.
// s3:// targets are recorded as skipped with a hint (storage-s3 module handles live sync).
func MirrorTenant(ctx context.Context, tenantID, sourceDir string, policy StorageSyncPolicy) (SyncReport, error) {
	report := SyncReport{
		TenantID: tenantID,
		Source:   sourceDir,
	}
	if policy.Mode != "" && policy.Mode != "mirror" {
		return report, fmt.Errorf("storage sync: unsupported mode %q", policy.Mode)
	}
	if len(policy.Targets) == 0 {
		return report, nil
	}
	info, err := os.Stat(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return report, nil
		}
		return report, err
	}
	if !info.IsDir() {
		return report, fmt.Errorf("storage sync: source not a directory: %s", sourceDir)
	}

	for _, target := range policy.Targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if strings.HasPrefix(target, "s3://") {
			report.Skipped++
			continue
		}
		if !strings.HasPrefix(target, "file://") {
			report.Errors = append(report.Errors, fmt.Sprintf("unsupported target scheme: %s", target))
			continue
		}
		destRoot := strings.TrimPrefix(target, "file://")
		destRoot = filepath.Join(destRoot, SafeID(OrDefault(tenantID)))
		if err := os.MkdirAll(destRoot, 0o700); err != nil {
			report.Errors = append(report.Errors, err.Error())
			continue
		}
		walkErr := filepath.WalkDir(sourceDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			rel, err := filepath.Rel(sourceDir, path)
			if err != nil {
				return err
			}
			dest := filepath.Join(destRoot, rel)
			if d.IsDir() {
				return os.MkdirAll(dest, 0o700)
			}
			if err := mirrorFile(path, dest); err != nil {
				report.Errors = append(report.Errors, err.Error())
				return nil
			}
			report.Copied++
			return nil
		})
		if walkErr != nil {
			report.Errors = append(report.Errors, walkErr.Error())
		}
	}
	report.FinishedAt = time.Now().UTC()
	return report, nil
}

func mirrorFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
