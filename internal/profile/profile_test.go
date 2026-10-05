package profile

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		want     Name
		source   Source
		insecure bool
		warn     string // substring expected in a warning ("" = no warnings)
		wantErr  error
		errSub   string
	}{
		{name: "unset no flag defaults household", env: nil, want: Household, source: SourceDefault},
		{name: "unset insecure infers dev", env: map[string]string{EnvInsecure: "true"}, want: Dev, source: SourceInferredInsecure, insecure: true, warn: "deprecated"},
		{name: "unset legacy flag infers dev", env: map[string]string{EnvInsecureLegacy: "1"}, want: Dev, source: SourceInferredInsecure, insecure: true, warn: EnvInsecureLegacy},
		{name: "flag false is not insecure", env: map[string]string{EnvInsecure: "false"}, want: Household, source: SourceDefault},
		{name: "explicit dev", env: map[string]string{EnvProfile: "dev"}, want: Dev, source: SourceExplicit},
		{name: "explicit dev insecure", env: map[string]string{EnvProfile: "DEV", EnvInsecure: "1"}, want: Dev, source: SourceExplicit, insecure: true},
		{name: "explicit household", env: map[string]string{EnvProfile: "household"}, want: Household, source: SourceExplicit},
		{name: "staging alias", env: map[string]string{EnvProfile: " staging "}, want: Household, source: SourceAlias},
		{name: "sqlite treated as unset", env: map[string]string{EnvProfile: "sqlite"}, want: Household, source: SourceDefault, warn: EnvDBBackend + "=sqlite"},
		{name: "postgres with flag infers dev", env: map[string]string{EnvProfile: "postgres", EnvInsecure: "true"}, want: Dev, source: SourceInferredInsecure, insecure: true, warn: EnvDBBackend},
		{name: "household insecure fatal", env: map[string]string{EnvProfile: "household", EnvInsecure: "true"}, wantErr: ErrInsecureInHousehold},
		{name: "staging insecure fatal", env: map[string]string{EnvProfile: "staging", EnvInsecureLegacy: "true"}, wantErr: ErrInsecureInHousehold},
		{name: "unknown value", env: map[string]string{EnvProfile: "prod"}, errSub: "unknown MUXCORE_PROFILE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Resolve(envMap(tt.env))
			if tt.wantErr != nil || tt.errSub != "" {
				if err == nil {
					t.Fatalf("expected error, got profile %+v", r)
				}
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Fatalf("error %v, want %v", err, tt.wantErr)
				}
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("error %q does not contain %q", err, tt.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if r.Name != tt.want || r.Source != tt.source || r.Insecure != tt.insecure {
				t.Fatalf("got %s/%s/insecure=%v, want %s/%s/insecure=%v", r.Name, r.Source, r.Insecure, tt.want, tt.source, tt.insecure)
			}
			if tt.warn == "" && len(r.Warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", r.Warnings)
			}
			if tt.warn != "" && !strings.Contains(strings.Join(r.Warnings, "\n"), tt.warn) {
				t.Fatalf("warnings %v do not mention %q", r.Warnings, tt.warn)
			}
			if r.RequireMarketplaceSignatures() != (r.Name == Household) {
				t.Fatal("signature requirement must follow household")
			}
		})
	}
}

func TestDataDirAndExport(t *testing.T) {
	d := t.TempDir()
	if got := DataDir(envMap(map[string]string{EnvDataDir: d})); got != d {
		t.Fatalf("DataDir = %q, want %q", got, d)
	}
	got := DataDir(envMap(nil))
	if !filepath.IsAbs(got) || filepath.Base(got) != DefaultDataDir {
		t.Fatalf("default DataDir = %q", got)
	}
	if CAExportDir(envMap(map[string]string{EnvCAExportDir: " /x "})) != "/x" {
		t.Fatal("CAExportDir trim")
	}
}
