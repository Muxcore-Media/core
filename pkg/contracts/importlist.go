package contracts

import "context"

// ImportListItem is a single item from an external watchlist or import source.
type ImportListItem struct {
	Title      string
	MediaType  string            // "movie", "tv", "music", etc.
	ExternalID string            // ID from the source service (e.g. Trakt ID, IMDb ID)
	Year       int
	Source     string            // "trakt", "plex", "myanimelist", "rss", etc.
	Status     string            // "wanted", "collected", "watched"
	Metadata   map[string]string
}

// ImportListProvider is implemented by import list modules (importlist-trakt, etc.)
type ImportListProvider interface {
	// FetchItems retrieves items from the external source.
	FetchItems(ctx context.Context) ([]ImportListItem, error)
	// ListInfo returns metadata about this import list.
	ListInfo(ctx context.Context) ImportListInfo
}

// ImportListInfo describes an import list source.
type ImportListInfo struct {
	ID          string
	Name        string
	Description string
	MediaType   string // primary media type this list provides
	URL         string // source URL if applicable
}
