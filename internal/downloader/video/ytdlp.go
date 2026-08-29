package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tmsconfig "github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/tvcompat"
	tmsutils "github.com/NikitaDmitryuk/telegram-media-server/internal/utils"
)

const (
	ytdlpTimeout        = 30 * time.Second
	metadataAttemptTime = 9 * time.Second
	metadataAttempts    = 3
	DefaultQuality      = "best[height<=1080]"
	secondsPerMinute    = 60
	gracefulStopTimeout = 5 * time.Second
	forceKillTimeout    = 2 * time.Second
	// minProbeSize is the minimum file size (bytes) for ffprobe to read headers; used for early TV compatibility probe.
	minProbeSize       = 256 * 1024
	probePollInterval  = 2 * time.Second
	defaultYtdlpBinary = "/usr/bin/yt-dlp"
)

var (
	metadataRetryDelay = func(attempt int) time.Duration {
		return time.Duration(attempt) * time.Second
	}
	diagnosticURLPattern = regexp.MustCompile(`https?://\S+`)
)

func ytdlpBinary(cfg *tmsconfig.Config) string {
	if cfg != nil && cfg.YtdlpPath != "" {
		return cfg.YtdlpPath
	}
	return defaultYtdlpBinary
}

type YTDLPDownloader struct {
	url              string
	title            string
	fileSize         int64
	vcodec           string
	outputFileName   string
	cmd              *exec.Cmd
	cancel           context.CancelFunc
	stoppedManually  bool
	config           *tmsconfig.Config
	releaseExecution func()
}

func NewYTDLPDownloader(videoURL string, config *tmsconfig.Config) downloader.Downloader {
	dl, err := NewYTDLPDownloaderContext(context.Background(), videoURL, config)
	if err != nil {
		logutils.Log.WithError(err).Error("Failed to retrieve video title, generating fallback title")
		return newFallbackDownloader(videoURL, config)
	}
	return dl
}

// NewYTDLPDownloaderContext probes metadata once and returns permanent provider
// errors (notably authentication requirements) before a database row is created.
func NewYTDLPDownloaderContext(ctx context.Context, videoURL string, config *tmsconfig.Config) (downloader.Downloader, error) {
	metadata, err := probeVideoMetadata(ctx, videoURL, config)
	if err != nil {
		if errors.Is(err, downloader.ErrVideoAuthenticationRequired) || isPermanentMetadataError(err.Error()) {
			return nil, err
		}
		logutils.Log.WithError(err).Warn("Video metadata probe exhausted retries, using fallback metadata")
		return newFallbackDownloader(videoURL, config), nil
	}
	title := strings.TrimSpace(metadata.Title)
	if title == "" {
		title = fallbackVideoTitle(videoURL)
	}
	return &YTDLPDownloader{
		url:            videoURL,
		title:          title,
		fileSize:       metadataFileSize(&metadata),
		vcodec:         metadataVcodec(&metadata),
		outputFileName: tmsutils.GenerateFileName(title),
		config:         config,
	}, nil
}

func newFallbackDownloader(videoURL string, config *tmsconfig.Config) downloader.Downloader {
	title := fallbackVideoTitle(videoURL)
	return &YTDLPDownloader{
		url:            videoURL,
		title:          title,
		outputFileName: tmsutils.GenerateFileName(title),
		config:         config,
	}
}

func fallbackVideoTitle(videoURL string) string {
	title, _ := extractVideoID(videoURL)
	if title == "" {
		return "unknown_video"
	}
	return title
}

func (*YTDLPDownloader) TotalEpisodes() int { return 0 }

// GetEarlyTvCompatibility uses the metadata cached during downloader creation.
func (d *YTDLPDownloader) GetEarlyTvCompatibility(ctx context.Context) (string, error) {
	_ = ctx
	return tvcompat.CompatFromVcodec(d.vcodec), nil
}

