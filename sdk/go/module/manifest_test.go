package module

import "testing"

func TestManifestVersion(t *testing.T) {
	cases := map[string]string{
		`{"name":"x","version":"0.1.7"}`: "0.1.7",
		`{"version":"v1.2.3"}`:           "1.2.3",
		`{"version":"  "}`:               "dev",
		`{"name":"x"}`:                   "dev",
		`not json`:                       "dev",
		``:                               "dev",
	}
	for in, want := range cases {
		if got := ManifestVersion([]byte(in)); got != want {
			t.Errorf("ManifestVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
