package contracts

import "context"

type IndexerResult struct {
	Title       string
	GUID        string
	Link        string
	MagnetURI   string
	Size        int64
	Seeders     int
	Leechers    int
	PublishDate string
	Categories  []string
	Source      string
}

type SearchQuery struct {
	Query      string
	Type       string // movie, tv, music, book, etc.
	Categories []string
	Season     int
	Episode    int
	Year       int
	Limit      int
}

type Indexer interface {
	Name() string
	Search(ctx context.Context, query SearchQuery) ([]IndexerResult, error)
	Capabilities(ctx context.Context) ([]string, error)
}
