package contracts

import "context"

// FileEventType describes what happened to a file.
type FileEventType string

const (
	FileCreated  FileEventType = "created"
	FileModified FileEventType = "modified"
	FileDeleted  FileEventType = "deleted"
	FileRenamed  FileEventType = "renamed"
)

// FileEvent describes a filesystem change.
type FileEvent struct {
	Path    string
	OldPath string // populated for rename events
	Type    FileEventType
	Size    int64
	ModTime int64
}

// FileWatcher watches directories for filesystem changes and emits events.
// Core provides the event stream; modules subscribe to react (e.g., media-library
// auto-imports new files, transcoders detect new media to process).
type FileWatcher interface {
	// Watch starts watching a directory for matching files. Returns a channel
	// that receives file events. Patterns are glob-style (e.g., "*.mkv", "*.mp4").
	Watch(ctx context.Context, path string, patterns []string) (<-chan FileEvent, error)
	// Unwatch stops watching a directory.
	Unwatch(ctx context.Context, path string) error
}
