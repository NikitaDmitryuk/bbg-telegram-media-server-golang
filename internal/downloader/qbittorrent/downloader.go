package qbittorrent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader"
	aria2pkg "github.com/NikitaDmitryuk/telegram-media-server/internal/downloader/torrent"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/tvcompat"
	"github.com/go-bittorrent/magneturi"
)

const (
	pollInterval        = 3 * time.Second
	delayAfterAdd       = 500 * time.Millisecond
	progressPercentMax  = 100
	stopTimeout         = 15 * time.Second
	deleteTimeout       = 10 * time.Second
	controlRetryInitial = 2 * time.Second
	controlRetryMax     = time.Minute
)

// episodesChanCapacity returns the buffer size for incremental episode notifications.
// It must be at least the number of video episodes so run() never blocks on send while
// the download manager is busy (e.g. TV compatibility ffprobe on first episode).
func episodesChanCapacity(totalVideo int) int {
	if totalVideo < 2 {
		return 1
	}
	return totalVideo
}

// QBittorrentDownloader implements downloader.Downloader using qBittorrent Web API.
type QBittorrentDownloader struct {
	torrentFileName string
	downloadDir     string
	cfg             *config.Config
	magnetURI       string
	client          *Client

	mu                       sync.Mutex
	hash                     string
	hashChan                 chan string       // sends qBittorrent torrent hash once when known (for DB persistence)
	onHashKnown              func(hash string) // optional; called synchronously when hash is known so manager can persist to DB before restart
	stoppedManually          bool
	resumeHash               string // when set, skip add and poll by this hash (resume after restart)
	initialCompletedEpisodes int    // for resume: episodes already completed before restart (from DB)
	totalEpisodesStored      int    // for resume: total episode count when no torrent file (from DB)
	onMagnetMetadata         func(paths []string, totalBytes int64, videoFileCount int)
	magnetDBSynced           bool // true after first successful torrents/files sync to DB (magnet only)
}

// NewQBittorrentDownloader creates a downloader that uses qBittorrent.
// torrentFileName is the .torrent or .magnet file name under downloadDir.
func NewQBittorrentDownloader(torrentFileName, moviePath string, cfg *config.Config) (downloader.Downloader, error) {
	if cfg.QBittorrentURL == "" {
		return nil, fmt.Errorf("QBittorrentURL is not set")
	}
	client, err := NewClient(cfg.QBittorrentURL, cfg.QBittorrentUsername, cfg.QBittorrentPassword)
	if err != nil {
		return nil, err
	}
	d := &QBittorrentDownloader{
		torrentFileName: torrentFileName,
		downloadDir:     moviePath,
		cfg:             cfg,
		client:          client,
		hashChan:        make(chan string, 1),
	}
	if strings.HasSuffix(strings.ToLower(torrentFileName), ".magnet") {
		path := filepath.Join(moviePath, torrentFileName)
		if b, err := os.ReadFile(path); err == nil {
			d.magnetURI = strings.TrimSpace(string(b))
		}
	}
	return d, nil
}

// NewQBittorrentResumeDownloader creates a downloader that monitors an already-added torrent by hash (e.g. after bot restart).
// completedEpisodes is the number of episodes already completed (from DB) so we do not re-send OnFirstEpisodeReady.
func NewQBittorrentResumeDownloader(
	hash, downloadDir string,
	totalEpisodes, completedEpisodes int,
	cfg *config.Config,
) (downloader.Downloader, error) {
	if cfg.QBittorrentURL == "" {
		return nil, fmt.Errorf("QBittorrentURL is not set")
	}
	client, err := NewClient(cfg.QBittorrentURL, cfg.QBittorrentUsername, cfg.QBittorrentPassword)
	if err != nil {
		return nil, err
	}
	return &QBittorrentDownloader{
		downloadDir:              downloadDir,
		cfg:                      cfg,
		client:                   client,
		resumeHash:               hash,
		initialCompletedEpisodes: completedEpisodes,
		totalEpisodesStored:      totalEpisodes,
	}, nil
}