func (d *YTDLPDownloader) StartDownload(
	ctx context.Context,
) (progressChan chan float64, errChan chan error, episodesChan <-chan int, err error) {
	useProxy, err := shouldUseProxy(d.url, d.config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error checking proxy requirement: %w", err)
	}

	outputPath := filepath.Join(d.config.MoviePath, d.outputFileName)
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel

	cmdArgs := d.buildYTDLPArgs(outputPath)

	if useProxy {
		proxy := d.config.Proxy
		logutils.Log.WithField("proxy", proxy).Infof("Using proxy for URL: %s", d.url)
		cmdArgs = append([]string{"--proxy", proxy}, cmdArgs...)
	} else {
		logutils.Log.Infof("No proxy used for URL: %s", d.url)
	}

	cmd := exec.CommandContext(
		ctx,
		ytdlpBinary(d.config),
		cmdArgs...) // #nosec G204 -- binary from config, cmdArgs built from URL and options
	d.cmd = cmd
	releaseExecution := acquireYTDLPExecution()
	d.releaseExecution = releaseExecution

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		d.releaseYTDLPExecution()
		return nil, nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		d.releaseYTDLPExecution()
		return nil, nil, nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		d.releaseYTDLPExecution()
		return nil, nil, nil, fmt.Errorf("failed to start yt-dlp: %w", err)
	}

	progressChan = make(chan float64)
	errChan = make(chan error, 1)
	epCh := make(chan int, 1)

	go d.monitorDownload(ctx, cancel, stdout, stderr, progressChan, errChan)
	go waitForProbeableFile(ctx, outputPath, epCh)

	return progressChan, errChan, epCh, nil
}

// waitForProbeableFile sends 1 on epCh when the output file exists and is large enough for ffprobe,
// so the manager can run TV compatibility probe and show the circle early (before full download).
func waitForProbeableFile(ctx context.Context, outputPath string, epCh chan int) {
	defer close(epCh)
	ticker := time.NewTicker(probePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(outputPath)
			if err != nil {
				continue
			}
			if info.Size() >= minProbeSize {
				select {
				case epCh <- 1:
				default:
				}
				return
			}
		}
	}
}

func (d *YTDLPDownloader) monitorDownload(
	ctx context.Context,
	cancel context.CancelFunc,
	stdout, stderr io.ReadCloser,
	progressChan chan float64,
	errChan chan error,
) {
	defer cancel()
	defer close(progressChan)
	defer d.releaseYTDLPExecution()
	errorOutput := make(chan string, 1)

	go func() {
		defer close(errorOutput)
		scanner := bufio.NewScanner(stderr)
		var output strings.Builder
		for scanner.Scan() {
			output.WriteString(scanner.Text() + "\n")
		}
		errorOutput <- output.String()
	}()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "[download]") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				percentStr := strings.TrimSuffix(fields[1], "%")
				if percent, err := strconv.ParseFloat(percentStr, 64); err == nil {
					progressChan <- percent
				}
			}
		}
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- d.cmd.Wait()
	}()

	var processErr error
	select {
	case processErr = <-waitDone:
	case <-ctx.Done():
		logutils.Log.Info("yt-dlp process canceled due to context cancellation")
		if d.cmd.Process != nil {
			if killErr := d.cmd.Process.Kill(); killErr != nil {
				logutils.Log.WithError(killErr).Warn("Failed to kill yt-dlp process")
			}
		}
		processErr = ctx.Err()
	}

	stderrOutput := <-errorOutput

	errChan <- d.downloadProcessError(processErr, stderrOutput)
	close(errChan)
}

func (d *YTDLPDownloader) downloadProcessError(processErr error, stderrOutput string) error {
	if processErr == nil {
		return nil
	}
	if d.stoppedManually || errors.Is(processErr, context.Canceled) || errors.Is(processErr, context.DeadlineExceeded) {
		if errors.Is(processErr, context.DeadlineExceeded) {
			logutils.Log.Info("yt-dlp process timed out")
		} else {
			logutils.Log.Info("yt-dlp process stopped manually")
		}
		return nil
	}
	diagnostic := sanitizeYTDLPDiagnostic(stderrOutput, d.url, d.config)
	logutils.Log.WithError(processErr).Errorf("yt-dlp exited with error: %s", diagnostic)
	if isAuthenticationError(stderrOutput) {
		return fmt.Errorf("%w", downloader.ErrVideoAuthenticationRequired)
	}
	return fmt.Errorf("yt-dlp failed: %s: %w", diagnostic, processErr)
}

func (d *YTDLPDownloader) releaseYTDLPExecution() {
	if d.releaseExecution != nil {
		d.releaseExecution()
		d.releaseExecution = nil
	}
}

