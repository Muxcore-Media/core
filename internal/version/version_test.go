package version

import (
	"strings"
	"testing"
)

func TestCheckModule_EmptyMinVersion(t *testing.T) {
	if err := CheckModule("mod-a", ""); err != nil {
		t.Errorf("empty MinCoreVersion should always pass, got %v", err)
	}
}

func TestCheckModule_DevBuildAcceptsAll(t *testing.T) {
	orig := Version
	Version = "0.0.0-dev"
	defer func() { Version = orig }()

	cases := []string{"1.0.0", "99.0.0", "0.1.0", "invalid"}
	for _, v := range cases {
		if err := CheckModule("mod", v); err != nil {
			t.Errorf("dev build should accept %q, got %v", v, err)
		}
	}
}

func TestCheckModule_UnparsableCoreVersion(t *testing.T) {
	orig := Version
	Version = "not-a-version"
	defer func() { Version = orig }()

	// Unparseable core version — allow everything (can't do semver check)
	if err := CheckModule("mod", "1.0.0"); err != nil {
		t.Errorf("unparseable core version should allow all, got %v", err)
	}
}

func TestCheckModule_MajorVersionMismatch(t *testing.T) {
	orig := Version
	Version = "2.0.0"
	defer func() { Version = orig }()

	err := CheckModule("mod", "1.0.0")
	if err == nil {
		t.Fatal("expected error for major version mismatch")
	}
	if !strings.Contains(err.Error(), "major version mismatch") {
		t.Errorf("expected 'major version mismatch' in error, got %v", err)
	}
}

func TestCheckModule_MinorTooOld(t *testing.T) {
	orig := Version
	Version = "1.2.0"
	defer func() { Version = orig }()

	err := CheckModule("mod", "1.5.0") // module needs 1.5, core is 1.2
	if err == nil {
		t.Fatal("expected error for minor version too old")
	}
	if !strings.Contains(err.Error(), "minor version too old") {
		t.Errorf("expected 'minor version too old' in error, got %v", err)
	}
}

func TestCheckModule_PatchTooOld(t *testing.T) {
	orig := Version
	Version = "1.5.2"
	defer func() { Version = orig }()

	err := CheckModule("mod", "1.5.9") // module needs 1.5.9, core is 1.5.2
	if err == nil {
		t.Fatal("expected error for patch version too old")
	}
	if !strings.Contains(err.Error(), "patch version too old") {
		t.Errorf("expected 'patch version too old' in error, got %v", err)
	}
}

func TestCheckModule_Compatible(t *testing.T) {
	orig := Version
	Version = "1.5.9"
	defer func() { Version = orig }()

	cases := []string{
		"1.0.0", "1.5.0", "1.5.9", "v1.5.9", "1.5",
	}
	for _, v := range cases {
		if err := CheckModule("mod", v); err != nil {
			t.Errorf("version %q should be compatible with core 1.5.9, got %v", v, err)
		}
	}
}

func TestCheckModule_InvalidMinVersion(t *testing.T) {
	orig := Version
	Version = "1.0.0"
	defer func() { Version = orig }()

	err := CheckModule("mod", "not-a-version")
	if err == nil {
		t.Fatal("expected error for invalid MinCoreVersion")
	}
	if !strings.Contains(err.Error(), "invalid MinCoreVersion") {
		t.Errorf("expected 'invalid MinCoreVersion', got %v", err)
	}
}

func TestCompatError_Error(t *testing.T) {
	e := &CompatError{
		ModuleID:       "test-mod",
		MinCoreVersion: "2.0.0",
		CoreVersion:    "1.0.0",
		Reason:         "major mismatch",
	}
	s := e.Error()
	if !strings.Contains(s, "test-mod") || !strings.Contains(s, "2.0.0") {
		t.Errorf("CompatError.Error() missing expected content: %s", s)
	}
}

func TestString(t *testing.T) {
	orig := Version
	Version = "1.2.3"
	defer func() { Version = orig }()
	if String() != "1.2.3" {
		t.Errorf("String() = %q, want 1.2.3", String())
	}
}

func TestParseSemVer(t *testing.T) {
	cases := []struct {
		input   string
		major   int
		minor   int
		patch   int
		wantErr bool
	}{
		{"1.2.3", 1, 2, 3, false},
		{"v1.2.3", 1, 2, 3, false},
		{"1.2", 1, 2, 0, false},
		{"1", 0, 0, 0, true},
		{"abc", 0, 0, 0, true},
		{"1.2.3.4", 0, 0, 0, true},
	}
	for _, tc := range cases {
		sv, err := parseSemVer(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseSemVer(%q): expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSemVer(%q): unexpected error %v", tc.input, err)
			continue
		}
		if sv.major != tc.major || sv.minor != tc.minor || sv.patch != tc.patch {
			t.Errorf("parseSemVer(%q) = {%d,%d,%d}, want {%d,%d,%d}",
				tc.input, sv.major, sv.minor, sv.patch, tc.major, tc.minor, tc.patch)
		}
	}
}
