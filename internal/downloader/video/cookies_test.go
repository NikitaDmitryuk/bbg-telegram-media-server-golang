package ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tmsconfig "github.com/NikitaDmitryuk/telegram-media-server/internal/config"
)

const testCookieJar = "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tSID\tvalue\n.google.com\tTRUE\t/\tTRUE\t0\tOTHER\tsecret\n"

func cookieTestConfig(t *testing.T) *tmsconfig.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "youtube.cookies.txt")
	if err := os.WriteFile(path, []byte(testCookieJar), 0o600); err != nil {
		t.Fatalf("write cookie jar: %v", err)
	}
	return &tmsconfig.Config{
		YtdlpCookiesPath:          path,
		YtdlpCookiesCheckInterval: time.Hour,
		YtdlpPath:                 "yt-dlp",
	}
}

func TestCookiesUsablePreservesAnonymousMode(t *testing.T) {
	if cookiesUsable(&tmsconfig.Config{}) {
		t.Fatal("empty cookie configuration must stay anonymous")
	}
	cfg := cookieTestConfig(t)
	if !cookiesUsable(cfg) {
		t.Fatal("existing valid cookie jar should be usable")
	}
	markCookiesInvalid(cfg)
	if cookiesUsable(cfg) {
		t.Fatal("invalid cookie jar must be disabled")
	}
}

func TestNotifyInvalidCookiesOncePerEpisode(t *testing.T) {
	cfg := cookieTestConfig(t)
	markCookiesInvalid(cfg)
	var notifications atomic.Int32
	notify := func(context.Context) bool {
		notifications.Add(1)
		return true
	}

	notifyInvalidCookiesOnce(context.Background(), cfg, notify)
	notifyInvalidCookiesOnce(context.Background(), cfg, notify)
	if got := notifications.Load(); got != 1 {
		t.Fatalf("notifications=%d, want 1", got)
	}
	if err := markCookiesValid(cfg); err != nil {
		t.Fatalf("mark valid: %v", err)
	}
	markCookiesInvalid(cfg)
	notifyInvalidCookiesOnce(context.Background(), cfg, notify)
	if got := notifications.Load(); got != 2 {
		t.Fatalf("notifications after new invalid episode=%d, want 2", got)
	}
}

func TestFilterYouTubeCookieJar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte(testCookieJar), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filterYouTubeCookieJar(path); err != nil {
		t.Fatalf("filter: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !containsAll(text, "# Netscape HTTP Cookie File", ".youtube.com", "SID") {
		t.Fatalf("filtered jar lost YouTube cookie: %q", text)
	}
	if containsAll(text, ".google.com") {
		t.Fatalf("filtered jar retained non-YouTube cookie: %q", text)
	}
}

func TestCookieCheckTransientFailureKeepsCookiesEnabled(t *testing.T) {
	if runtime.GOOS == windowsGOOS {
		t.Skip("fake executable requires a Unix shell")
	}
	cfg := cookieTestConfig(t)
	cfg.YtdlpPath = writeFakeYTDLP(t, "printf 'temporary network failure\\n' >&2\nexit 1\n")
	checkAndRefreshCookies(context.Background(), cfg)
	if !cookiesUsable(cfg) {
		t.Fatal("transient validation failure must not disable cookies")
	}
}

func TestCookieCheckAuthenticationFailureDisablesCookies(t *testing.T) {
	if runtime.GOOS == windowsGOOS {
		t.Skip("fake executable requires a Unix shell")
	}
	cfg := cookieTestConfig(t)
	cfg.YtdlpPath = writeFakeYTDLP(t, "printf 'ERROR: Sign in to confirm your age\\n' >&2\nexit 1\n")
	checkAndRefreshCookies(context.Background(), cfg)
	if cookiesUsable(cfg) {
		t.Fatal("authentication failure must disable cookies")
	}
}

func TestCookieCheckRefreshesAndFiltersJarAtomically(t *testing.T) {
	if runtime.GOOS == windowsGOOS {
		t.Skip("fake executable requires a Unix shell")
	}
	cfg := cookieTestConfig(t)
	cfg.YtdlpPath = writeFakeYTDLP(t, `
cookie_file=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--cookies" ]; then shift; cookie_file=$1; break; fi
  shift
done
printf '.google.com\tTRUE\t/\tTRUE\t0\tOTHER\tsecret\n' >> "${cookie_file}"
`)
	checkAndRefreshCookies(context.Background(), cfg)
	if !cookiesUsable(cfg) {
		t.Fatal("successful refresh should keep cookies enabled")
	}
	data, err := os.ReadFile(cfg.YtdlpCookiesPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), ".google.com") {
		t.Fatalf("refreshed jar retained non-YouTube cookie: %q", data)
	}
	info, err := os.Stat(cfg.YtdlpCookiesPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != cookieReadOnlyMode {
		t.Fatalf("cookie mode=%o, want %o", info.Mode().Perm(), cookieReadOnlyMode)
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
