package contracts

// TagModule is a single module entry in a spool tag definition.
type TagModule struct {
	Repo     string `json:"repo"`
	Version  string `json:"version"`
	Required bool   `json:"required"`
	Checksum string `json:"checksum,omitempty"`
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
// Discovered via FindByCapability("spool.resolver"). If no resolver is
// registered, core falls back to a generic HTTPS JSON fetcher.
type SpoolResolver interface {
	// ResolveTag fetches and parses a tag definition from the given spool URL.
	ResolveTag(spoolURL, tagName string) (*TagDefinition, error)
}
