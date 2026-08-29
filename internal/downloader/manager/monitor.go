package manager

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/database"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/tvcompat"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/utils"
)

// errChanWaitTimeout is how long to wait for download result after progress channel closes.
const errChanWaitTimeout = 10 * time.Second

//nolint:gocyclo // Complex monitoring logic is acceptable here
func (dm *DownloadManager) monitorDownload(
	movieID uint,
	job *downloadJob,
	outerErrChan chan error,
) {
	defer func() {
		dm.mu.Lock()
		delete(dm.jobs, movieID)
		dm.mu.Unlock()
		<-dm.semaphore
	}()

	var (
		lastProgress         float64
		lastPersistedPercent = -1
		lastProgressFlush    time.Time
		progressStagnantTime time.Time
		downloadStartTime    = time.Now()
		maxStagnantDuration  = 30 * time.Minute
	)
	stallTolerant := false
	if tolerant, ok := job.downloader.(downloader.StallTolerantDownloader); ok {
		stallTolerant = tolerant.AllowsIndefiniteStall()
	}
	stallNotified := false
	stallStore, hasStallStore := dm.db.(database.TorrentStallStore)
	if stallTolerant {
		progressStagnantTime = downloadStartTime
		if hasStallStore {
			if persisted, err := stallStore.TorrentStallNotified(context.Background(), movieID); err == nil {
				stallNotified = persisted
			}
		}
	}

	updateTicker := time.NewTicker(dm.downloadSettings.ProgressUpdateInterval)
	defer updateTicker.Stop()

	var timeoutTimer *time.Timer
	var timeoutChan <-chan time.Time
	if dm.downloadSettings.DownloadTimeout > 0 {
		timeoutTimer = time.NewTimer(dm.downloadSettings.DownloadTimeout)
		timeoutChan = timeoutTimer.C
		defer timeoutTimer.Stop()
	}

	for {
		select {
		case completed, ok := <-job.episodesChan:
			if !ok {
				job.episodesChan = nil
				continue
			}
			// Episode completion means the download is progressing; reset stagnation timer.
			if stallTolerant {
				progressStagnantTime = time.Now()
				if stallNotified && hasStallStore {
					if resetErr := stallStore.SetTorrentStallNotified(context.Background(), movieID, false); resetErr == nil {
						stallNotified = false
					}
				}
			} else {
				progressStagnantTime = time.Time{}
			}
			if updateErr := dm.db.UpdateEpisodesProgress(context.Background(), movieID, completed); updateErr != nil {
				logutils.Log.WithError(updateErr).WithField("movie_id", movieID).Error("Failed to update episodes progress")
			}
			if completed == 1 {
				// "First episode ready" only for series (multiple files); skip for single-file video/yt-dlp.
				if job.totalEpisodes > 1 {
					job.queueNotifier.OnFirstEpisodeReady(movieID, job.title)
				}
				// Probe TV compatibility as soon as first file is ready so user sees green/yellow/red immediately.
				if dm.cfg.VideoSettings.CompatibilityMode {
					ctx := context.Background()
					targetLevel := tvcompat.ParseH264Level(dm.cfg.VideoSettings.TvH264Level)
					if targetLevel <= 0 {
						targetLevel = 41
					}
					compat := tvcompat.ProbeTvCompatibility(ctx, movieID, dm.cfg.MoviePath, dm.db, targetLevel)
					// Only update DB when the probe actually determined compatibility.
					// Empty result means the probe failed (ffprobe missing, file not ready, etc.)
					// — keep the early estimate (e.g. green from file extension).
					if compat != "" {
						_ = dm.db.SetTvCompatibility(ctx, movieID, compat)
						if compat == tvcompat.TvCompatRed && dm.cfg.VideoSettings.RejectIncompatible {
							job.rejectedIncompatible = true
							job.cancel()
						}
					}
				}
			}

		case progress, ok := <-job.progressChan:
			if !ok {
				// If the download was manually stopped, progressChan may close before
				// errChan delivers ErrStoppedByUser. Don't treat this as successful completion:
				// skip SetLoaded / conversion and propagate the correct signal.
				if job.downloader.StoppedManually() {
					if job.silentStop {
						logutils.Log.WithField("movie_id", movieID).Info("Download stopped by deletion (progressChan closed first)")
						outerErrChan <- downloader.ErrStoppedByDeletion
					} else {
						logutils.Log.WithField("movie_id", movieID).Info("Download stopped by user (progressChan closed first)")
						outerErrChan <- nil
					}
					return
				}

				// progressChan closed: get actual result from downloader (success or failure).
				// Do not assume success — e.g. aria2 may have exited with error (bad magnet, etc.).
				logutils.Log.WithField("movie_id", movieID).Debug("Progress channel closed, waiting for result from errChan")
				var finalErr error
				select {
				case finalErr = <-job.errChan:
				case <-time.After(errChanWaitTimeout):
					finalErr = fmt.Errorf("timeout waiting for download result after progress channel closed")
				}
				if finalErr != nil {
					logutils.Log.WithError(finalErr).WithField("movie_id", movieID).Error("Download failed")
					outerErrChan <- utils.WrapError(finalErr, "Download failed", map[string]any{
						"movie_id": movieID,
					})
					return
				}
				logutils.Log.WithField("movie_id", movieID).Debug("errChan returned success")
				logutils.Log.WithFields(map[string]any{
					"movie_id": movieID,
					"duration": time.Since(downloadStartTime),
				}).Info("Download completed successfully")

				needWait, done, compatRed := dm.enqueueConversionIfNeeded(context.Background(), movieID, job.title)
				if compatRed && dm.cfg.VideoSettings.RejectIncompatible {
					job.queueNotifier.OnVideoNotSupported(movieID, job.title)
					outerErrChan <- nil
					return
				}
				if needWait && done != nil {
					<-done
				}
				if err := dm.db.SetLoaded(context.Background(), movieID, dm.cfg.MoviePath); err != nil {
					logutils.Log.WithError(err).WithField("movie_id", movieID).Error("Failed to mark movie as loaded")
					outerErrChan <- utils.WrapError(err, "Failed to mark movie as loaded", map[string]any{
						"movie_id": movieID,
					})
				} else {
					outerErrChan <- nil
				}
				return
			}

			currentTime := time.Now()
			progressDiff := math.Abs(progress - lastProgress)

			const significantProgressChange = 0.1
			if progressDiff > significantProgressChange {
				lastProgress = progress
				if stallTolerant {
					progressStagnantTime = currentTime
					if stallNotified && hasStallStore {
						if resetErr := stallStore.SetTorrentStallNotified(context.Background(), movieID, false); resetErr == nil {
							stallNotified = false
						}
					}
				} else {
					progressStagnantTime = time.Time{}
				}
			} else if progressStagnantTime.IsZero() && progress > 0 {
				progressStagnantTime = currentTime
			}

			const maxProgress = 100
			if progress > maxProgress {
				progress = maxProgress
			}

			percent := normalizedProgressPercent(progress)
			if shouldPersistProgress(percent, lastPersistedPercent, lastProgressFlush, currentTime) {
				if updateErr := dm.db.UpdateDownloadedPercentage(context.Background(), movieID, percent); updateErr != nil {
					logutils.Log.WithError(updateErr).WithField("movie_id", movieID).Error("Failed to update progress in database")
				} else {
					lastPersistedPercent = percent
					lastProgressFlush = currentTime
				}
			}

			if !stallTolerant && !progressStagnantTime.IsZero() && currentTime.Sub(progressStagnantTime) > maxStagnantDuration {
				err := fmt.Errorf("download appears to be stagnant (no progress for %v)", maxStagnantDuration)
				logutils.Log.WithError(err).WithField("movie_id", movieID).Warn("Download stagnant")
				outerErrChan <- err
				return
			}

		case err, ok := <-job.errChan:
			if !ok {
				// errChan must deliver nil or a concrete error before close; an empty close means the
				// downloader goroutine exited abnormally (e.g. panic) and would otherwise skip SetLoaded
				// and completion notification while the UI could already show 100% download.
				abnormal := fmt.Errorf("download ended without result (downloader channel closed unexpectedly)")
				logutils.Log.WithField("movie_id", movieID).Error(abnormal.Error())
				outerErrChan <- utils.WrapError(abnormal, "Download failed", map[string]any{
					"movie_id": movieID,
				})
				return
			}

			if errors.Is(err, downloader.ErrStoppedByUser) {
				logutils.Log.WithField("movie_id", movieID).Info("Download stopped by user")
				if job.silentStop {
					outerErrChan <- downloader.ErrStoppedByDeletion
				} else {
					outerErrChan <- nil
				}
				return
			}
			if err != nil {
				logutils.Log.WithError(err).WithField("movie_id", movieID).Error("Download failed")
				outerErrChan <- utils.WrapError(err, "Download failed", map[string]any{
					"movie_id": movieID,
				})
			} else {
				logutils.Log.WithField("movie_id", movieID).Info("Download completed successfully")

				needWait, done, compatRed := dm.enqueueConversionIfNeeded(context.Background(), movieID, job.title)
				if compatRed && dm.cfg.VideoSettings.RejectIncompatible {
					job.queueNotifier.OnVideoNotSupported(movieID, job.title)
					outerErrChan <- nil
					return
				}
				if needWait && done != nil {
					<-done
				}
				if err := dm.db.SetLoaded(context.Background(), movieID, dm.cfg.MoviePath); err != nil {
					logutils.Log.WithError(err).WithField("movie_id", movieID).Error("Failed to mark movie as loaded")
					outerErrChan <- utils.WrapError(err, "Failed to mark movie as loaded", map[string]any{
						"movie_id": movieID,
					})
				} else {
					outerErrChan <- nil
				}
			}
			return

		case <-updateTicker.C:
			if stallTolerant && dm.downloadSettings.TorrentStallWarningAfter > 0 && !stallNotified &&
				!progressStagnantTime.IsZero() && time.Since(progressStagnantTime) >= dm.downloadSettings.TorrentStallWarningAfter {
				if !hasStallStore || stallStore.SetTorrentStallNotified(context.Background(), movieID, true) == nil {
					stallNotified = true
					job.queueNotifier.OnStalled(movieID, job.title)
					logutils.Log.WithField("movie_id", movieID).Warn("Torrent download is stalled; notification sent")
				}
			}
			// Periodic logging for debugging
			dm.mu.RLock()
			if _, exists := dm.jobs[movieID]; exists {
				elapsed := time.Since(downloadStartTime)
				logutils.Log.WithFields(map[string]any{
					"movie_id":      movieID,
					"elapsed":       elapsed,
					"last_progress": lastProgress,
				}).Debug("Download status update")
			}
			dm.mu.RUnlock()

		case <-timeoutChan:
			if timeoutChan != nil {
				err := fmt.Errorf("download timeout after %v", dm.downloadSettings.DownloadTimeout)
				logutils.Log.WithError(err).WithField("movie_id", movieID).Error("Download timed out")
				outerErrChan <- err
				return
			}

		case <-job.ctx.Done():
			if job.rejectedIncompatible {
				job.queueNotifier.OnVideoNotSupported(movieID, job.title)
				outerErrChan <- nil
			} else {
				logutils.Log.WithField("movie_id", movieID).Info("Download canceled")
				outerErrChan <- job.ctx.Err()
			}
			return
		}
	}
}

