package contracts

import "context"

// TagModule is a single module entry in a spool tag definition.
type TagModule struct {
	Repo     string `json:"repo"`
	Version  string `json:"version"`
	Required bool   `json:"required"`
	Checksum string `json:"checksum,omitempty"`
	// Signature is an optional hex/base64 ed25519 detached signature over the
	// module artifact bytes. Sidecar files {artifact}.sig / {artifact}.minisig
	// are also accepted when MUXCORE_SPOOL_REQUIRE_SIGNATURE=1.
	Signature string `json:"signature,omitempty"`
	// Publisher is an optional identity string for the module pin (marketplace
	// trust). When MUXCORE_SPOOL_ALLOWED_PUBLISHERS is set, DeployTag/boot
	// require a match.
	Publisher string `json:"publisher,omitempty"`
	// InstanceID differentiates multiple copies of the same module on one
	// node. When set, the derived module ID becomes "{baseID}-{instanceID}"
	// instead of just "{baseID}". Allows running two identical modules
	// with different config (e.g. two torrent downloaders on different ports).
	InstanceID string `json:"instance_id,omitempty"`
	// Config is instance-specific configuration passed as environment
	// variables to the module binary. Each key is prefixed with MUXCORE_CFG_
	// and set as an env var during spawn.
	Config map[string]string `json:"config,omitempty"`
}

// TagDefinition is a curated module preset fetched from a spool.
type TagDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Version     string      `json:"version"`
	Modules     []TagModule `json:"modules"`
}

// SpoolResolver resolves a tag name into a list of module definitions from
// a spool URL. Implementations may support different spool formats
// (GitHub, self-hosted, IPFS, etc.).
//
// Discovered via FindByCapability(CapabilitySpoolResolver). If no resolver is
// registered, core falls back to a generic HTTPS JSON fetcher.
type SpoolResolver interface {
	// ResolveTag fetches and parses a tag definition from the given spool URL.
	// The context carries deadlines and cancellation for the network request.
	ResolveTag(ctx context.Context, spoolURL, tagName string) (*TagDefinition, error)
}
