package api

import (
	"strings"
	"testing"
)

func TestValidateModuleID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id    string
		valid bool
	}{
		{"admin-ui", true},
		{"media-scanner", true},
		{"", false},
		{"../escape", false},
		{"bad id", false},
		{strings.Repeat("a", maxModuleIDLen+1), false},
	}
	for _, tc := range cases {
		err := ValidateModuleID(tc.id)
		if tc.valid && err != nil {
			t.Errorf("ValidateModuleID(%q) = %v, want nil", tc.id, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("ValidateModuleID(%q) = nil, want error", tc.id)
		}
	}
}

func TestSanitizeModuleConfig(t *testing.T) {
	t.Parallel()
	got, err := SanitizeModuleConfig(map[string]string{
		"Host":    "127.0.0.1",
		"Timeout": "30",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["Host"] != "127.0.0.1" {
		t.Fatalf("Host=%q", got["Host"])
	}

	_, err = SanitizeModuleConfig(map[string]string{"bad key": "v"})
	if err == nil {
		t.Fatal("expected error for invalid config key")
	}

	_, err = SanitizeModuleConfig(map[string]string{"Key": "val\x00ue"})
	if err == nil {
		t.Fatal("expected error for null byte in value")
	}
}

func TestValidateTaskID(t *testing.T) {
	t.Parallel()
	if err := ValidateTaskID("task-123"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidateTaskID("../etc/passwd"); err == nil {
		t.Fatal("expected error for path traversal")
	}
}
