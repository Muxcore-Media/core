package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/enroll"
)

const enrollUsage = `usage: muxcored enroll <command> [--ca-dir DIR] [args]

Manage ADR-0017 module enrollment tokens.

commands:
  token <module-id>   print the single-use enrollment token for a module
                      (requires MUXCORE_ENROLL_SECRET)
  reset <module-id>   remove the module from the enrollment ledger so its
                      token can be used again (e.g. after losing its
                      identity volume)
  list                list enrolled modules

The ledger is <ca dir>/enrolled.json. The CA dir is --ca-dir, else
grpc.ca_cert_dir / MUXCORE_GRPC_CA_CERT_DIR, else <MUXCORE_DATA_DIR>/ca.
`

// runEnrollCLI implements `muxcored enroll …` and returns the exit code.
func runEnrollCLI(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		_, _ = fmt.Fprint(stderr, enrollUsage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	cmd := args[0]
	fs := flag.NewFlagSet("muxcored enroll "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	caDirFlag := fs.String("ca-dir", "", "core CA directory (holds enrolled.json)")
	// Accept flags before or after the positional arguments.
	var rest []string
	for remaining := args[1:]; ; {
		if err := fs.Parse(remaining); err != nil {
			return 2
		}
		remaining = fs.Args()
		if len(remaining) == 0 {
			break
		}
		rest = append(rest, remaining[0])
		remaining = remaining[1:]
	}
	caDir := func() (string, error) {
		if *caDirFlag != "" {
			return *caDirFlag, nil
		}
		return enrollCADir(getenv)
	}

	switch cmd {
	case "token":
		if len(rest) != 1 {
			_, _ = fmt.Fprint(stderr, enrollUsage)
			return 2
		}
		id := rest[0]
		if err := enroll.ValidModuleID(id); err != nil {
			_, _ = fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		secret := strings.TrimSpace(getenv(enroll.EnvSecret))
		if secret == "" {
			_, _ = fmt.Fprintf(stderr, "error: %s is not set\n", enroll.EnvSecret)
			return 1
		}
		if err := enroll.ValidSecret(secret); err != nil {
			_, _ = fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if dir, err := caDir(); err == nil {
			if used, err := enroll.NewLedger(dir).Has(id); err == nil && used {
				_, _ = fmt.Fprintf(stderr, "warning: %q is already enrolled; the token is spent until `muxcored enroll reset %s`\n", id, id)
			}
		}
		_, _ = fmt.Fprintln(stdout, enroll.Token([]byte(secret), id))
		return 0

	case "reset":
		if len(rest) != 1 {
			_, _ = fmt.Fprint(stderr, enrollUsage)
			return 2
		}
		dir, err := caDir()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		removed, err := enroll.NewLedger(dir).Reset(rest[0])
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if !removed {
			_, _ = fmt.Fprintf(stdout, "%s was not enrolled\n", rest[0])
			return 0
		}
		_, _ = fmt.Fprintf(stdout, "reset %s: its enrollment token can be used once more\n", rest[0])
		return 0

	case "list":
		if len(rest) != 0 {
			_, _ = fmt.Fprint(stderr, enrollUsage)
			return 2
		}
		dir, err := caDir()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		entries, err := enroll.NewLedger(dir).List()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		for _, e := range entries {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\n", e.ModuleID, e.EnrolledAt.Format(time.RFC3339))
		}
		return 0
	}
	_, _ = fmt.Fprintf(stderr, "unknown enroll command %q\n\n%s", cmd, enrollUsage)
	return 2
}

// enrollCADir resolves the CA directory the way muxcored does at startup,
// without exiting on a missing config file.
func enrollCADir(getenv func(string) string) (string, error) {
	path := getenv("MUXCORE_CONFIG")
	if path == "" {
		path = "muxcore.json"
	}
	cfg, err := config.Load(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("load config %s: %w", path, err)
		}
		cfg = config.Default()
		config.ApplyEnvOverrides(cfg)
	}
	return resolveCADir(cfg, getenv), nil
}