func (d *YTDLPDownloader) StopDownload() error {
	d.stoppedManually = true

	if d.cancel != nil {
		logutils.Log.Info("Canceling yt-dlp download context")
		d.cancel()
	}

	if d.cmd != nil && d.cmd.Process != nil {
		logutils.Log.Info("Stopping yt-dlp process gracefully")

		// First try graceful termination (SIGTERM on Unix systems)
		if err := d.cmd.Process.Signal(os.Interrupt); err != nil {
			// Process already exited — nothing to stop
			logutils.Log.WithError(err).Debug("Could not send interrupt signal to yt-dlp (process likely already exited)")
			return nil
		}

		// Wait for process to exit with timeout
		done := make(chan error, 1)
		go func() {
			done <- d.cmd.Wait()
		}()

		select {
		case <-done:
			logutils.Log.Info("yt-dlp process exited gracefully")
		case <-time.After(gracefulStopTimeout):
			// If graceful termination didn't work, force kill
			logutils.Log.Warn("yt-dlp did not exit gracefully, force killing")
			if killErr := d.cmd.Process.Kill(); killErr != nil {
				logutils.Log.WithError(killErr).Warn("Failed to force kill yt-dlp process")
			}

			// Wait a bit more for force kill to take effect
			select {
			case <-done:
				logutils.Log.Info("yt-dlp process exited after force kill")
			case <-time.After(forceKillTimeout):
				logutils.Log.Warn("yt-dlp process did not exit even after force kill, considering it stopped")
			}
		}
	}

	// Additional cleanup: try to remove any remaining temp files
	if err := d.cleanupTempFiles(); err != nil {
		logutils.Log.WithError(err).Warn("Failed to cleanup temporary files after stop")
	}

	logutils.Log.Info("yt-dlp process stopped manually")
	return nil
}

func (d *YTDLPDownloader) GetTitle() (string, error) {
	return d.title, nil
}

func (d *YTDLPDownloader) GetFiles() (mainFiles, tempFiles []string, err error) {
	baseName := strings.TrimSuffix(d.outputFileName, ".mp4")
	mainFiles = []string{
		d.outputFileName,
		// Subtitle files that yt-dlp can download
		baseName + ".*.vtt", // WebVTT subtitles (e.g., video.ru.vtt, video.en.vtt)
		baseName + ".*.srt", // SubRip subtitles
		baseName + ".*.ass", // Advanced SubStation Alpha
		baseName + ".*.ssa", // SubStation Alpha
		baseName + ".vtt",   // Subtitles without language code
		baseName + ".srt",
		baseName + ".ass",
		baseName + ".ssa",
	}

	// Comprehensive list of temporary files that yt-dlp can create
	tempFiles = []string{
		// Basic temp files
		baseName + ".part*",
		baseName + ".ytdl",
		baseName + ".ytdlp",
		// Video-specific temp files
		d.outputFileName + ".part*", // e.g., video.mp4.part
		d.outputFileName + ".ytdl",  // e.g., video.mp4.ytdl
		d.outputFileName + ".ytdlp", // e.g., video.mp4.ytdlp
		// Format-specific temp files (yt-dlp uses f-codes for different formats)
		baseName + ".f*.mp4",
		baseName + ".f*.mp4.part*",
		baseName + ".f*.mp4.ytdlp",
		baseName + ".f*.mp4.ytdl",
		baseName + ".f*.webm",
		baseName + ".f*.webm.part*",
		baseName + ".f*.webm.ytdlp",
		baseName + ".f*.webm.ytdl",
		baseName + ".f*.m4a",
		baseName + ".f*.m4a.part*",
		baseName + ".f*.m4a.ytdlp",
		baseName + ".f*.m4a.ytdl",
		baseName + ".f*.m4v",
		baseName + ".f*.m4v.part*",
		baseName + ".f*.m4v.ytdlp",
		baseName + ".f*.m4v.ytdl",
		// Additional common patterns
		baseName + ".temp",
		baseName + ".tmp",
		d.outputFileName + ".temp",
		d.outputFileName + ".tmp",
	}
	return mainFiles, tempFiles, nil
}

