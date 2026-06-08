package contracts

import "context"

// PlaybackSession represents an active playback session on a media server.
type PlaybackSession struct {
	ID        string
	UserID    string
	UserName  string
	ItemID    string
	ItemName  string
	MediaType string
	Position  int64 // ticks (1/10,000,000 of a second)
	Duration  int64 // ticks
	IsPaused  bool
	IsMuted   bool
	Volume    int
}

// Playback is implemented by media server connectors (Jellyfin, Plex, Emby)
// to provide playback status and library sync capabilities.
type Playback interface {
	// Ping checks if the media server is reachable.
	Ping(ctx context.Context) error

	// GetSessions returns all active playback sessions.
	GetSessions(ctx context.Context) ([]PlaybackSession, error)

	// RefreshLibrary triggers a library scan on the media server.
	RefreshLibrary(ctx context.Context) error

	// GetStreamURL returns a direct stream URL for the given item.
	GetStreamURL(ctx context.Context, itemID string) (string, error)
}
