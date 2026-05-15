package contracts

// -- Event payload schemas --

// MediaRequestedPayload is the payload for media.requested events.
type MediaRequestedPayload struct {
	MediaType string `json:"media_type"`
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	TmdbID    string `json:"tmdb_id,omitempty"`
	Quality   string `json:"quality,omitempty"`
}

// DownloadStartedPayload is the payload for download.started events.
type DownloadStartedPayload struct {
	DownloadID string `json:"download_id"`
	Title      string `json:"title"`
	Size       int64  `json:"size,omitempty"`
}

// DownloadCompletedPayload is the payload for download.completed events.
type DownloadCompletedPayload struct {
	RequestID  string `json:"request_id"`
	DownloadID string `json:"download_id"`
	Title      string `json:"title"`
}

// DownloadFailedPayload is the payload for download.failed events.
type DownloadFailedPayload struct {
	DownloadID string `json:"download_id"`
	Title      string `json:"title"`
	Error      string `json:"error"`
}

// TranscodeStartedPayload is the payload for transcode.started events.
type TranscodeStartedPayload struct {
	MediaID string `json:"media_id"`
	Profile string `json:"profile"`
}

// TranscodeCompletedPayload is the payload for transcode.completed events.
type TranscodeCompletedPayload struct {
	MediaID    string `json:"media_id"`
	OutputPath string `json:"output_path"`
}

// TranscodeFailedPayload is the payload for transcode.failed events.
type TranscodeFailedPayload struct {
	MediaID string `json:"media_id"`
	Error   string `json:"error"`
}

// LibraryItemAddedPayload is the payload for library.item.added events.
type LibraryItemAddedPayload struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// LibraryItemRemovedPayload is the payload for library.item.removed events.
type LibraryItemRemovedPayload struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// PlaybackStartedPayload is the payload for playback.started events.
type PlaybackStartedPayload struct {
	SessionID string `json:"session_id"`
	MediaID   string `json:"media_id"`
	Position  int64  `json:"position"`
}

// PlaybackStoppedPayload is the payload for playback.stopped events.
type PlaybackStoppedPayload struct {
	SessionID string `json:"session_id"`
	MediaID   string `json:"media_id"`
	Position  int64  `json:"position"`
}

// ModuleDegradedPayload is the payload for module.degraded events.
type ModuleDegradedPayload struct {
	ModuleID string `json:"module_id"`
	Error    string `json:"error"`
}

// SubtitleMissingPayload is the payload for subtitle.missing events.
type SubtitleMissingPayload struct {
	MediaID  string `json:"media_id"`
	Language string `json:"language"`
}

// SubtitleFetchedPayload is the payload for subtitle.fetched events.
type SubtitleFetchedPayload struct {
	MediaID  string `json:"media_id"`
	Language string `json:"language"`
	Provider string `json:"provider"`
}

// ModuleRegisteredPayload is the payload for module.registered events.
type ModuleRegisteredPayload struct {
	ModuleID string `json:"module_id"`
	Version  string `json:"version"`
}

// ModuleUnregisteredPayload is the payload for module.unregistered events.
type ModuleUnregisteredPayload struct {
	ModuleID string `json:"module_id"`
}

// QualityDecisionPayload is the payload for quality.decision events.
type QualityDecisionPayload struct {
	ReleaseTitle string `json:"release_title"`
	Profile      string `json:"profile"`
	Accepted     bool   `json:"accepted"`
	Reason       string `json:"reason"`
	Score        int    `json:"score"`
}

// FormatMatchedPayload is the payload for format.matched events.
type FormatMatchedPayload struct {
	ReleaseTitle string `json:"release_title"`
	Format       string `json:"format"`
	Score        int    `json:"score"`
}

// MediaAnalyzedPayload is the payload for media.analyzed events.
type MediaAnalyzedPayload struct {
	Path       string  `json:"path"`
	Codec      string  `json:"codec"`
	Resolution string  `json:"resolution"`
	Duration   float64 `json:"duration"`
}

// EventSchemaVersion is the current schema version for all event payloads.
// When a breaking change is made, this is incremented.
const EventSchemaVersion = "v1"
