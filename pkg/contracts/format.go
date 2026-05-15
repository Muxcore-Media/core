package contracts

import "context"

// CustomFormat defines a user-created rule for tagging or scoring releases.
type CustomFormat struct {
	ID          string
	Name        string
	Description string
	// Tags that get applied to releases matching this format.
	Tags []string
	// Score modifier applied when this format matches (+100 = prefer, -100 = avoid).
	Score int
}

// FormatCondition is a single condition within a custom format.
type FormatCondition struct {
	Field    string // field to check: "title", "codec", "audio", "source", "size"
	Operator string // "contains", "equals", "regex", "gte", "lte"
	Value    string // value to match against
}

// FormatMatcher evaluates whether a release matches a custom format.
type FormatMatcher interface {
	// Match evaluates a candidate against conditions and returns the format if matched.
	Match(ctx context.Context, format CustomFormat, candidate ReleaseCandidate) (bool, error)
}

// ReleaseProfile combines quality profiles with custom formats for final scoring.
type ReleaseProfile struct {
	ID               string
	Name             string
	MediaType        MediaType
	QualityProfileID string
	Formats          []CustomFormat // custom formats with scores
	MinScore         int            // minimum score to accept
	PreferredTags    []string       // releases with these tags get bonus score
	MustNotContain   []string       // releases with these tags are rejected
}
