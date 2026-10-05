package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Muxcore-Media/core/internal/enroll"
)

func runEnroll(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runEnrollCLI(args, func(k string) string { return env[k] }, &out, &errb)
	return code, out.String(), errb.String()
}

func TestEnrollCLI(t *testing.T) {
	dir := t.TempDir()
	const secret = "0123456789abcdef-cli-secret"
	env := map[string]string{enroll.EnvSecret: secret}

	if code, _, errOut := runEnroll(t, map[string]string{}, "token", "m1", "--ca-dir", dir); code != 1 || !strings.Contains(errOut, enroll.EnvSecret) {
		t.Fatalf("token without secret: %d %q", code, errOut)
	}
	code, out, _ := runEnroll(t, env, "token", "--ca-dir", dir, "m1")
	if code != 0 || strings.TrimSpace(out) != enroll.Token([]byte(secret), "m1") {
		t.Fatalf("token: %d %q", code, out)
	}
	if code, _, _ := runEnroll(t, env, "token", "--ca-dir", dir, "bad id"); code != 1 {
		t.Fatalf("invalid id: %d", code)
	}

	if err := enroll.NewLedger(dir).Consume("m1"); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runEnroll(t, env, "list", "--ca-dir", dir)
	if code != 0 || !strings.HasPrefix(out, "m1\t") {
		t.Fatalf("list: %d %q", code, out)
	}
	if _, _, errOut := runEnroll(t, env, "token", "--ca-dir", dir, "m1"); !strings.Contains(errOut, "already enrolled") {
		t.Fatalf("token for enrolled module should warn: %q", errOut)
	}
	if code, out, _ = runEnroll(t, env, "reset", "--ca-dir", dir, "m1"); code != 0 || !strings.Contains(out, "reset m1") {
		t.Fatalf("reset: %d %q", code, out)
	}
	if code, out, _ = runEnroll(t, env, "list", "--ca-dir", dir); code != 0 || out != "" {
		t.Fatalf("list after reset: %d %q", code, out)
	}
	if code, out, _ = runEnroll(t, env, "reset", "--ca-dir", dir, "m1"); code != 0 || !strings.Contains(out, "not enrolled") {
		t.Fatalf("reset unknown: %d %q", code, out)
	}
	if code, _, _ := runEnroll(t, env); code != 2 {
		t.Fatalf("no args: %d", code)
	}
	if code, _, _ := runEnroll(t, env, "bogus"); code != 2 {
		t.Fatalf("unknown command: %d", code)
	}
}

// Without --ca-dir the ledger is found where muxcored keeps its CA.
func TestEnrollCLI_DefaultCADir(t *testing.T) {
	data := t.TempDir()
	t.Chdir(t.TempDir()) // no muxcore.json here
	env := map[string]string{"MUXCORE_DATA_DIR": data, "MUXCORE_CONFIG": "missing.json"}
	if err := enroll.NewLedger(data + "/ca").Consume("m9"); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runEnroll(t, env, "list")
	if code != 0 || !strings.HasPrefix(out, "m9\t") {
		t.Fatalf("list: %d %q %q", code, out, errOut)
	}
}
