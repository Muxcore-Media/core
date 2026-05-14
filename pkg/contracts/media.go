package contracts

import "context"

type MediaType string

const (
	MediaTypeMovie     MediaType = "movie"
	MediaTypeTV        MediaType = "tv"
	MediaTypeMusic     MediaType = "music"
	MediaTypeBook      MediaType = "book"
	MediaTypeAudiobook MediaType = "audiobook"
	MediaTypeManga     MediaType = "manga"
	MediaTypeAnime     MediaType = "anime"
	MediaTypePodcast   MediaType = "podcast"
)

type MediaObject struct {
	ID          string
	Type        MediaType
	Title       string
	Year        int
	Overview    string
	Genres      []string
	Rating      float64
	PosterURL   string
	BackdropURL string
	Assets      []AssetRef
	Metadata    map[string]any
}

type AssetRef struct {
	ID       string
	Storage  string
	MIMEType string
	Size     int64
	Quality  string
}

type MediaLibrary interface {
	Add(ctx context.Context, obj MediaObject) error
	Remove(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (MediaObject, error)
	List(ctx context.Context, mediaType MediaType, offset, limit int) ([]MediaObject, error)
	Search(ctx context.Context, query string) ([]MediaObject, error)
}
