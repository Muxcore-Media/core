package firecracker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// VMConfig is a minimal Firecracker JSON config with boot, rootfs, and vsock.
type VMConfig struct { //nolint:govet // JSON field order follows Firecracker schema
	BootSource    BootSource    `json:"boot-source"`
	Drives        []Drive       `json:"drives"`
	Vsock         *Vsock        `json:"vsock,omitempty"`
	MachineConfig MachineConfig `json:"machine-config"`
}

type BootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args,omitempty"`
}

type Drive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type Vsock struct { //nolint:govet // JSON field order follows Firecracker schema
	GuestCID int    `json:"guest_cid"`
	UDSPath  string `json:"uds_path"`
}

type MachineConfig struct {
	VcpuCount  int  `json:"vcpu_count"`
	MemSizeMib int  `json:"mem_size_mib"`
	SMT        bool `json:"smt"`
}

// Options for WriteConfig.
type Options struct { //nolint:govet // option struct groups related sandbox settings
	ModuleID   string
	RootfsPath string
	KernelPath string
	VsockCID   int
	VsockUDS   string
	VCPU       int
	MemMiB     int
}

// WriteConfig writes a Firecracker config.json to dir.
func WriteConfig(dir string, opt Options) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("firecracker config: dir required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	kernel := strings.TrimSpace(opt.KernelPath)
	if kernel == "" {
		kernel = strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_FC_KERNEL"))
	}
	if kernel == "" {
		return "", fmt.Errorf("firecracker config: kernel path required (MUXCORE_SANDBOX_FC_KERNEL)")
	}
	rootfs := strings.TrimSpace(opt.RootfsPath)
	if rootfs == "" {
		return "", fmt.Errorf("firecracker config: rootfs required")
	}
	cid := opt.VsockCID
	if cid <= 0 {
		if v := strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_FC_VSOCK_CID")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cid = n
			}
		}
	}
	if cid <= 0 {
		cid = 3
	}
	uds := strings.TrimSpace(opt.VsockUDS)
	if uds == "" {
		uds = strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_FC_VSOCK_UDS"))
	}
	if uds == "" {
		uds = filepath.Join(dir, "vsock.sock")
	}
	vcpu := opt.VCPU
	if vcpu <= 0 {
		vcpu = 1
	}
	mem := opt.MemMiB
	if mem <= 0 {
		mem = 128
	}
	cfg := VMConfig{
		BootSource: BootSource{
			KernelImagePath: kernel,
			BootArgs:        "console=ttyS0 reboot=k panic=1 pci=off",
		},
		Drives: []Drive{{
			DriveID: "rootfs", PathOnHost: rootfs, IsRootDevice: true, IsReadOnly: false,
		}},
		Vsock:         &Vsock{GuestCID: cid, UDSPath: uds},
		MachineConfig: MachineConfig{VcpuCount: vcpu, MemSizeMib: mem, SMT: false},
	}
	path := filepath.Join(dir, "config.json")
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	_ = opt.ModuleID
	return path, nil
}

// ValidateConfig checks required fields in a written config.json.
func ValidateConfig(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg VMConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if cfg.BootSource.KernelImagePath == "" {
		return fmt.Errorf("firecracker config: missing kernel")
	}
	if len(cfg.Drives) == 0 || cfg.Drives[0].PathOnHost == "" {
		return fmt.Errorf("firecracker config: missing rootfs drive")
	}
	if cfg.Vsock == nil || cfg.Vsock.UDSPath == "" {
		return fmt.Errorf("firecracker config: missing vsock")
	}
	return nil
}
