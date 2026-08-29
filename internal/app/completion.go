package app

import (
	"errors"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/filemanager"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/notifier"
)

// RunCompletionLoop waits for the download to complete (via completionChan from the manager),
// then performs cleanup (temp files, delete on failure) and notifies via the given notifier.
// It must not read progressChan — the manager's monitor is the only consumer of progress so it can write progress to the DB.
func RunCompletionLoop(
	a *App,
	completionChan <-chan error,
	dl downloader.Downloader,
	movieID uint,
	title string,
	compl notifier.CompletionNotifier,
) {
	err := <-completionChan
	if errors.Is(err, downloader.ErrStoppedByDeletion) {
		logutils.Log.Info("Download stopped by deletion queue (no user notification)")
		return
	}
	if dl.StoppedManually() {
		logutils.Log.Info("Download was manually stopped")
		compl.OnStopped(movieID, title)
		return
	}
	if err != nil {
		logutils.Log.WithError(err).Error("Download failed")
		if errors.Is(err, downloader.ErrAmbiguousControlPlane) {
			logutils.Log.WithField("movie_id", movieID).
				Warn("Keeping download record after ambiguous external control request")
			compl.OnFailed(movieID, title, err)
			return
		}
		if deleteErr := filemanager.DeleteMovie(movieID, a.Config.MoviePath, a.DB, a.DownloadManager); deleteErr != nil {
			logutils.Log.WithError(deleteErr).Error("Failed to delete movie after download failed")
		}
		compl.OnFailed(movieID, title, err)
		return
	}
	logutils.Log.Info("Download completed successfully")
	// Notify first: cleanup (temp files) can block or fail on some setups; webhooks must not depend on it.
	// Magnet dn/titles with odd bytes used to break JSON marshal in SendCompletionWebhook — fixed there too.
	compl.OnCompleted(movieID, title)
	if err := filemanager.DeleteTemporaryFilesByMovieID(movieID, a.Config.MoviePath, a.DB, a.DownloadManager); err != nil {
		logutils.Log.WithError(err).Error("Failed to delete temporary files after download")
	}
	// qBittorrent: torrent is already removed in QBittorrentDownloader.removeTorrentOnCompletion on success.
	// RemoveQBittorrentTorrent remains for DeleteMovie (user/library cleanup) and failed-download paths.
}
