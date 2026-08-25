package contracts

import mediacontracts "github.com/Muxcore-Media/contracts-media/events"

// Media / download domain event types and payloads.
// Deprecated: import github.com/Muxcore-Media/contracts-media/events directly.
// These aliases preserve compatibility for modules still using core/pkg/contracts.

const (
	EventMovieAdded           = mediacontracts.EventMovieAdded
	EventMovieRemoved         = mediacontracts.EventMovieRemoved
	EventMovieUpdated         = mediacontracts.EventMovieUpdated
	EventMovieFileAdded       = mediacontracts.EventMovieFileAdded
	EventMovieFileRemoved     = mediacontracts.EventMovieFileRemoved
	EventMovieRequested       = mediacontracts.EventMovieRequested
	EventTVAdded              = mediacontracts.EventTVAdded
	EventTVRemoved            = mediacontracts.EventTVRemoved
	EventTVUpdated            = mediacontracts.EventTVUpdated
	EventTVEpisodeFileAdded   = mediacontracts.EventTVEpisodeFileAdded
	EventTVEpisodeFileRemoved = mediacontracts.EventTVEpisodeFileRemoved
	EventTVRequested          = mediacontracts.EventTVRequested
	EventFileImported         = mediacontracts.EventFileImported
	EventImportFailed         = mediacontracts.EventImportFailed
	EventDownloadStarted      = mediacontracts.EventDownloadStarted
	EventDownloadCompleted    = mediacontracts.EventDownloadCompleted
	EventDownloadFailed       = mediacontracts.EventDownloadFailed
	EventDownloadDispatched   = mediacontracts.EventDownloadDispatched
)

type (
	MovieAddedPayload         = mediacontracts.MovieAddedPayload
	MovieRemovedPayload       = mediacontracts.MovieRemovedPayload
	MovieFileAddedPayload     = mediacontracts.MovieFileAddedPayload
	TVAddedPayload            = mediacontracts.TVAddedPayload
	TVRemovedPayload          = mediacontracts.TVRemovedPayload
	TVEpisodeFileAddedPayload = mediacontracts.TVEpisodeFileAddedPayload
	FileImportedPayload       = mediacontracts.FileImportedPayload
	ImportFailedPayload       = mediacontracts.ImportFailedPayload
	DownloadEventFile         = mediacontracts.DownloadEventFile
	DownloadEventPayload      = mediacontracts.DownloadEventPayload
	DownloadDispatchedPayload = mediacontracts.DownloadDispatchedPayload
)
