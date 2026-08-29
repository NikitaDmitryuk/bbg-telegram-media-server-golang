package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/testutils"
)

func writeFakeYTDLP(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == windowsGOOS {
		t.Skip("fake yt-dlp scripts require a Unix shell")
	}
	path := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o600); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod fake yt-dlp: %v", err)
	}
	return path
}

func withoutMetadataRetryDelay(t *testing.T) {
	t.Helper()
	original := metadataRetryDelay
	metadataRetryDelay = func(int) time.Duration { return 0 }
	t.Cleanup(func() { metadataRetryDelay = original })
}

func TestMetadataProbeRetriesAndCachesResult(t *testing.T) {
	withoutMetadataRetryDelay(t)
	counterPath := filepath.Join(t.TempDir(), "count")
	script := fmt.Sprintf(`
count=0
if [ -f %q ]; then count=$(sed -n '1p' %q); fi
count=$((count + 1))
printf '%%s\n' "$count" > %q
if [ "$count" -lt 3 ]; then
  printf 'ERROR: temporary network failure\n' >&2
  exit 1
fi
printf '{"title":"Cached title","filesize":4096,"vcodec":"avc1.64001f"}\n'
`, counterPath, counterPath, counterPath)
	cfg := testutils.TestConfig(t.TempDir())
	cfg.YtdlpPath = writeFakeYTDLP(t, script)

	dl, err := NewYTDLPDownloaderContext(context.Background(), "https://example.com/video", cfg)
	if err != nil {
		t.Fatalf("create downloader: %v", err)
	}
	title, _ := dl.GetTitle()
	if title != "Cached title" {
		t.Fatalf("title = %q", title)
	}
	size, _ := dl.GetFileSize()
	if size != 4096 {
		t.Fatalf("size = %d", size)
	}
	compat, err := dl.(downloader.EarlyCompatDownloader).GetEarlyTvCompatibility(context.Background())
	if err != nil || compat != "green" {
		t.Fatalf("compat = %q, err=%v", compat, err)
	}
	data, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if strings.TrimSpace(string(data)) != "3" {
		t.Fatalf("metadata command count = %q", data)
	}
}

func TestMetadataProbeAuthenticationDoesNotRetry(t *testing.T) {
	withoutMetadataRetryDelay(t)
	counterPath := filepath.Join(t.TempDir(), "count")
	script := fmt.Sprintf("printf '1\\n' > %q\nprintf 'ERROR: Sign in to confirm your age. Use --cookies for authentication.\\n' >&2\nexit 1\n", counterPath)
	cfg := testutils.TestConfig(t.TempDir())
	cfg.YtdlpPath = writeFakeYTDLP(t, script)

	_, err := NewYTDLPDownloaderContext(context.Background(), "https://example.com/restricted", cfg)
	if !errors.Is(err, downloader.ErrVideoAuthenticationRequired) {
		t.Fatalf("expected authentication error, got %v", err)
	}
	data, readErr := os.ReadFile(counterPath)
	if readErr != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("authentication error should not retry: count=%q err=%v", data, readErr)
	}
}

func TestMetadataProbeExhaustionUsesFallback(t *testing.T) {
	withoutMetadataRetryDelay(t)
	counterPath := filepath.Join(t.TempDir(), "count")
	script := fmt.Sprintf(`
count=0
if [ -f %q ]; then count=$(sed -n '1p' %q); fi
count=$((count + 1))
printf '%%s\n' "$count" > %q
printf 'ERROR: temporary network failure\n' >&2
exit 1
`, counterPath, counterPath, counterPath)
	cfg := testutils.TestConfig(t.TempDir())
	cfg.YtdlpPath = writeFakeYTDLP(t, script)

	dl, err := NewYTDLPDownloaderContext(context.Background(), "https://youtu.be/abc123", cfg)
	if err != nil {
		t.Fatalf("transient exhaustion should fall back: %v", err)
	}
	title, _ := dl.GetTitle()
	if title != "youtu_be_abc123" {
		t.Fatalf("fallback title = %q", title)
	}
	data, _ := os.ReadFile(counterPath)
	count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if count != metadataAttempts {
		t.Fatalf("attempts = %d, want %d", count, metadataAttempts)
	}
}

func TestMetadataProbeAppliesCommonArguments(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args")
	script := fmt.Sprintf("printf '%%s\\n' \"$@\" > %q\nprintf '{\"title\":\"Title\"}\\n'\n", argsPath)
	cfg := testutils.TestConfig(t.TempDir())
	cfg.YtdlpPath = writeFakeYTDLP(t, script)
	cfg.YtdlpExtraArgs = "--retries 7 --extractor-retries 4"
	cfg.YtdlpCookiesPath = "/safe/youtube.cookies.txt"
	cfg.Proxy = "socks5://user:password@proxy.example:1080"
	cfg.ProxyDomains = "example.com"

	dl, err := NewYTDLPDownloaderContext(context.Background(), "https://example.com/video", cfg)
	if err != nil {
		t.Fatalf("create downloader: %v", err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := string(data)
	for _, expected := range []string{"--retries", "7", "--extractor-retries", "4", "--cookies", cfg.YtdlpCookiesPath, "--proxy", cfg.Proxy, "--remote-components", "ejs:github", "--dump-single-json"} {
		if !strings.Contains(args, expected+"\n") {
			t.Errorf("missing argument %q in %q", expected, args)
		}
	}
	downloadArgs := strings.Join(dl.(*YTDLPDownloader).buildYTDLPArgs("/tmp/video.mp4"), "\n") + "\n"
	for _, expected := range []string{"--retries", "7", "--cookies", cfg.YtdlpCookiesPath, "--remote-components", "ejs:github"} {
		if !strings.Contains(downloadArgs, expected+"\n") {
			t.Errorf("download args missing %q in %q", expected, downloadArgs)
		}
	}
}

func TestMetadataProbeHonorsParentTimeout(t *testing.T) {
	withoutMetadataRetryDelay(t)
	cfg := testutils.TestConfig(t.TempDir())
	cfg.YtdlpPath = writeFakeYTDLP(t, "exec sleep 1\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := probeVideoMetadata(ctx, "https://example.com/video", cfg)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("metadata probe ignored parent timeout: %v", elapsed)
	}
}

func TestSanitizeYTDLPDiagnosticRemovesURLAndProxy(t *testing.T) {
	cfg := testutils.TestConfig(t.TempDir())
	cfg.Proxy = "socks5://user:secret@proxy.example:1080"
	videoURL := "https://example.com/private?id=secret"
	got := sanitizeYTDLPDiagnostic("debug\nERROR: failed for "+videoURL+" via "+cfg.Proxy, videoURL, cfg)
	if strings.Contains(got, videoURL) || strings.Contains(got, cfg.Proxy) || strings.Contains(got, "user:secret") {
		t.Fatalf("diagnostic leaked sensitive input: %q", got)
	}
}
