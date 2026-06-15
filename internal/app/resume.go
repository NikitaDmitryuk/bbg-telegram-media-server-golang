package app

import (
	"context"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/database"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader/qbittorrent"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/filemanager"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/notifier"
)

const (
	qbittorrentResumeInitialDelay = 2 * time.Second
	qbittorrentResumeMaxDelay     = 1 * time.Minute
	qbittorrentReadyTimeout       = 10 * time.Second
)

// ResumeIncompleteDownloads finds movies with an active qBittorrent hash and downloaded_percentage < 100,
// then reattaches monitoring so progress and completion are tracked again after a bot restart.
func ResumeIncompleteDownloads(a *App) {
	if a.Config.QBittorrentURL == "" {
		return
	}
	ctx := context.Background()
	movies, err := a.DB.GetIncompleteQBittorrentDownloads(ctx)
	if err != nil {
		logutils.Log.WithError(err).Error("Failed to get incomplete qBittorrent downloads for resume")
		return
	}
	if len(movies) == 0 {
		return
	}
	logutils.Log.WithField("count", len(movies)).Info("Resuming incomplete qBittorrent downloads")
	for i := range movies {
		go resumeIncompleteQBittorrentDownload(ctx, a, &movies[i])
	}
}

func resumeIncompleteQBittorrentDownload(ctx context.Context, a *App, movie *database.Movie) {
	delay := qbittorrentResumeInitialDelay
	for {
		if ctx.Err() != nil {
			return
		}
		if err := waitForQBittorrentReady(ctx, a); err != nil {
			logutils.Log.WithError(err).WithFields(map[string]any{
				"movie_id": movie.ID,
				"url":      a.Config.QBittorrentURL,
				"username": a.Config.QBittorrentUsername,
			}).Warn("qBittorrent is not ready for resume; will retry")
			sleepWithContext(ctx, delay)
			delay = nextResumeDelay(delay)
			continue
		}

		dl, err := qbittorrent.NewQBittorrentResumeDownloader(
			movie.QBittorrentHash,
			a.Config.MoviePath,
			movie.TotalEpisodes,
			movie.CompletedEpisodes,
			a.Config,
		)
		if err != nil {
			logutils.Log.WithError(err).WithField("movie_id", movie.ID).Warn("Failed to create qBittorrent resume downloader; will retry")
			sleepWithContext(ctx, delay)
			delay = nextResumeDelay(delay)
			continue
		}

		completionChan, err := a.DownloadManager.ResumeDownload(
			movie.ID,
			dl,
			movie.Name,
			movie.TotalEpisodes,
			notifier.Noop,
		)
		if err != nil {
			logutils.Log.WithError(err).WithField("movie_id", movie.ID).Warn("Failed to attach resumed qBittorrent download; will retry")
			sleepWithContext(ctx, delay)
			delay = nextResumeDelay(delay)
			continue
		}

		delay = qbittorrentResumeInitialDelay
		err = <-completionChan
		if err == nil {
			logutils.Log.WithField("movie_id", movie.ID).Info("Resumed qBittorrent download completed successfully")
			if cleanupErr := filemanager.DeleteTemporaryFilesByMovieID(movie.ID, a.Config.MoviePath, a.DB, a.DownloadManager); cleanupErr != nil {
				logutils.Log.WithError(cleanupErr).
					WithField("movie_id", movie.ID).
					Warn("Failed to delete temporary files after resumed download")
			}
			return
		}

		logutils.Log.WithError(err).WithFields(map[string]any{
			"movie_id": movie.ID,
			"hash":     movie.QBittorrentHash,
		}).Warn("Resumed qBittorrent download stopped with error; keeping database record and retrying")
		sleepWithContext(ctx, delay)
		delay = nextResumeDelay(delay)
	}
}

func waitForQBittorrentReady(ctx context.Context, a *App) error {
	client, err := qbittorrent.NewClient(a.Config.QBittorrentURL, a.Config.QBittorrentUsername, a.Config.QBittorrentPassword)
	if err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, qbittorrentReadyTimeout)
	defer cancel()
	version, err := client.CheckLogin(checkCtx)
	if err != nil {
		return err
	}
	logutils.Log.WithFields(map[string]any{
		"url":      a.Config.QBittorrentURL,
		"username": a.Config.QBittorrentUsername,
		"version":  version,
	}).Info("qBittorrent Web API login check succeeded")
	return nil
}

func nextResumeDelay(current time.Duration) time.Duration {
	next := current * 2
	if next > qbittorrentResumeMaxDelay {
		return qbittorrentResumeMaxDelay
	}
	return next
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
