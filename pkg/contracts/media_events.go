package contracts

// Media / download domain event types and payloads.
// Modules historically imported these from core/pkg/contracts even though
// events.go notes that domain schemas may live in contract repos.

const (
	EventMovieAdded           = "media.movie.added"
	EventMovieRemoved         = "media.movie.removed"
	EventMovieUpdated         = "media.movie.updated"
	EventMovieFileAdded       = "media.movie.file.added"
	EventMovieFileRemoved     = "media.movie.file.removed"
	EventTVAdded              = "media.tv.added"
	EventTVRemoved            = "media.tv.removed"
	EventTVUpdated            = "media.tv.updated"
	EventTVEpisodeFileAdded   = "media.tv.episode.file.added"
	EventTVEpisodeFileRemoved = "media.tv.episode.file.removed"
	EventFileImported         = "media.file.imported"
	EventImportFailed         = "media.import.failed"
	EventDownloadStarted      = "download.started"
	EventDownloadCompleted    = "download.completed"
	EventDownloadFailed       = "download.failed"
	EventDownloadDispatched   = "download.dispatched"
)

type MovieAddedPayload struct {
	Title   string `json:"title"`
	MovieID string `json:"movie_id"`
	TMDBID  int32  `json:"tmdb_id"`
	Year    int32  `json:"year,omitempty"`
}

type MovieRemovedPayload struct {
	Title   string `json:"title"`
	MovieID string `json:"movie_id,omitempty"`
	TMDBID  int32  `json:"tmdb_id"`
}

type MovieFileAddedPayload struct {
	MovieID  string `json:"movie_id"`
	FilePath string `json:"file_path"`
	Quality  string `json:"quality"`
}

type TVAddedPayload struct {
	Name     string `json:"name"`
	SeriesID string `json:"series_id"`
	TMDBID   int32  `json:"tmdb_id"`
}

type TVRemovedPayload struct {
	SeriesID string `json:"series_id"`
}

type TVEpisodeFileAddedPayload struct {
	EpisodeID string `json:"episode_id"`
	FilePath  string `json:"file_path"`
	Quality   string `json:"quality"`
}

type FileImportedPayload struct {
	MediaType       string  `json:"media_type"`
	Title           string  `json:"title"`
	Year            int32   `json:"year,omitempty"`
	TMDBID          int32   `json:"tmdb_id,omitempty"`
	SeasonNumber    int32   `json:"season_number,omitempty"`
	EpisodeNumber   int32   `json:"episode_number,omitempty"`
	EpisodeNumbers  []int32 `json:"episode_numbers,omitempty"`
	AbsoluteNumber  int32   `json:"absolute_number,omitempty"`
	AirDate         string  `json:"air_date,omitempty"`
	OriginalPath    string  `json:"original_path,omitempty"`
	StorageKey      string  `json:"storage_key,omitempty"`
	DestinationPath string  `json:"destination_path,omitempty"`
	Quality         string  `json:"quality,omitempty"`
}

// ImportFailedPayload is published when post-download import cannot complete
// (scanner missing, ImportPath error, etc.).
type ImportFailedPayload struct {
	DownloadID string `json:"download_id,omitempty"`
	Path       string `json:"path,omitempty"`
	Error      string `json:"error,omitempty"`
}

type DownloadEventFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type DownloadEventPayload struct {
	ID       string             `json:"id"`
	Name     string             `json:"name,omitempty"`
	InfoHash string             `json:"info_hash,omitempty"`
	SavePath string             `json:"save_path,omitempty"`
	Label    string             `json:"label,omitempty"`
	Error    string             `json:"error,omitempty"`
	Files    []DownloadEventFile `json:"files,omitempty"`
}

type DownloadDispatchedPayload struct {
	Title            string `json:"title"`
	DownloadProtocol string `json:"download_protocol,omitempty"`
	Score            int32  `json:"score,omitempty"`
	ItemType         string `json:"item_type,omitempty"`
	ItemID           string `json:"item_id,omitempty"`
	TMDBID           int32  `json:"tmdb_id,omitempty"`
	GUID             string `json:"guid,omitempty"`
	Indexer          string `json:"indexer,omitempty"`
	Size             int64  `json:"size,omitempty"`
	DownloadID       string `json:"download_id,omitempty"`
	SeriesID         string `json:"series_id,omitempty"`
}
