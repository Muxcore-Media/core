package module

import (
	"encoding/json"
	"strings"
)

// ManifestVersion returns the "version" field of a module's muxcore.json
// (pass the bytes embedded at the module root with //go:embed muxcore.json).
// It is the single source of a module's reported version (ADR-0021); releases
// bump muxcore.json together with the tag. Returns "dev" when the manifest is
// missing, unparsable or has no version.
func ManifestVersion(manifest []byte) string {
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return "dev"
	}
	if v := strings.TrimSpace(m.Version); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	return "dev"
}
