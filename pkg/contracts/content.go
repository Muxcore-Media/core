package contracts

import "context"

// SupplementaryContentProvider finds and fetches supplementary content for media objects —
// subtitles, lyrics, chapter titles, alternate artwork, and any other content that enriches
// a media object, either as metadata fields or as companion files.
//
// Kind strings ("subtitle", "lyrics", "chapters", etc.) are defined by modules, not core.
// Modules advertise their supported kinds via SupportedKinds() and are discoverable via
// ServiceRegistry.FindByCapability("content.subtitle").
type SupplementaryContentProvider interface {
	// Search returns available content candidates for a media object.
	Search(ctx context.Context, media MediaObject, kind string, language string) ([]ContentCandidate, error)

	// Fetch retrieves content for a candidate. The result may embed metadata into the
	// media object or produce a companion file (e.g., .srt, .lrc).
	Fetch(ctx context.Context, candidate ContentCandidate) (*ContentResult, error)

	// SupportedKinds returns the content kinds this provider handles.
	SupportedKinds() []string
}

// ContentCandidate is a single result from a supplementary content search.
type ContentCandidate struct {
	ID          string // provider-specific identifier
	Kind        string // "subtitle", "lyrics", "chapters", etc.
	Language    string
	Title       string // human-readable label
	Format      string // "srt", "lrc", "txt", etc.
	ProviderID  string // which provider returned this
	DownloadURL string
	Score       int // relevance, higher is better
}

// ContentResult is the fetched supplementary content.
type ContentResult struct {
	MediaID  string
	Kind     string
	Language string
	Format   string
	// Data is the content itself. For companion files (.srt, .lrc), this is the raw
	// file contents. For metadata enrichment, this is serialized field data.
	Data []byte
	// EmbedInMetadata is true when Data should be merged into the media object's
	// Fields map rather than stored as a companion file.
	EmbedInMetadata bool
}
