package contracts

import "context"

// MediaType is a user-defined string that names a media type (e.g. "movie", "comic-book", "4k-movie").
// The constants below are common examples, not an exhaustive list — any string can be a media type.
// Each media type is associated with a module that handles its behavior. Multiple instances of the
// same type can coexist with different modules or configurations (e.g. "movie-720p", "movie-1080p",
// "movie-4k" all backed by the same or different movie modules).
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

// MediaFieldType enumerates the types supported in media type schemas.
type MediaFieldType string

const (
	FieldTypeString      MediaFieldType = "string"
	FieldTypeInt         MediaFieldType = "int"
	FieldTypeFloat       MediaFieldType = "float"
	FieldTypeBool        MediaFieldType = "bool"
	FieldTypeStringSlice MediaFieldType = "string_slice"
)

// MediaFieldSchema describes one field in a media type's metadata schema.
type MediaFieldSchema struct {
	Key         string
	Type        MediaFieldType
	Description string
}

// MediaTypeSchema is the complete metadata schema for a single media type.
// Modules that own a media type register one of these at Init time.
type MediaTypeSchema struct {
	MediaType MediaType
	Fields    []MediaFieldSchema
	ModuleID  string
}

// MediaTypeSchemaProvider is an optional interface for modules that own a media type.
// Core discovers schemas at Init time via type assertion.
type MediaTypeSchemaProvider interface {
	MediaTypeSchema() MediaTypeSchema
}

// MediaObject represents an item in the media library.
// Core fields (ID, Type, Title, Assets) are universal. Type-specific metadata
// lives in Fields, validated against the MediaTypeSchema registered by the
// module that owns the media type.
type MediaObject struct {
	ID     string
	Type   MediaType
	Title  string
	Assets []AssetRef
	Fields map[string]any
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