func (d *QBittorrentDownloader) parseMeta() (*aria2pkg.Meta, error) {
	if d.magnetURI != "" {
		return d.magnetDummyMeta()
	}
	torrentPath := filepath.Join(d.downloadDir, d.torrentFileName)
	return aria2pkg.ParseMeta(torrentPath)
}

func (d *QBittorrentDownloader) magnetDummyMeta() (*aria2pkg.Meta, error) {
	m := &aria2pkg.Meta{}
	m.Info.Name = "Magnet download"
	if parsed, err := magneturi.Parse(d.magnetURI); err == nil {
		if parsed.DisplayName != "" {
			m.Info.Name = parsed.DisplayName
		}
		if parsed.ExactLength > 0 {
			m.Info.Length = parsed.ExactLength
		}
	}
	return m, nil
}

func (d *QBittorrentDownloader) GetTitle() (string, error) {
	if d.resumeHash != "" {
		return "", nil
	}
	meta, err := d.parseMeta()
	if err != nil {
		return "", err
	}
	return meta.Info.Name, nil
}

func (d *QBittorrentDownloader) GetFiles() (mainFiles, tempFiles []string, err error) {
	if d.resumeHash != "" {
		return nil, nil, fmt.Errorf("resume downloader has no torrent meta")
	}
	meta, err := d.parseMeta()
	if err != nil {
		return nil, nil, err
	}
	if len(meta.Info.Files) > 0 {
		for _, file := range meta.Info.Files {
			if meta.Info.Name == "" {
				return nil, nil, fmt.Errorf("torrent meta does not contain a root directory name")
			}
			if len(file.Path) == 0 {
				return nil, nil, fmt.Errorf("file path is empty in torrent meta")
			}
			mainFiles = append(mainFiles, filepath.Join(meta.Info.Name, filepath.Join(file.Path...)))
		}
	} else {
		mainFiles = append(mainFiles, meta.Info.Name)
	}
	tempFiles = []string{d.torrentFileName}
	return mainFiles, tempFiles, nil
}

func (d *QBittorrentDownloader) GetFileSize() (int64, error) {
	if d.resumeHash != "" {
		return 0, nil
	}
	meta, err := d.parseMeta()
	if err != nil {
		logutils.Log.WithError(err).Warn("Failed to parse torrent metadata for file size, returning 0")
		return 0, nil
	}
	if len(meta.Info.Files) > 0 {
		var total int64
		for _, f := range meta.Info.Files {
			total += f.Length
		}
		return total, nil
	}
	return meta.Info.Length, nil
}

func (d *QBittorrentDownloader) TotalEpisodes() int {
	if d.resumeHash != "" {
		return d.totalEpisodesStored
	}
	meta, err := d.parseMeta()
	if err != nil {
		return 0
	}
	var n int
	for i := range meta.Info.Files {
		path := filepath.Join(meta.Info.Name, filepath.Join(meta.Info.Files[i].Path...))
		if tvcompat.IsVideoFilePath(path) {
			n++
		}
	}
	if n == 0 && len(meta.Info.Files) == 0 && meta.Info.Name != "" {
		return 1
	}
	return n
}

func (d *QBittorrentDownloader) StoppedManually() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stoppedManually
}

func (*QBittorrentDownloader) AllowsIndefiniteStall() bool { return true }

func retryDelay(attempt int) time.Duration {
	delay := controlRetryInitial
	for i := 0; i < attempt && delay < controlRetryMax; i++ {
		delay *= 2
	}
	if delay > controlRetryMax {
		delay = controlRetryMax
	}
	// Small bounded jitter prevents several resumed jobs from retrying in lockstep.
	jitterRange := delay / 5
	if jitterRange == 0 {
		return delay
	}
	jitter := time.Duration(time.Now().UnixNano()%int64(2*jitterRange)) - jitterRange
	return delay + jitter
}

func retryControlCall(ctx context.Context, operation string, call func() error) error {
	return retryControlCallWithBackoff(ctx, operation, call, retryDelay)
}

