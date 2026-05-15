package contracts

import "context"

// MediaInfo holds codec and technical metadata about a media file.
type MediaInfo struct {
	Path     string
	Duration float64 // seconds
	Bitrate  int64   // overall bitrate in bps
	Size     int64   // file size in bytes

	// Video streams
	VideoCodec  string // e.g., "h264", "hevc", "av1", "vp9"
	Resolution  string // e.g., "1920x1080"
	Width       int
	Height      int
	AspectRatio string // e.g., "16:9"
	FrameRate   float64
	BitDepth    int    // 8, 10, 12
	HDR         string // "HDR10", "HDR10+", "DolbyVision", "" if SDR
	ColorSpace  string

	// Audio streams
	AudioCodecs    []string // e.g., ["aac", "ac3", "dts"]
	AudioChannels  []string // e.g., ["5.1", "2.0", "7.1"]
	AudioLanguages []string // e.g., ["eng", "jpn"]
	Atmos          bool

	// Subtitles
	SubtitleLanguages []string // e.g., ["eng", "fre"]
	SubtitleFormats   []string // e.g., ["srt", "pgs", "ass"]
	ForcedSubtitles   []string // languages with forced subs

	// Container
	Container string // e.g., "mkv", "mp4", "avi"

	// Chapters
	Chapters int
}

// MediaInfoProvider analyzes media files and returns technical metadata.
// Modules implement this to provide codec/format analysis (ffprobe, MediaInfo, etc.).
type MediaInfoProvider interface {
	// Analyze inspects a media file and returns its technical metadata.
	Analyze(ctx context.Context, path string) (MediaInfo, error)

	// Probe quickly checks if a file contains valid media (fast path, no deep analysis).
	Probe(ctx context.Context, path string) (MediaInfo, error)
}
