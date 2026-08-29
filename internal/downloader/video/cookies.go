package ytdlp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tmsconfig "github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
)

const (
	cookieCheckTimeout = 30 * time.Second
	cookiePrivateMode  = 0o600
	cookieReadOnlyMode = 0o400
	netscapeFieldCount = 7
)

type cookieLifecycleState struct {
	Provisioned bool `json:"provisioned"`
	Invalid     bool `json:"invalid"`
	Notified    bool `json:"notified"`
}

type CookieNotifier func(context.Context) bool

var cookieStateMu sync.Mutex

func cookieStatePath(cfg *tmsconfig.Config) string {
	if cfg == nil || cfg.YtdlpCookiesPath == "" {
		return ""
	}
	if cfg.YtdlpCookiesStatePath != "" {
		return cfg.YtdlpCookiesStatePath
	}
	return cfg.YtdlpCookiesPath + ".state.json"
}

func readCookieState(cfg *tmsconfig.Config) cookieLifecycleState {
	path := cookieStatePath(cfg)
	if path == "" {
		return cookieLifecycleState{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cookieLifecycleState{}
	}
	var state cookieLifecycleState
	if json.Unmarshal(data, &state) != nil {
		return cookieLifecycleState{}
	}
	return state
}

func writeCookieState(cfg *tmsconfig.Config, state cookieLifecycleState) error {
	path := cookieStatePath(cfg)
	if path == "" {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal cookie state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".youtube-cookie-state-*")
	if err != nil {
		return fmt.Errorf("create cookie state: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(cookiePrivateMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect cookie state: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write cookie state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close cookie state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install cookie state: %w", err)
	}
	return nil
}

func cookiesUsable(cfg *tmsconfig.Config) bool {
	if cfg == nil || cfg.YtdlpCookiesPath == "" {
		return false
	}
	cookieStateMu.Lock()
	defer cookieStateMu.Unlock()
	state := readCookieState(cfg)
	if state.Invalid {
		return false
	}
	info, err := os.Stat(cfg.YtdlpCookiesPath)
	return err == nil && info.Mode().IsRegular()
}

func markCookiesInvalid(cfg *tmsconfig.Config) {
	if cfg == nil || cfg.YtdlpCookiesPath == "" {
		return
	}
	cookieStateMu.Lock()
	defer cookieStateMu.Unlock()
	state := readCookieState(cfg)
	state.Provisioned = true
	state.Invalid = true
	if err := writeCookieState(cfg, state); err != nil {
		logutils.Log.WithError(err).Warn("Failed to persist invalid yt-dlp cookie state")
	}
}

func markCookiesValid(cfg *tmsconfig.Config) error {
	cookieStateMu.Lock()
	defer cookieStateMu.Unlock()
	return writeCookieState(cfg, cookieLifecycleState{Provisioned: true})
}

func notifyInvalidCookiesOnce(ctx context.Context, cfg *tmsconfig.Config, notify CookieNotifier) {
	if notify == nil {
		return
	}
	cookieStateMu.Lock()
	state := readCookieState(cfg)
	if !state.Invalid || state.Notified {
		cookieStateMu.Unlock()
		return
	}
	cookieStateMu.Unlock()

	if !notify(ctx) {
		return
	}

	cookieStateMu.Lock()
	defer cookieStateMu.Unlock()
	state = readCookieState(cfg)
	if state.Invalid {
		state.Notified = true
		if err := writeCookieState(cfg, state); err != nil {
			logutils.Log.WithError(err).Warn("Failed to persist yt-dlp cookie notification state")
		}
	}
}

func StartCookieMonitor(ctx context.Context, cfg *tmsconfig.Config, notify CookieNotifier) {
	if cfg == nil || cfg.YtdlpCookiesPath == "" || cfg.YtdlpCookiesCheckInterval <= 0 {
		return
	}
	run := func() {
		checkAndRefreshCookies(ctx, cfg)
		notifyInvalidCookiesOnce(ctx, cfg, notify)
	}
	run()
	ticker := time.NewTicker(cfg.YtdlpCookiesCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func checkAndRefreshCookies(parent context.Context, cfg *tmsconfig.Config) {
	if cfg == nil || cfg.YtdlpCookiesPath == "" {
		return
	}
	state := readCookieState(cfg)
	if _, err := os.Stat(cfg.YtdlpCookiesPath); err != nil {
		if errors.Is(err, os.ErrNotExist) && !state.Provisioned {
			return
		}
		markCookiesInvalid(cfg)
		return
	}

	release, acquired := tryAcquireYTDLPUpdate()
	if !acquired {
		logutils.Log.Info("yt-dlp cookie check skipped because yt-dlp is currently in use")
		return
	}
	defer release()

	tmp, err := copyCookieJar(cfg.YtdlpCookiesPath)
	if err != nil {
		logutils.Log.WithError(err).Warn("Failed to prepare yt-dlp cookie check")
		return
	}
	defer os.Remove(tmp)

	ctx, cancel := context.WithTimeout(parent, cookieCheckTimeout)
	defer cancel()
	args := []string{
		"--ignore-config", "--cookies", tmp, "--flat-playlist", "--playlist-items", "1",
		"--simulate", "--quiet", "--no-warnings", ":ythistory",
	}
	cmd := exec.CommandContext(ctx, ytdlpBinary(cfg), args...) // #nosec G204 -- configured binary and fixed arguments
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		diagnostic := sanitizeYTDLPDiagnostic(stderr.String(), "", cfg)
		if isAuthenticationError(diagnostic) || isCookieRejectionError(diagnostic) {
			markCookiesInvalid(cfg)
			return
		}
		logutils.Log.WithError(err).Warn("Transient yt-dlp cookie validation failure")
		return
	}
	if err := filterYouTubeCookieJar(tmp); err != nil {
		logutils.Log.WithError(err).Warn("Failed to filter refreshed yt-dlp cookies")
		return
	}
	if err := os.Chmod(tmp, cookieReadOnlyMode); err != nil {
		logutils.Log.WithError(err).Warn("Failed to protect refreshed yt-dlp cookies")
		return
	}
	if err := os.Rename(tmp, cfg.YtdlpCookiesPath); err != nil {
		// Controller-managed cookie files may intentionally live in a root-owned
		// directory. Validation still succeeds, while refresh remains read-only.
		logutils.Log.Info("yt-dlp cookies validated; cookie file is not writable for automatic refresh")
	}
	if err := markCookiesValid(cfg); err != nil {
		logutils.Log.WithError(err).Warn("Failed to persist valid yt-dlp cookie state")
	}
}

func copyCookieJar(source string) (string, error) {
	input, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("read cookie jar: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(source), ".youtube-cookies-*")
	if err != nil {
		tmp, err = os.CreateTemp("", ".youtube-cookies-*")
	}
	if err != nil {
		return "", fmt.Errorf("create temporary cookie jar: %w", err)
	}
	path := tmp.Name()
	if err := tmp.Chmod(cookiePrivateMode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("protect temporary cookie jar: %w", err)
	}
	if _, err := tmp.Write(input); err != nil {
		_ = tmp.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("copy cookie jar: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temporary cookie jar: %w", err)
	}
	return path, nil
}

func filterYouTubeCookieJar(path string) error {
	input, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open cookie jar: %w", err)
	}
	defer input.Close()

	var output strings.Builder
	scanner := bufio.NewScanner(input)
	lineNumber := 0
	keptCookies := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if lineNumber == 1 {
			if line != "# HTTP Cookie File" && line != "# Netscape HTTP Cookie File" {
				return errors.New("cookie jar is not in Netscape format")
			}
			output.WriteString(line + "\n")
			continue
		}
		domainLine := strings.TrimPrefix(line, "#HttpOnly_")
		fields := strings.Split(domainLine, "\t")
		if len(fields) < netscapeFieldCount {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(fields[0]), ".")
		if domain != "youtube.com" && !strings.HasSuffix(domain, ".youtube.com") {
			continue
		}
		output.WriteString(line + "\n")
		keptCookies++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan cookie jar: %w", err)
	}
	if keptCookies == 0 {
		return errors.New("cookie jar contains no youtube.com cookies")
	}
	return os.WriteFile(path, []byte(output.String()), cookiePrivateMode)
}

func isCookieRejectionError(output string) bool {
	lower := strings.ToLower(output)
	markers := []string{
		"account cookies are no longer valid",
		"cookies are no longer valid",
		"failed to load cookies",
		"could not open cookies",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
