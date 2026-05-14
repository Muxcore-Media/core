package contracts

import "context"

type DownloadStatus string

const (
	DownloadQueued      DownloadStatus = "queued"
	DownloadDownloading DownloadStatus = "downloading"
	DownloadCompleted   DownloadStatus = "completed"
	DownloadFailed      DownloadStatus = "failed"
	DownloadPaused      DownloadStatus = "paused"
)

type DownloadTask struct {
	ID         string
	MagnetURI  string
	TorrentURL string
	NZBData    []byte
	DestPath   string
	Label      string
	Priority   int
}

type DownloadInfo struct {
	ID           string
	Name         string
	Status       DownloadStatus
	Progress     float64
	SizeBytes    int64
	Downloaded   int64
	Uploaded     int64
	SpeedDown    int64
	SpeedUp      int64
	ETA          int64
	Seeders      int
	Leechers     int
	Files        []DownloadFileInfo
}

type DownloadFileInfo struct {
	Name string
	Size int64
	Path string
}

type Downloader interface {
	Add(ctx context.Context, task DownloadTask) (string, error)
	Remove(ctx context.Context, id string, deleteData bool) error
	Pause(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	Status(ctx context.Context, id string) (DownloadInfo, error)
	List(ctx context.Context) ([]DownloadInfo, error)
}