func (d *YTDLPDownloader) cleanupTempFiles() error {
	if d.config == nil || d.config.MoviePath == "" {
		return nil
	}

	_, tempFiles, err := d.GetFiles()
	if err != nil {
		return err
	}

	var errorList []string
	for _, tempFile := range tempFiles {
		fullPath := filepath.Join(d.config.MoviePath, tempFile)

		// Use glob to handle patterns with *
		if strings.Contains(tempFile, "*") {
			matches, globErr := filepath.Glob(fullPath)
			if globErr != nil {
				continue
			}
			for _, match := range matches {
				if removeErr := os.Remove(match); removeErr != nil && !os.IsNotExist(removeErr) {
					logutils.Log.WithError(removeErr).Debugf("Failed to cleanup temp file: %s", match)
					errorList = append(errorList, removeErr.Error())
				} else if removeErr == nil {
					logutils.Log.Infof("Cleaned up temp file: %s", match)
				}
			}
		} else {
			if removeErr := os.Remove(fullPath); removeErr != nil && !os.IsNotExist(removeErr) {
				logutils.Log.WithError(removeErr).Debugf("Failed to cleanup temp file: %s", fullPath)
				errorList = append(errorList, removeErr.Error())
			} else if removeErr == nil {
				logutils.Log.Infof("Cleaned up temp file: %s", fullPath)
			}
		}
	}

	if len(errorList) > 0 {
		return fmt.Errorf("failed to cleanup some temp files: %s", strings.Join(errorList, "; "))
	}

	return nil
}

func (d *YTDLPDownloader) GetFileSize() (int64, error) {
	return d.fileSize, nil
}

func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func (d *YTDLPDownloader) StoppedManually() bool {
	return d.stoppedManually
}

func (d *YTDLPDownloader) buildYTDLPArgs(outputPath string) []string {
	videoSettings := d.config.GetVideoSettings()

	qualitySelector := prepareQualitySelector(&videoSettings)

	args := append(commonYTDLPArgs(d.config),
		"--newline",
		"-f", qualitySelector,
		"-o", outputPath,
	)

	args = appendFormatSortArgs(args, &videoSettings)
	args = appendReencodingArgs(args, &videoSettings)
	args = appendSubtitleArgs(args, &videoSettings)
	args = append(args, d.url)

	return args
}

func commonYTDLPArgs(cfg *tmsconfig.Config) []string {
	var args []string
	if cfg != nil && cfg.YtdlpExtraArgs != "" {
		args = append(args, strings.Fields(cfg.YtdlpExtraArgs)...)
	}
	args = append(args, "--remote-components", "ejs:github")
	if cfg != nil && cfg.YtdlpCookiesPath != "" {
		args = append(args, "--cookies", cfg.YtdlpCookiesPath)
	}
	return args
}

// prepareQualitySelector processes the quality selector with audio language filter and fallback.
func prepareQualitySelector(videoSettings *tmsconfig.VideoConfig) string {
	qualitySelector := videoSettings.QualitySelector

	// Check for potential conflict between VIDEO_QUALITY_SELECTOR and VIDEO_MAX_HEIGHT
	checkQualitySettingsConflict(qualitySelector, videoSettings.MaxHeight)

	if videoSettings.AudioLang != "" {
		qualitySelector = addAudioLanguageFilter(qualitySelector, videoSettings.AudioLang)
	}

	// Add fallback to "best" if the requested format is not available
	// This helps with sites like VK that may not support complex format selectors
	if !strings.HasSuffix(qualitySelector, "/best") && !strings.HasSuffix(qualitySelector, "/b") {
		qualitySelector += "/best"
	}

	return qualitySelector
}

// checkQualitySettingsConflict logs a warning if both height filter and MaxHeight are set.
func checkQualitySettingsConflict(qualitySelector string, maxHeight int) {
	hasHeightFilter := strings.Contains(qualitySelector, "[height<=") || strings.Contains(qualitySelector, "[height<")
	if hasHeightFilter && maxHeight > 0 {
		logutils.Log.Warn("Both VIDEO_QUALITY_SELECTOR with height filter and VIDEO_MAX_HEIGHT are set. " +
			"This may cause unexpected behavior. Consider using only one of these settings: " +
			"either VIDEO_QUALITY_SELECTOR with [height<=X] filter OR VIDEO_MAX_HEIGHT (recommended)")
	}
}

// appendFormatSortArgs adds format sorting options (-S) for resolution and codec preferences.
func appendFormatSortArgs(args []string, videoSettings *tmsconfig.VideoConfig) []string {
	var formatSortParts []string

	// Add max height restriction (e.g., 1080 for 1080p, 720 for 720p)
	if videoSettings.MaxHeight > 0 {
		formatSortParts = append(formatSortParts, fmt.Sprintf("res:%d", videoSettings.MaxHeight))
	}

	if videoSettings.CompatibilityMode {
		formatSortParts = append(formatSortParts, "vcodec:h264", "acodec:mp3")
		videoSettings.EnableReencoding = true
		videoSettings.VideoCodec = "h264"
		videoSettings.AudioCodec = "mp3"
		videoSettings.OutputFormat = "mp4"
	}

	if len(formatSortParts) > 0 {
		args = append(args, "-S", strings.Join(formatSortParts, ","))
	}

	return args
}

