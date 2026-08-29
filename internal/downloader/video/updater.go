package ytdlp

import (
	"context"
	"os/exec"
	"strings"
	"time"

	tmsconfig "github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
)

const updateTimeout = 3 * time.Minute

func RunUpdate(ctx context.Context, binaryPath, updateMode, pythonPath string) {
	if binaryPath == "" {
		binaryPath = defaultYtdlpBinary
	}
	if updateMode == "" {
		updateMode = tmsconfig.DefaultYtdlpUpdateMode
	}
	if pythonPath == "" {
		pythonPath = tmsconfig.DefaultYtdlpPythonPath
	}
	if updateMode == "off" {
		logutils.Log.Info("yt-dlp update disabled")
		return
	}
	releaseUpdate, acquired := tryAcquireYTDLPUpdate()
	if !acquired {
		logutils.Log.Info("yt-dlp update skipped because yt-dlp is currently in use")
		return
	}
	defer releaseUpdate()
	updateCtx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch updateMode {
	case "self":
		cmd = exec.CommandContext(updateCtx, binaryPath, "-U")
	case "pip":
		cmd = exec.CommandContext(updateCtx, pythonPath, "-m", "pip", "install", "--upgrade", "--no-cache-dir", "yt-dlp")
	default:
		logutils.Log.WithField("mode", updateMode).Warn("yt-dlp update skipped: invalid update mode")
		return
	}
	output, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(output))

	if err != nil {
		if updateCtx.Err() != nil {
			logutils.Log.WithError(err).Warn("yt-dlp update timed out or was canceled")
			return
		}
		msg := "yt-dlp update failed: " + err.Error()
		if out != "" {
			msg += "; output: " + out
		}
		logutils.Log.WithError(err).WithFields(map[string]any{
			"output": string(output),
			"binary": binaryPath,
			"mode":   updateMode,
		}).Warn(msg)
		return
	}

	logutils.Log.WithFields(map[string]any{
		"binary": binaryPath,
		"mode":   updateMode,
		"output": out,
	}).Info("yt-dlp update check completed successfully")
}

type ytdlpUpdater struct {
	binaryPath string
	updateMode string
	pythonPath string
}

func (u *ytdlpUpdater) RunUpdate(ctx context.Context) {
	RunUpdate(ctx, u.binaryPath, u.updateMode, u.pythonPath)
}

func NewUpdater(cfg *tmsconfig.Config) downloader.Updater {
	updater := &ytdlpUpdater{
		binaryPath: defaultYtdlpBinary,
		updateMode: tmsconfig.DefaultYtdlpUpdateMode,
		pythonPath: tmsconfig.DefaultYtdlpPythonPath,
	}
	if cfg != nil {
		if cfg.YtdlpPath != "" {
			updater.binaryPath = cfg.YtdlpPath
		}
		if cfg.YtdlpUpdateMode != "" {
			updater.updateMode = cfg.YtdlpUpdateMode
		}
		if cfg.YtdlpPythonPath != "" {
			updater.pythonPath = cfg.YtdlpPythonPath
		}
	}
	return updater
}
