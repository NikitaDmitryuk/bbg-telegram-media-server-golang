package downloads

import (
	"context"
	"errors"
	"strings"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/app"
	tmsdownloader "github.com/NikitaDmitryuk/telegram-media-server/internal/downloader"
	tmslang "github.com/NikitaDmitryuk/telegram-media-server/internal/lang"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/utils"
)

// sendDownloadStartError sends a user-facing message for download start failure (same message shape as API).
func sendDownloadStartError(a *app.App, chatID int64, err error, replyMarkup any) {
	if errors.Is(err, tmsdownloader.ErrVideoAuthenticationRequired) {
		a.Bot.SendMessage(chatID, tmslang.Translate("error.downloads.video_authentication_required", nil), replyMarkup)
		return
	}
	msg := utils.DownloadErrorMessage(err)
	if strings.Contains(strings.ToLower(msg), "invalid magnet") {
		a.Bot.SendMessage(chatID, tmslang.Translate("error.downloads.invalid_magnet_format", nil), replyMarkup)
		return
	}
	a.Bot.SendMessage(chatID, tmslang.Translate("error.downloads.download_start_error", map[string]any{"Error": msg}), replyMarkup)
}

func HandleDownload(
	a *app.App,
	chatID int64,
	downloaderInstance tmsdownloader.Downloader,
) {
	go handleDownloadAsync(a, chatID, downloaderInstance)
}

func handleDownloadAsync(
	a *app.App,
	chatID int64,
	downloaderInstance tmsdownloader.Downloader,
) {
	videoTitle, err := downloaderInstance.GetTitle()
	if err != nil {
		logutils.Log.WithError(err).Error("Failed to get video title")
		a.Bot.SendMessage(chatID, tmslang.Translate("error.downloads.video_title_error", map[string]any{
			"Error": utils.DownloadErrorMessage(err),
		}), nil)
		return
	}

	if validateErr := app.ValidateDownloadStart(context.Background(), a, downloaderInstance); validateErr != nil {
		logutils.Log.WithError(validateErr).Debug("ValidateDownloadStart failed")
		switch {
		case errors.Is(validateErr, app.ErrAlreadyExists):
			logutils.Log.Warn("Media already exists")
			a.Bot.SendMessage(chatID, tmslang.Translate("error.movies.already_exists", nil), nil)
		case errors.Is(validateErr, app.ErrNotEnoughSpace):
			logutils.Log.Warn("Not enough space for the download")
			a.Bot.SendMessage(chatID, tmslang.Translate("error.storage.not_enough_space", nil), nil)
		default:
			a.Bot.SendMessage(chatID, tmslang.Translate("error.movies.check_error", map[string]any{
				"Error": utils.DownloadErrorMessage(validateErr),
			}), nil)
		}
		return
	}

	tgNotifier := telegramNotifier{chatID: chatID, app: a}
	movieID, _, completionChan, err := a.DownloadManager.StartDownload(downloaderInstance, tgNotifier)
	if err != nil {
		logutils.Log.WithError(err).Error("Failed to start download")
		sendDownloadStartError(a, chatID, err, nil)
		return
	}

	a.Bot.SendMessage(chatID, tmslang.Translate("general.video_downloading", map[string]any{
		"Title": videoTitle,
	}), nil)

	go app.RunCompletionLoop(a, completionChan, downloaderInstance, movieID, videoTitle, tgNotifier)
}

// telegramNotifier implements notifier.CompletionNotifier and notifier.QueueNotifier for bot-originated downloads.
type telegramNotifier struct {
	chatID int64
	app    *app.App
}

func (n telegramNotifier) OnStopped(_ uint, _ string) {
	// No message on manual stop (user already knows they stopped it).
	_ = n
}

func (n telegramNotifier) OnFailed(_ uint, _ string, err error) {
	if errors.Is(err, tmsdownloader.ErrVideoAuthenticationRequired) {
		n.app.Bot.SendMessage(n.chatID, tmslang.Translate("error.downloads.video_authentication_required", nil), nil)
		return
	}
	n.app.Bot.SendMessage(n.chatID, tmslang.Translate("error.downloads.video_download_error", map[string]any{
		"Error": utils.DownloadErrorMessage(err),
	}), nil)
}

func (n telegramNotifier) OnCompleted(_ uint, title string) {
	n.app.Bot.SendMessage(n.chatID, tmslang.Translate("general.video_successfully_downloaded", map[string]any{
		"Title": title,
	}), nil)
}

func (n telegramNotifier) OnQueued(_ uint, title string, position, maxConcurrent int) {
	msg := tmslang.Translate("general.download_queued", map[string]any{
		"Title":         title,
		"Position":      position,
		"MaxConcurrent": maxConcurrent,
	})
	n.app.Bot.SendMessage(n.chatID, msg, nil)
}

func (n telegramNotifier) OnStarted(_ uint, title string) {
	msg := tmslang.Translate("general.download_started_from_queue", map[string]any{"Title": title})
	n.app.Bot.SendMessage(n.chatID, msg, nil)
}

func (n telegramNotifier) OnFirstEpisodeReady(_ uint, title string) {
	msg := tmslang.Translate("general.first_episode_ready", map[string]any{"Title": title})
	n.app.Bot.SendMessage(n.chatID, msg, nil)
}

func (n telegramNotifier) OnVideoNotSupported(_ uint, title string) {
	msg := tmslang.Translate("general.video_not_supported", map[string]any{"Title": title})
	n.app.Bot.SendMessage(n.chatID, msg, nil)
}
