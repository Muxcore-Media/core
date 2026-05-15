package contracts

import (
	"context"
	"time"
)

type Event struct {
	ID        string
	Type      string
	Source    string
	TraceID   string
	Payload   []byte
	Metadata  map[string]string
	Timestamp time.Time
}

type EventHandler func(ctx context.Context, event Event) error

type EventBus interface {
	Publish(ctx context.Context, event Event) error
	Subscribe(ctx context.Context, eventType string, handler EventHandler) error
	Unsubscribe(ctx context.Context, eventType string, handler EventHandler) error
	Request(ctx context.Context, event Event, timeout time.Duration) (Event, error)
}

// Well-known event types
const (
	EventMediaRequested     = "media.requested"
	EventDownloadStarted    = "download.started"
	EventDownloadCompleted  = "download.completed"
	EventDownloadFailed     = "download.failed"
	EventTranscodeStarted   = "transcode.started"
	EventTranscodeCompleted = "transcode.completed"
	EventTranscodeFailed    = "transcode.failed"
	EventLibraryItemAdded   = "library.item.added"
	EventLibraryItemRemoved = "library.item.removed"
	EventSubtitleMissing    = "subtitle.missing"
	EventSubtitleFetched    = "subtitle.fetched"
	EventPlaybackStarted    = "playback.started"
	EventPlaybackStopped    = "playback.stopped"
	EventModuleRegistered   = "module.registered"
	EventModuleUnregistered = "module.unregistered"
	EventModuleDegraded     = "module.degraded"
	EventQualityDecision    = "quality.decision"
	EventFormatMatched      = "format.matched"
	EventMediaAnalyzed      = "media.analyzed"
)