// appendReencodingArgs adds video reencoding options if enabled.
func appendReencodingArgs(args []string, videoSettings *tmsconfig.VideoConfig) []string {
	if !videoSettings.EnableReencoding {
		return args
	}

	args = append(args, "--recode-video", videoSettings.OutputFormat)

	if videoSettings.ForceReencoding {
		postprocessorArgs := fmt.Sprintf("ffmpeg:-c:v %s -c:a %s",
			videoSettings.VideoCodec, videoSettings.AudioCodec)

		if videoSettings.FFmpegExtraArgs != "" {
			postprocessorArgs += " " + videoSettings.FFmpegExtraArgs
		}

		args = append(args, "--postprocessor-args", postprocessorArgs)
	}

	return args
}

// appendSubtitleArgs adds subtitle download options if configured.
func appendSubtitleArgs(args []string, videoSettings *tmsconfig.VideoConfig) []string {
	if videoSettings.WriteSubs {
		args = append(args, "--write-subs")
		if videoSettings.SubtitleLang != "" {
			args = append(args, "--sub-langs", videoSettings.SubtitleLang)
		}
	} else if videoSettings.SubtitleLang != "" {
		args = append(args, "--write-subs", "--sub-langs", videoSettings.SubtitleLang)
	}

	return args
}

func shouldUseProxy(rawURL string, cfg *tmsconfig.Config) (bool, error) {
	if cfg == nil || cfg.Proxy == "" {
		return false, nil
	}

	if cfg.ProxyDomains == "" {
		return true, nil
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return false, fmt.Errorf("failed to parse URL: %w", err)
	}

	hostname := parsedURL.Hostname()
	domains := strings.Split(cfg.ProxyDomains, ",")
	for _, domain := range domains {
		if strings.Contains(hostname, strings.TrimSpace(domain)) {
			return true, nil
		}
	}

	return false, nil
}

type videoMetadata struct {
	Title          string  `json:"title"`
	Filesize       float64 `json:"filesize"`
	FilesizeApprox float64 `json:"filesize_approx"`
	Duration       float64 `json:"duration"`
	Vcodec         string  `json:"vcodec"`
	Formats        []struct {
		Vcodec string `json:"vcodec"`
	} `json:"formats"`
}

