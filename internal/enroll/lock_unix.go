//go:build unix

package enroll

import (
	"fmt"
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on path (created 0600) and
// returns the unlock function.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // lock file in the CA directory
	if err != nil {
		return nil, fmt.Errorf("enroll ledger: open lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // fd fits in int
		_ = f.Close()
		return nil, fmt.Errorf("enroll ledger: lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // fd fits in int
		_ = f.Close()
	}, nil
}