const progressFlushInterval = 30 * time.Second

func normalizedProgressPercent(progress float64) int {
	if progress < 0 {
		return 0
	}
	const maxProgress = 100
	if progress > maxProgress {
		return maxProgress
	}
	return int(math.Round(progress))
}

func shouldPersistProgress(percent, lastPersisted int, lastFlush, now time.Time) bool {
	if percent == 0 {
		return false
	}
	if percent != lastPersisted {
		return true
	}
	return !lastFlush.IsZero() && now.Sub(lastFlush) >= progressFlushInterval
}

func (dm *DownloadManager) GetDownloadStatus(movieID uint) (isActive bool, progress float64, err error) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	job, exists := dm.jobs[movieID]
	if !exists {
		return false, 0, nil
	}

	movie, err := dm.db.GetMovieByID(context.Background(), movieID)
	if err != nil {
		return false, 0, err
	}

	_ = job
	return true, float64(movie.DownloadedPercentage), nil
}

func (dm *DownloadManager) GetActiveDownloads() []uint {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	downloads := make([]uint, 0, len(dm.jobs))
	for movieID := range dm.jobs {
		downloads = append(downloads, movieID)
	}
	return downloads
}

func (dm *DownloadManager) GetDownloadCount() int {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return len(dm.jobs)
}
