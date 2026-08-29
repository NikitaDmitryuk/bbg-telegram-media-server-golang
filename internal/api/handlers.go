package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/app"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/database"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader/factory"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/filemanager"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/prowlarr"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/utils"
)

// Health returns 200 and {"status":"ok"}.
func Health(w http.ResponseWriter, _ *http.Request, _ *app.App) {
	resp := HealthResponse{Status: "ok"}
	writeJSON(w, http.StatusOK, resp)
}

// ListDownloads returns GET /api/v1/downloads — best effort snapshot of queue + active + DB.
func ListDownloads(w http.ResponseWriter, r *http.Request, a *app.App) {
	ctx := r.Context()
	items := make([]DownloadItem, 0)
	seen := make(map[uint]struct{})

	// Queue items
	for _, q := range a.DownloadManager.GetQueueItems() {
		movieID := uintFromMap(q, "movie_id")
		title, _ := q["title"].(string)
		pos, _ := q["position"].(int)
		items = append(items, DownloadItem{
			ID:              movieID,
			Title:           title,
			Status:          "queued",
			Progress:        0,
			PositionInQueue: &pos,
		})
		if movieID != 0 {
			seen[movieID] = struct{}{}
		}
	}

	// Active downloads (from jobs)
	for _, movieID := range a.DownloadManager.GetActiveDownloads() {
		movie, err := a.DB.GetMovieByID(ctx, movieID)
		if err != nil {
			logutils.Log.WithError(err).WithFields(map[string]any{
				"movie_id":   movieID,
				"request_id": RequestIDFromContext(ctx),
			}).Warn("ListDownloads: skip active download (GetMovieByID failed)")
			continue
		}
		status := downloadStatusFromMovie(&movie)
		items = append(items, DownloadItem{
			ID:                 movie.ID,
			Title:              movie.Name,
			Status:             status,
			Progress:           movie.DownloadedPercentage,
			ConversionProgress: movie.ConversionPercentage,
			ConversionStatus:   movie.ConversionStatus,
			TvCompatibility:    movie.TvCompatibility,
			SizeBytes:          movie.FileSize,
			SizeGB:             formatDownloadSizeGB(movie.FileSize),
		})
		seen[movie.ID] = struct{}{}
	}

	movies, err := a.DB.GetMovieList(ctx)
	if err != nil {
		logutils.Log.WithError(err).WithField("request_id", RequestIDFromContext(ctx)).Warn("ListDownloads: GetMovieList failed")
		writeJSON(w, http.StatusOK, items)
		return
	}
	for i := range movies {
		if _, ok := seen[movies[i].ID]; ok {
			continue
		}
		status := downloadStatusFromMovie(&movies[i])
		items = append(items, DownloadItem{
			ID:                 movies[i].ID,
			Title:              movies[i].Name,
			Status:             status,
			Progress:           movies[i].DownloadedPercentage,
			ConversionProgress: movies[i].ConversionPercentage,
			ConversionStatus:   movies[i].ConversionStatus,
			TvCompatibility:    movies[i].TvCompatibility,
			SizeBytes:          movies[i].FileSize,
			SizeGB:             formatDownloadSizeGB(movies[i].FileSize),
		})
	}

	writeJSON(w, http.StatusOK, items)
}

func formatDownloadSizeGB(size int64) string {
	if size <= 0 {
		return ""
	}
	const bytesPerGiB = 1024 * 1024 * 1024
	return strconv.FormatFloat(float64(size)/bytesPerGiB, 'f', 2, 64)
}

const downloadPercentComplete = 100

func downloadStatusFromMovie(m *database.Movie) string {
	if m.DownloadedPercentage < downloadPercentComplete {
		return "downloading"
	}
	switch m.ConversionStatus {
	case "pending", "in_progress":
		return "converting"
	case "done", "skipped", "":
		return statusCompleted
	case "failed":
		return statusFailed
	default:
		return statusCompleted
	}
}

func uintFromMap(m map[string]any, key string) uint {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		if n < 0 {
			return 0
		}
		return uint(n)
	case float64:
		if n < 0 {
			return 0
		}
		return uint(n)
	case uint:
		return n
	}
	return 0
}