func probeVideoMetadata(parent context.Context, videoURL string, cfg *tmsconfig.Config) (videoMetadata, error) {
	ctx, cancel := context.WithTimeout(parent, ytdlpTimeout)
	defer cancel()

	var lastErr error
	for attempt := 1; attempt <= metadataAttempts; attempt++ {
		metadata, diagnostic, err := probeVideoMetadataOnce(ctx, videoURL, cfg)
		if err == nil {
			return metadata, nil
		}
		if isAuthenticationError(diagnostic) {
			return videoMetadata{}, fmt.Errorf("%w", downloader.ErrVideoAuthenticationRequired)
		}
		lastErr = fmt.Errorf("metadata probe attempt %d failed: %s: %w", attempt, diagnostic, err)
		if isPermanentMetadataError(diagnostic) || attempt == metadataAttempts {
			break
		}
		delay := metadataRetryDelay(attempt)
		select {
		case <-ctx.Done():
			return videoMetadata{}, fmt.Errorf("metadata probe timed out: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
	if ctx.Err() != nil {
		return videoMetadata{}, fmt.Errorf("metadata probe timed out: %w", ctx.Err())
	}
	return videoMetadata{}, lastErr
}

func probeVideoMetadataOnce(ctx context.Context, videoURL string, cfg *tmsconfig.Config) (videoMetadata, string, error) {
	useProxy, err := shouldUseProxy(videoURL, cfg)
	if err != nil {
		return videoMetadata{}, "invalid URL or proxy configuration", err
	}

	args := commonYTDLPArgs(cfg)
	if useProxy && cfg != nil && cfg.Proxy != "" {
		args = append(args, "--proxy", cfg.Proxy)
	}
	args = append(args, "--dump-single-json", "--skip-download", "--no-playlist", "--no-warnings", videoURL)

	attemptCtx, cancel := context.WithTimeout(ctx, metadataAttemptTime)
	defer cancel()
	cmd := exec.CommandContext(attemptCtx, ytdlpBinary(cfg), args...) // #nosec G204 -- configured binary and trusted options
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	releaseExecution := acquireYTDLPExecution()
	output, runErr := cmd.Output()
	releaseExecution()
	diagnostic := sanitizeYTDLPDiagnostic(stderr.String(), videoURL, cfg)
	if runErr != nil {
		if attemptCtx.Err() != nil {
			return videoMetadata{}, "metadata request timed out", attemptCtx.Err()
		}
		return videoMetadata{}, diagnostic, runErr
	}

	var metadata videoMetadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		return videoMetadata{}, "invalid metadata JSON", err
	}
	return metadata, "", nil
}

func metadataFileSize(metadata *videoMetadata) int64 {
	if metadata.Filesize > 0 {
		return int64(metadata.Filesize)
	}
	if metadata.FilesizeApprox > 0 {
		return int64(metadata.FilesizeApprox)
	}
	if metadata.Duration > 0 {
		return int64(metadata.Duration * 1024 * 1024 / secondsPerMinute)
	}
	return 0
}

func metadataVcodec(metadata *videoMetadata) string {
	if metadata.Vcodec != "" && metadata.Vcodec != "none" {
		return metadata.Vcodec
	}
	for _, format := range metadata.Formats {
		if format.Vcodec != "" && format.Vcodec != "none" {
			return format.Vcodec
		}
	}
	return ""
}

func isAuthenticationError(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "sign in to confirm") ||
		strings.Contains(lower, "login_required") ||
		strings.Contains(lower, "authentication is required")
}

func isPermanentMetadataError(output string) bool {
	lower := strings.ToLower(output)
	permanentMarkers := []string{
		"unsupported url",
		"video unavailable",
		"private video",
		"has been removed",
		"this video is unavailable",
		"failed to load cookies",
		"could not open cookies",
	}
	for _, marker := range permanentMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func sanitizeYTDLPDiagnostic(output, videoURL string, cfg *tmsconfig.Config) string {
	sanitized := strings.TrimSpace(output)
	if videoURL != "" {
		sanitized = strings.ReplaceAll(sanitized, videoURL, "<video-url>")
	}
	if cfg != nil && cfg.Proxy != "" {
		sanitized = strings.ReplaceAll(sanitized, cfg.Proxy, "<proxy>")
	}
	if cfg != nil && cfg.YtdlpCookiesPath != "" {
		sanitized = strings.ReplaceAll(sanitized, cfg.YtdlpCookiesPath, "<cookies-file>")
	}
	sanitized = diagnosticURLPattern.ReplaceAllString(sanitized, "<url>")
	lines := strings.Split(sanitized, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "ERROR:") {
			sanitized = line
			break
		}
	}
	const maxDiagnosticLength = 1024
	if len(sanitized) > maxDiagnosticLength {
		sanitized = sanitized[:maxDiagnosticLength] + "..."
	}
	if sanitized == "" {
		return "yt-dlp command failed without diagnostic output"
	}
	return sanitized
}

func extractVideoID(rawURL string) (string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %w", err)
	}

	hostname := parsedURL.Hostname()
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	cleanHostname := re.ReplaceAllString(hostname, "_")

	query := parsedURL.Query()
	if v := query.Get("v"); v != "" {
		return fmt.Sprintf("%s_%s", cleanHostname, v), nil
	}

	path := strings.Trim(parsedURL.Path, "/")
	if path != "" {
		cleanPath := re.ReplaceAllString(path, "_")
		return fmt.Sprintf("%s_%s", cleanHostname, cleanPath), nil
	}

	return cleanHostname, nil
}

func addAudioLanguageFilter(selector, lang string) string {
	if lang == "" {
		return selector
	}

	languageFilter := fmt.Sprintf("[language=%s]", lang)

	if strings.Contains(selector, "/") {
		parts := strings.Split(selector, "/")
		for i, part := range parts {
			parts[i] = addLanguageFilterToSingleFormat(part, languageFilter)
		}
		return strings.Join(parts, "/")
	}

	return addLanguageFilterToSingleFormat(selector, languageFilter)
}

func addLanguageFilterToSingleFormat(format, languageFilter string) string {
	if strings.Contains(format, "+") {
		parts := strings.Split(format, "+")
		for i, part := range parts {
			if strings.Contains(part, "a") || strings.Contains(part, "audio") || i > 0 {
				parts[i] = addFilterToFormat(part, languageFilter)
			}
		}
		return strings.Join(parts, "+")
	}

	return addFilterToFormat(format, languageFilter)
}

func addFilterToFormat(format, filter string) string {
	return format + filter
}
