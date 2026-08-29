package downloader

import (
	"context"
	"errors"
)

var ErrVideoAuthenticationRequired = errors.New(
	"video provider authentication is required; configure a valid yt-dlp cookies file",
)

// ErrAmbiguousControlPlane means a mutating external API request failed without
// proving whether the remote side applied it. Automatic cleanup must not delete
// the local record or remote data in this case.
var ErrAmbiguousControlPlane = errors.New("external download control request has an ambiguous result")

type Downloader interface {
	GetTitle() (string, error)
	GetFiles() ([]string, []string, error)
	GetFileSize() (int64, error)
	TotalEpisodes() int
	StoppedManually() bool
	StartDownload(ctx context.Context) (progressChan chan float64, errChan chan error, episodesChan <-chan int, err error)
	StopDownload() error
}

// EarlyCompatDownloader: optional; manager uses GetEarlyTvCompatibility to show TV compat from metadata before file is ready.
type EarlyCompatDownloader interface {
	Downloader
	GetEarlyTvCompatibility(ctx context.Context) (string, error)
}

// QBittorrentHashDownloader: optional; manager uses QBittorrentHashChan to persist the torrent hash
// for removal from qBittorrent on movie delete.
type QBittorrentHashDownloader interface {
	Downloader
	QBittorrentHashChan() <-chan string // sends the qBittorrent torrent hash once when known, then closes
}

// OnHashKnownSetter: optional; manager sets callback to persist hash synchronously
// so it survives process restart (channel-based persist can be lost between send and DB write).
type OnHashKnownSetter interface {
	SetOnHashKnown(cb func(hash string))
}

// StallTolerantDownloader marks long-lived downloads whose lack of progress is
// expected and must not be treated as a terminal failure (for example torrents
// waiting for metadata or peers).
type StallTolerantDownloader interface {
	Downloader
	AllowsIndefiniteStall() bool
}

// MagnetMetadataSyncSetter: optional; qBittorrent magnet downloads register placeholder paths until
// the swarm provides metadata — then real relative paths and sizes are synced to the DB via this callback.
type MagnetMetadataSyncSetter interface {
	SetOnMagnetMetadataReady(cb func(relativePaths []string, totalBytes int64, videoFileCount int))
}

type Updater interface {
	RunUpdate(ctx context.Context)
}
