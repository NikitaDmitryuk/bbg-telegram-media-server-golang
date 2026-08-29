package ytdlp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tmsconfig "github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
)

const windowsGOOS = "windows"

func TestMain(m *testing.M) {
	if logutils.Log == nil {
		logutils.InitLogger("error")
	}
	os.Exit(m.Run())
}

func TestStartPeriodicUpdater_ZeroInterval_ReturnsImmediately(t *testing.T) {
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		downloader.StartPeriodicUpdater(ctx, 0, NewUpdater(&tmsconfig.Config{YtdlpPath: "yt-dlp"}))
		close(done)
	}()
	select {
	case <-done:
		// OK
	case <-time.After(time.Second):
		t.Fatal("StartPeriodicUpdater(_, 0) did not return immediately")
	}
}

func TestStartPeriodicUpdater_NegativeInterval_ReturnsImmediately(t *testing.T) {
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		downloader.StartPeriodicUpdater(ctx, -time.Hour, NewUpdater(&tmsconfig.Config{YtdlpPath: "yt-dlp"}))
		close(done)
	}()
	select {
	case <-done:
		// OK
	case <-time.After(time.Second):
		t.Fatal("StartPeriodicUpdater(_, negative) did not return immediately")
	}
}

func TestStartPeriodicUpdater_StopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		downloader.StartPeriodicUpdater(ctx, 10*time.Millisecond, NewUpdater(&tmsconfig.Config{YtdlpPath: "yt-dlp"}))
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("StartPeriodicUpdater did not stop after context cancel")
	}
}

func TestRunUpdate_WithCanceledContext_ReturnsWithoutPanic(_ *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	RunUpdate(ctx, "yt-dlp", "self", "python3")
	// No panic, returns quickly (may log warning)
}

func TestRunUpdate_WithTimeoutContext_ReturnsWithoutPanic(_ *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	RunUpdate(ctx, "yt-dlp", "self", "python3")
	// No panic; update will almost certainly not finish in 1ms
}

func TestRunUpdate_Success_WithFakeYtdlp(t *testing.T) {
	if runtime.GOOS == windowsGOOS {
		t.Skip("skipping fake yt-dlp script test on Windows")
	}
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "yt-dlp")
	logPath := filepath.Join(tmpDir, "args.log")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nexit 0\n", logPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	if err := os.Chmod(scriptPath, 0755); err != nil {
		t.Fatalf("chmod fake yt-dlp: %v", err)
	}
	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)
	os.Setenv("PATH", tmpDir+string(filepath.ListSeparator)+origPath)

	RunUpdate(context.Background(), scriptPath, "self", "python3")

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	if strings.TrimSpace(string(got)) != "-U" {
		t.Fatalf("expected self-update args -U, got %q", string(got))
	}
}

func TestRunUpdate_ExitFailure_WithFakeYtdlp(t *testing.T) {
	if runtime.GOOS == windowsGOOS {
		t.Skip("skipping fake yt-dlp script test on Windows")
	}
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "yt-dlp")
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	if err := os.Chmod(scriptPath, 0755); err != nil {
		t.Fatalf("chmod fake yt-dlp: %v", err)
	}
	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)
	os.Setenv("PATH", tmpDir+string(filepath.ListSeparator)+origPath)

	RunUpdate(context.Background(), scriptPath, "self", "python3")
	// No panic; RunUpdate logs failure but does not crash
}

func TestRunUpdate_PipMode_WithFakePython(t *testing.T) {
	if runtime.GOOS == windowsGOOS {
		t.Skip("skipping fake python script test on Windows")
	}
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "python")
	logPath := filepath.Join(tmpDir, "args.log")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nexit 0\n", logPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	if err := os.Chmod(scriptPath, 0755); err != nil {
		t.Fatalf("chmod fake python: %v", err)
	}

	RunUpdate(context.Background(), "yt-dlp", "pip", scriptPath)

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	want := "-m\npip\ninstall\n--upgrade\n--no-cache-dir\nyt-dlp"
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("expected pip update args %q, got %q", want, strings.TrimSpace(string(got)))
	}
}

func TestRunUpdate_InvalidMode_ReturnsWithoutRunning(t *testing.T) {
	if runtime.GOOS == windowsGOOS {
		t.Skip("skipping fake yt-dlp script test on Windows")
	}
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "yt-dlp")
	logPath := filepath.Join(tmpDir, "called")
	script := fmt.Sprintf("#!/bin/sh\ntouch %q\nexit 0\n", logPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	if err := os.Chmod(scriptPath, 0755); err != nil {
		t.Fatalf("chmod fake yt-dlp: %v", err)
	}

	RunUpdate(context.Background(), scriptPath, "invalid", "python3")

	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("invalid mode should not run updater command, stat err=%v", err)
	}
}

func TestRunUpdate_SkipsWhileYTDLPIsInUse(t *testing.T) {
	if runtime.GOOS == windowsGOOS {
		t.Skip("skipping fake yt-dlp script test on Windows")
	}
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "yt-dlp")
	calledPath := filepath.Join(tmpDir, "called")
	script := fmt.Sprintf("#!/bin/sh\nprintf called > %q\n", calledPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	if err := os.Chmod(scriptPath, 0o700); err != nil {
		t.Fatalf("chmod fake yt-dlp: %v", err)
	}

	releaseExecution := acquireYTDLPExecution()
	RunUpdate(context.Background(), scriptPath, "self", "python3")
	releaseExecution()

	if _, err := os.Stat(calledPath); !os.IsNotExist(err) {
		t.Fatalf("updater should not run while yt-dlp is in use, stat err=%v", err)
	}
}
