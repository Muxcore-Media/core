package contracts

import "context"

// QualityProfile defines acceptable quality ranges for a media type.
type QualityProfile struct {
	ID         string
	Name       string
	MediaType  MediaType
	Upgradable bool     // if true, upgrade to better quality when available
	MinQuality string   // minimum acceptable quality (e.g., "HD-720p")
	MaxQuality string   // maximum desired quality (e.g., "4K-HDR")
	Preferred  []string // ordered list of preferred quality tags
	Cutoff     string   // stop searching once this quality is met
}

// QualityDecision is the result of evaluating a release against a quality profile.
type QualityDecision struct {
	Accepted bool
	Reason   string // why it was accepted or rejected
	Quality  string // detected quality of the release
	Upgrade  bool   // true if this release upgrades an existing one
	Score    int    // quality score for ranking (higher = better)
}

// ReleaseDecider evaluates whether a candidate release matches a quality profile.
// Modules implement this to provide custom decision logic.
type ReleaseDecider interface {
	// Decide evaluates a candidate release against the given profile.
	Decide(ctx context.Context, profile QualityProfile, candidate ReleaseCandidate) (QualityDecision, error)
}

// ReleaseCandidate represents a found release being evaluated.
type ReleaseCandidate struct {
	Title    string
	Source   string // indexer name
	Size     int64
	Quality  string   // e.g., "1080p", "4K", "HDR"
	Codec    string   // e.g., "h264", "hevc", "av1"
	Audio    []string // audio formats present
	Seeders  int
	Leechers int
	Verified bool
	Repack   bool
	Proper   bool
	Scene    bool
}
