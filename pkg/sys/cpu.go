package sys

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// SetGOMAXPROCSFromCgroup reads the cgroup CPU quota and adjusts
// GOMAXPROCS to match the container limit. No-op when unconstrained
// or when cgroupfs is not readable (non-container, permission denied).
func SetGOMAXPROCSFromCgroup() {
	n := cgroupCPUCount()
	if n <= 0 {
		return
	}
	if n < 1 {
		n = 1
	}
	runtime.GOMAXPROCS(n)
}

func cgroupCPUCount() int {
	// cgroup v2: /sys/fs/cgroup/cpu.max  "$MAX $PERIOD"
	if b, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 2 && f[0] != "max" {
			q, _ := strconv.Atoi(f[0])
			p, _ := strconv.Atoi(f[1])
			if q > 0 && p > 0 {
				return (q + p - 1) / p // ceiling division
			}
		}
	}
	// cgroup v1: /sys/fs/cgroup/cpu/cpu.cfs_{quota,period}_us
	if b, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us"); err == nil {
		q, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if q > 0 {
			if b, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us"); err == nil {
				p, _ := strconv.Atoi(strings.TrimSpace(string(b)))
				if p > 0 {
					return (q + p - 1) / p
				}
			}
		}
	}
	return 0
}