func retryControlCallWithBackoff(
	ctx context.Context,
	operation string,
	call func() error,
	nextDelay func(int) time.Duration,
) error {
	for attempt := 0; ; attempt++ {
		err := call()
		if err == nil {
			return nil
		}
		delay := nextDelay(attempt)
		if logutils.Log != nil {
			logutils.Log.WithError(err).WithFields(map[string]any{
				"operation": operation,
				"retry_in":  delay,
			}).Warn("qBittorrent control request failed; retrying")
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (d *QBittorrentDownloader) torrentsInfoResilient(
	ctx context.Context,
	hash, sortBy string,
	reverse bool,
) ([]TorrentInfo, error) {
	var info []TorrentInfo
	err := retryControlCall(ctx, "torrents/info", func() error {
		var err error
		info, err = d.client.TorrentsInfo(ctx, hash, sortBy, reverse)
		return err
	})
	return info, err
}

func (d *QBittorrentDownloader) setHash(h string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hash = h
}

func (d *QBittorrentDownloader) StartDownload(
	ctx context.Context,
) (progressChan chan float64, errChan chan error, episodesChan <-chan int, err error) {
	progressChan = make(chan float64)
	errChan = make(chan error, 1)
	var epCh chan int
	var totalVideo int
	var meta *aria2pkg.Meta
	if d.resumeHash != "" {
		totalVideo = d.totalEpisodesStored
		if totalVideo > 1 {
			epCh = make(chan int, episodesChanCapacity(totalVideo))
			episodesChan = epCh
		}
		go d.run(ctx, nil, totalVideo, progressChan, errChan, epCh)
		return progressChan, errChan, episodesChan, nil
	}
	meta, metaErr := d.parseMeta()
	if metaErr != nil {
		return nil, nil, nil, fmt.Errorf("failed to parse torrent meta: %w", metaErr)
	}
	d.hashChan = make(chan string, 1)
	totalVideo = d.TotalEpisodes()
	if totalVideo > 1 {
		epCh = make(chan int, episodesChanCapacity(totalVideo))
		episodesChan = epCh
	}
	go d.run(ctx, meta, totalVideo, progressChan, errChan, epCh)
	return progressChan, errChan, episodesChan, nil
}

// QBittorrentHashChan implements downloader.QBittorrentHashDownloader.
func (d *QBittorrentDownloader) QBittorrentHashChan() <-chan string {
	return d.hashChan
}

// SetOnHashKnown implements downloader.OnHashKnownSetter.
func (d *QBittorrentDownloader) SetOnHashKnown(cb func(hash string)) {
	d.onHashKnown = cb
}

// SetOnMagnetMetadataReady implements downloader.MagnetMetadataSyncSetter.
func (d *QBittorrentDownloader) SetOnMagnetMetadataReady(cb func(paths []string, totalBytes int64, videoFileCount int)) {
	d.onMagnetMetadata = cb
}

//nolint:gocyclo // run orchestrates login, add, find hash, poll loop; splitting would obscure flow.
func (d *QBittorrentDownloader) run(
	ctx context.Context,
	meta *aria2pkg.Meta,
	totalVideo int,
	progressChan chan float64,
	errChan chan error,
	episodesChan chan int,
) {
	defer close(errChan)
	if episodesChan != nil {
		defer close(episodesChan)
	}
	defer close(progressChan)

	if err := retryControlCall(ctx, "login", func() error { return d.client.Login(ctx) }); err != nil {
		errChan <- err
		return
	}

	var our *TorrentInfo
	if d.resumeHash != "" {
		// Resume: skip add, verify torrent still exists in qBittorrent
		info, err := d.torrentsInfoResilient(ctx, d.resumeHash, "", false)
		if err != nil {
			errChan <- err
			return
		}
		if len(info) == 0 {
			our = &TorrentInfo{Hash: d.resumeHash}
			logutils.Log.Warn("Resumed torrent is absent in qBittorrent; waiting for it to reappear")
		} else {
			our = &info[0]
		}
		d.setHash(our.Hash)
	} else {
		savepath := d.downloadDir
		isSeries := totalVideo > 1
		addOpts := &AddTorrentOptions{
			SequentialDownload: isSeries,
			FirstLastPiecePrio: isSeries,
		}
		if d.magnetURI != "" {
			if err := d.client.AddTorrentFromURLs(ctx, d.magnetURI, savepath, addOpts); err != nil {
				errChan <- fmt.Errorf("%w: qBittorrent add magnet: %w", downloader.ErrAmbiguousControlPlane, err)
				return
			}
		} else {
			torrentPath := filepath.Join(d.downloadDir, d.torrentFileName)
			body, err := os.ReadFile(torrentPath)
			if err != nil {
				errChan <- fmt.Errorf("read torrent file: %w", err)
				return
			}
			if err := d.client.AddTorrentFromFile(ctx, d.torrentFileName, body, savepath, addOpts); err != nil {
				errChan <- fmt.Errorf("%w: qBittorrent add file: %w", downloader.ErrAmbiguousControlPlane, err)
				return
			}
		}

		// Find our torrent: last added (most recent added_on).
		time.Sleep(delayAfterAdd)
		list, err := d.torrentsInfoResilient(ctx, "", "added_on", true)
		if err != nil {
			errChan <- fmt.Errorf("qBittorrent list after add: %w", err)
			return
		}
		if len(list) == 0 {
			errChan <- fmt.Errorf("qBittorrent: torrent not found after add")
			return
		}
		// Prefer match by name; otherwise take the newest.
		for i := range list {
			if list[i].Name == meta.Info.Name {
				our = &list[i]
				break
			}
		}
		if our == nil {
			our = &list[0]
		}
		d.setHash(our.Hash)
		logutils.Log.WithField("hash", our.Hash).Info("qBittorrent torrent hash determined")
		// Persist hash synchronously so it survives process restart (callback runs in this goroutine before channel send).
		if d.onHashKnown != nil {
			d.onHashKnown(our.Hash)
		} else {
			logutils.Log.Warn("onHashKnown callback is nil, hash will not be persisted to DB")
		}
		// Notify manager via channel (fallback / idempotent second persist).
		if d.hashChan != nil {
			select {
			case d.hashChan <- our.Hash:
			default:
			}
			close(d.hashChan)
			d.hashChan = nil
		}

		if totalVideo > 1 {
			d.applyLexicographicPriorities(ctx, our.Hash)
		}
	}

	effectiveVideoCount := totalVideo

	// Send initial 0% so the UI shows progress from the start
	select {
	case progressChan <- 0:
	case <-ctx.Done():
		if d.StoppedManually() {
			errChan <- downloader.ErrStoppedByUser
		} else {
			errChan <- ctx.Err()
		}
		return
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastProgress float64
	lastCompletedEpisodes := d.initialCompletedEpisodes
	missingLogged := false
	attentionState := ""
	for {
		select {
		case <-ctx.Done():
			if d.StoppedManually() {
				errChan <- downloader.ErrStoppedByUser
			} else {
				errChan <- ctx.Err()
			}
			return
		case <-ticker.C:
			// Refresh torrent info
			info, err := d.torrentsInfoResilient(ctx, our.Hash, "", false)
			if err != nil {
				errChan <- err
				return
			}
			if len(info) == 0 {
				if d.StoppedManually() {
					errChan <- downloader.ErrStoppedByUser
				} else if !missingLogged {
					logutils.Log.Warn("Torrent is absent in qBittorrent; keeping the download and waiting")
					missingLogged = true
				}
				continue
			}
			missingLogged = false
			t := info[0]
			state := strings.ToLower(strings.TrimSpace(t.State))
			if state == "error" || state == "missingfiles" {
				if attentionState != state {
					logutils.Log.WithField("state", state).Warn("Torrent requires attention; keeping the download active")
					attentionState = state
				}
				continue
			}
			attentionState = ""
			logutils.Log.WithFields(map[string]any{
				"progress":    t.Progress,
				"size":        t.Size,
				"total_size":  t.TotalSize,
				"downloaded":  t.Downloaded,
				"amount_left": t.AmountLeft,
				"completed":   t.Completed,
				"state":       t.State,
			}).Debug("qBittorrent API torrents/info response")
			progress := t.Progress * 100
			if progress > progressPercentMax {
				progress = progressPercentMax
			}
			if progress != lastProgress {
				lastProgress = progress
				select {
				case progressChan <- progress:
				case <-ctx.Done():
					if d.StoppedManually() {
						errChan <- downloader.ErrStoppedByUser
					} else {
						errChan <- ctx.Err()
					}
					return
				}
			}
			// Magnet: replace placeholder DB paths (display name as "file") with real paths from qBittorrent.
			if d.magnetURI != "" && !d.magnetDBSynced {
				qfiles, qerr := d.client.TorrentFiles(ctx, our.Hash)
				if qerr == nil && len(qfiles) > 0 {
					d.magnetDBSynced = true
					relPaths := make([]string, 0, len(qfiles))
					var totalBytes int64
					for i := range qfiles {
						relPaths = append(relPaths, filepath.FromSlash(qfiles[i].Name))
						totalBytes += qfiles[i].Size
					}
					vCount := countVideoPathsInTorrentFiles(qfiles)
					if vCount > effectiveVideoCount {
						effectiveVideoCount = vCount
					}
					if d.onMagnetMetadata != nil {
						d.onMagnetMetadata(relPaths, totalBytes, vCount)
					}
					if effectiveVideoCount > 1 {
						d.applyLexicographicPriorities(ctx, our.Hash)
					}
				}
			}

			// Track completed video files so OnFirstEpisodeReady and episode progress fire incrementally
			if episodesChan != nil && effectiveVideoCount > 1 {
				files, fileErr := d.client.TorrentFiles(ctx, our.Hash)
				if fileErr == nil {
					completed := countCompletedVideoFiles(files)
					if completed > lastCompletedEpisodes {
						lastCompletedEpisodes = completed
						select {
						case episodesChan <- completed:
						case <-ctx.Done():
							if d.StoppedManually() {
								errChan <- downloader.ErrStoppedByUser
							} else {
								errChan <- ctx.Err()
							}
							return
						}
					}
				}
			}
			// Do not trust progress/amount_left alone: qBittorrent can report 100% or 0 left while
			// still in metaDL, allocating, downloading, etc. That used to mark the movie complete in
			// the API, enqueue TV conversion on missing/partial files, and block forever on <-done.
			if !qbittorrentTorrentReadyToFinalize(&t) {
				continue
			}
			if effectiveVideoCount > 1 {
				files, fileErr := d.client.TorrentFiles(ctx, our.Hash)
				if fileErr != nil {
					continue
				}
				if countCompletedVideoFiles(files) < effectiveVideoCount {
					continue
				}
			}
			if episodesChan != nil && effectiveVideoCount > 0 && lastCompletedEpisodes != effectiveVideoCount {
				select {
				case episodesChan <- effectiveVideoCount:
				case <-ctx.Done():
					if d.StoppedManually() {
						errChan <- downloader.ErrStoppedByUser
					} else {
						errChan <- ctx.Err()
					}
					return
				}
			}
			d.removeTorrentOnCompletion(our.Hash)
			errChan <- nil
			return
		}
	}
}

// removeTorrentOnCompletion removes the torrent from qBittorrent immediately after
// download finishes, using the existing authenticated client session. This is more
// reliable than the DB-based fallback in RemoveQBittorrentTorrent because it doesn't
// depend on hash persistence to the database.
func (d *QBittorrentDownloader) removeTorrentOnCompletion(hash string) {
	ctx, cancel := context.WithTimeout(context.Background(), deleteTimeout)
	defer cancel()
	if err := d.client.DeleteTorrent(ctx, hash, false); err != nil {
		logutils.Log.WithError(err).WithField("hash", hash).Warn("Failed to remove torrent from qBittorrent on completion")
		return
	}
	logutils.Log.WithField("hash", hash).Info("Removed completed torrent from qBittorrent")
}

// qbittorrentTorrentReadyToFinalize is true when torrent state and byte/progress fields agree
// that the payload is actually done (typically seeding), not still fetching metadata or pieces.
func qbittorrentTorrentReadyToFinalize(t *TorrentInfo) bool {
	if t == nil {
		return false
	}
	st := strings.ToLower(strings.TrimSpace(t.State))
	switch st {
	case "error", "missingfiles":
		return false
	case "metadl", "allocating", "downloading", "stalleddl", "queueddl", "forceddl", "checkingdl", "moving", "checkingresumedata":
		return false
	case "pauseddl":
		return t.Progress >= 1.0 && t.Size > 0 && t.AmountLeft == 0
	default:
		// e.g. uploading, stalledUP, pausedUP, queuedUP, forcedUP, checkingUP
	}
	if t.Progress >= 1.0 {
		return true
	}
	return t.Size > 0 && t.AmountLeft == 0
}

func countVideoPathsInTorrentFiles(files []TorrentFileInfo) int {
	n := 0
	for i := range files {
		if tvcompat.IsVideoFilePath(files[i].Name) {
			n++
		}
	}
	return n
}

func countCompletedVideoFiles(files []TorrentFileInfo) int {
	n := 0
	for _, f := range files {
		if tvcompat.IsVideoFilePath(f.Name) && f.Progress >= 1.0 {
			n++
		}
	}
	return n
}

const (
	priorityNormal  = 1
	priorityHigh    = 6
	priorityMaximal = 7
)

// applyLexicographicPriorities sets file download priorities so that earlier
// video files (lexicographic order by name) are downloaded first. The first
// video file gets maximal priority, the second gets high, and the rest get
// normal. Combined with sequential download mode this ensures the first
// episode finishes as soon as possible without blocking the rest.
func (d *QBittorrentDownloader) applyLexicographicPriorities(ctx context.Context, hash string) {
	files, err := d.client.TorrentFiles(ctx, hash)
	if err != nil {
		logutils.Log.WithError(err).Warn("Failed to get torrent files for priority setup")
		return
	}

	type videoEntry struct {
		index int
		name  string
	}
	var videos []videoEntry
	for _, f := range files {
		if tvcompat.IsVideoFilePath(f.Name) {
			videos = append(videos, videoEntry{index: f.Index, name: f.Name})
		}
	}
	if len(videos) <= 1 {
		return
	}
	sort.Slice(videos, func(i, j int) bool { return videos[i].name < videos[j].name })

	for rank, v := range videos {
		prio := priorityNormal
		switch rank {
		case 0:
			prio = priorityMaximal
		case 1:
			prio = priorityHigh
		}
		if err := d.client.SetFilePriority(ctx, hash, fmt.Sprintf("%d", v.index), prio); err != nil {
			logutils.Log.WithError(err).WithField("file", v.name).Warn("Failed to set file priority")
		}
	}

	logutils.Log.WithFields(map[string]any{
		"hash":        hash,
		"video_files": len(videos),
		"first_file":  videos[0].name,
	}).Info("Applied lexicographic file priorities for series")
}

func (d *QBittorrentDownloader) StopDownload() error {
	d.mu.Lock()
	d.stoppedManually = true
	hash := d.hash
	d.mu.Unlock()
	if hash == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if err := d.client.Login(ctx); err != nil {
		logutils.Log.WithError(err).Warn("qBittorrent login before delete failed")
		return nil
	}
	if err := d.client.DeleteTorrent(ctx, hash, false); err != nil {
		logutils.Log.WithError(err).WithField("hash", hash).Warn("qBittorrent delete failed")
		return err
	}
	return nil
}

// GetEarlyTvCompatibility returns preliminary TV compatibility from torrent file names, or yellow for magnet.
func (d *QBittorrentDownloader) GetEarlyTvCompatibility(_ context.Context) (string, error) {
	mainFiles, _, err := d.GetFiles()
	if err != nil {
		return "", err
	}
	compat := tvcompat.CompatFromTorrentFileNames(mainFiles)
	if compat == "" && d.magnetURI != "" {
		return tvcompat.TvCompatYellow, nil
	}
	return compat, nil
}

// Ensure QBittorrentDownloader implements downloader.Downloader and optional interfaces.
var (
	_ downloader.Downloader                = (*QBittorrentDownloader)(nil)
	_ downloader.EarlyCompatDownloader     = (*QBittorrentDownloader)(nil)
	_ downloader.QBittorrentHashDownloader = (*QBittorrentDownloader)(nil)
	_ downloader.OnHashKnownSetter         = (*QBittorrentDownloader)(nil)
	_ downloader.MagnetMetadataSyncSetter  = (*QBittorrentDownloader)(nil)
)