// DeleteDownload handles DELETE /api/v1/downloads/:id.
func DeleteDownload(w http.ResponseWriter, r *http.Request, a *app.App, id uint) {
	if err := deleteDownloadEverywhere(r.Context(), a, id); err != nil {
		logutils.Log.WithError(err).WithFields(map[string]any{
			"movie_id":   id,
			"request_id": RequestIDFromContext(r.Context()),
		}).Error("DeleteDownload: delete failed")
		writeError(w, http.StatusInternalServerError, "failed to delete download")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func deleteDownloadEverywhere(ctx context.Context, a *app.App, id uint) error {
	exists := false
	if a.DB != nil {
		var err error
		exists, err = a.DB.MovieExistsId(ctx, id)
		if err != nil {
			return err
		}
	}
	if exists {
		return filemanager.DeleteMovie(id, a.Config.MoviePath, a.DB, a.DownloadManager)
	}
	return a.DownloadManager.StopDownload(id)
}

// maxAddDownloadBodyBytes limits POST /api/v1/downloads body size to avoid DoS.
const maxAddDownloadBodyBytes = 1024 * 1024 // 1 MiB

var errInvalidTorrentBase64 = errors.New("invalid torrent_base64")

func readAddDownloadJSON(w http.ResponseWriter, r *http.Request) (AddDownloadRequest, bool) {
	body := http.MaxBytesReader(w, r.Body, maxAddDownloadBodyBytes)
	var req AddDownloadRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON")
		}
		return AddDownloadRequest{}, false
	}
	return req, true
}

func validateAddDownloadSource(req AddDownloadRequest, w http.ResponseWriter) (hasURL, hasTorrent, ok bool) {
	hasURL = strings.TrimSpace(req.URL) != ""
	hasTorrent = strings.TrimSpace(req.TorrentBase64) != ""
	switch {
	case !hasURL && !hasTorrent:
		writeError(w, http.StatusBadRequest, "url or torrent_base64 is required")
		return false, false, false
	case hasURL && hasTorrent:
		writeError(w, http.StatusBadRequest, "specify only one of url or torrent_base64")
		return false, false, false
	default:
		return hasURL, hasTorrent, true
	}
}

func newDownloaderForAdd(
	ctx context.Context,
	req AddDownloadRequest,
	hasTorrent bool,
	moviePath string,
	cfg *config.Config,
) (downloader.Downloader, error) {
	if hasTorrent {
		raw, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.ReplaceAll(req.TorrentBase64, "\n", "")))
		if decErr != nil {
			return nil, errInvalidTorrentBase64
		}
		return factory.CreateDownloaderFromTorrentData(raw, moviePath, cfg)
	}
	return factory.CreateDownloaderFromURL(ctx, req.URL, moviePath, cfg)
}

func writeValidateDownloadStartError(w http.ResponseWriter, ctx context.Context, validateErr error) {
	logutils.Log.WithError(validateErr).
		WithField("request_id", RequestIDFromContext(ctx)).
		Debug("AddDownload: ValidateDownloadStart failed")
	switch {
	case errors.Is(validateErr, app.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "media already exists")
	case errors.Is(validateErr, app.ErrNotEnoughSpace):
		writeError(w, http.StatusInsufficientStorage, "not enough disk space")
	default:
		writeError(w, http.StatusBadRequest, utils.DownloadErrorMessage(validateErr))
	}
}

// AddDownload handles POST /api/v1/downloads. Body: {"url":"..."} or {"torrent_base64":"..."} (mutually exclusive), optional "title".
func AddDownload(w http.ResponseWriter, r *http.Request, a *app.App) {
	ctx := r.Context()
	req, ok := readAddDownloadJSON(w, r)
	if !ok {
		return
	}
	_, hasTorrent, ok := validateAddDownloadSource(req, w)
	if !ok {
		return
	}
	dl, err := newDownloaderForAdd(ctx, req, hasTorrent, a.Config.MoviePath, a.Config)
	if err != nil {
		if errors.Is(err, errInvalidTorrentBase64) {
			writeError(w, http.StatusBadRequest, "invalid torrent_base64")
			return
		}
		logutils.Log.WithError(err).WithField("request_id", RequestIDFromContext(ctx)).Debug("AddDownload: CreateDownloaderFromURL failed")
		writeError(w, http.StatusBadRequest, utils.DownloadErrorMessage(err))
		return
	}
	title, err := dl.GetTitle()
	if err != nil {
		logutils.Log.WithError(err).WithField("request_id", RequestIDFromContext(ctx)).Debug("AddDownload: GetTitle failed")
		writeError(w, http.StatusBadRequest, utils.DownloadErrorMessage(err))
		return
	}
	if validateErr := app.ValidateDownloadStart(ctx, a, dl); validateErr != nil {
		writeValidateDownloadStartError(w, ctx, validateErr)
		return
	}
	movieID, _, completionChan, err := a.DownloadManager.StartDownload(dl, app.NewAdminQueueNotifier(a))
	if err != nil {
		logutils.Log.WithError(err).WithField("request_id", RequestIDFromContext(ctx)).Error("AddDownload: StartDownload failed")
		writeError(w, http.StatusInternalServerError, utils.DownloadErrorMessage(err))
		return
	}
	if strings.TrimSpace(req.Title) != "" {
		if updateErr := a.DB.UpdateMovieName(ctx, movieID, strings.TrimSpace(req.Title)); updateErr != nil {
			logutils.Log.WithError(updateErr).
				WithField("movie_id", movieID).
				Warn("AddDownload: UpdateMovieName failed, using downloader title")
		} else {
			title = strings.TrimSpace(req.Title)
		}
	}
	go app.RunCompletionLoop(a, completionChan, dl, movieID, title, webhookNotifier{
		webhookURL:    a.Config.TMSWebhookURL,
		webhookToken:  a.Config.TMSWebhookToken,
		webhookFormat: a.Config.TMSWebhookFormat,
	})
	writeJSON(w, http.StatusCreated, AddDownloadResponse{ID: movieID, Title: title})
}

// webhookNotifier implements notifier.CompletionNotifier for API-originated downloads.
type webhookNotifier struct {
	webhookURL    string
	webhookToken  string
	webhookFormat string
}

func (n webhookNotifier) OnStopped(movieID uint, title string) {
	SendCompletionWebhook(n.webhookURL, n.webhookToken, n.webhookFormat, movieID, title, statusStopped, "")
}

func (n webhookNotifier) OnFailed(movieID uint, title string, err error) {
	SendCompletionWebhook(n.webhookURL, n.webhookToken, n.webhookFormat, movieID, title, statusFailed, utils.DownloadErrorMessage(err))
}

func (n webhookNotifier) OnCompleted(movieID uint, title string) {
	SendCompletionWebhook(n.webhookURL, n.webhookToken, n.webhookFormat, movieID, title, statusCompleted, "")
}

const prowlarrSearchTimeout = 15 * time.Second

// Search handles GET /api/v1/search?q=...&limit=...&quality=...
func Search(w http.ResponseWriter, r *http.Request, a *app.App) {
	if a.Config.ProwlarrURL == "" || a.Config.ProwlarrAPIKey == "" {
		writeError(w, http.StatusServiceUnavailable, "search not configured (Prowlarr)")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), prowlarrSearchTimeout)
	defer cancel()
	client := prowlarr.NewProwlarr(a.Config.ProwlarrURL, a.Config.ProwlarrAPIKey)
	page, err := client.SearchTorrentsWithContext(ctx, q, 0, limit, nil, nil)
	if err != nil {
		logutils.Log.WithError(err).WithField("request_id", RequestIDFromContext(r.Context())).Error("Search: Prowlarr request failed")
		writeError(w, http.StatusServiceUnavailable, "search unavailable")
		return
	}
	quality := strings.TrimSpace(r.URL.Query().Get("quality"))
	items := make([]SearchResultItem, 0, len(page.Results))
	for _, res := range page.Results {
		if quality != "" && !strings.Contains(strings.ToLower(res.Title), strings.ToLower(quality)) {
			continue
		}
		items = append(items, SearchResultItem{
			Title:       res.Title,
			Size:        res.Size,
			Magnet:      res.Magnet,
			TorrentURL:  res.TorrentURL,
			IndexerName: res.IndexerName,
			Peers:       res.Peers,
		})
	}
	writeJSON(w, http.StatusOK, items)
}
