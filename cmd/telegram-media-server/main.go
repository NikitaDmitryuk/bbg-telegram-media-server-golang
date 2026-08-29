package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/api"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/app"
	tmsbot "github.com/NikitaDmitryuk/telegram-media-server/internal/bot"
	tmsconfig "github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/database"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/deletion"
	tmsfactory "github.com/NikitaDmitryuk/telegram-media-server/internal/downloader/factory"
	tmsdownloadmanager "github.com/NikitaDmitryuk/telegram-media-server/internal/downloader/manager"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/handlers/common"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/lang"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	config, err := tmsconfig.NewConfig()
	if err != nil {
		logutils.Log.WithError(err).Fatal(
			"Configuration validation failed — fix the reported variable(s) in .env and restart " +
				"(e.g. BOT_TOKEN, MOVIE_PATH, ADMIN_PASSWORD; if PROWLARR_URL is set, PROWLARR_API_KEY is required)")
	}

	logutils.InitLogger(config.LogLevel)
	logutils.Log.WithFields(map[string]any{
		"version":    Version,
		"build_time": BuildTime,
	}).Info("Starting Telegram Media Server")

	tmsfactory.RunUpdatersOnStart(context.Background(), config)

	db, dbErr := database.NewDatabase(config)
	if dbErr != nil {
		logutils.Log.WithError(dbErr).Fatal("Failed to initialize the database")
	}

	if langErr := lang.InitLocalizer(config); langErr != nil {
		logutils.Log.WithError(langErr).Fatal("Failed to initialize localizer")
	}

	downloadManager := tmsdownloadmanager.NewDownloadManager(config, db)
	logutils.Log.Info("Download manager initialized")

	deleteQueue := deletion.NewQueue(config.MoviePath, db, downloadManager)

	botInstance, err := tmsbot.InitBot(config)
	if err != nil {
		logutils.Log.WithError(err).Fatal("Bot initialization failed")
	}

	a := &app.App{
		Bot:             botInstance,
		DB:              db,
		Config:          config,
		DownloadManager: downloadManager,
		DeleteQueue:     deleteQueue,
	}

	app.ResumeIncompleteDownloads(a)
	downloadManager.ResumePendingTVConversions(context.Background())

	var apiServer *api.Server
	if config.TMSAPIEnabled {
		apiServer = api.NewServer(a, config.TMSAPIListen, config.TMSAPIKey)
		go func() {
			if err := apiServer.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logutils.Log.WithError(err).Error("TMS API server exited with error")
			}
		}()
	} else {
		logutils.Log.Info("TMS REST API is disabled (TMS_API_ENABLED=false). Set TMS_API_ENABLED=true in .env to enable Swagger and the API.")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmsfactory.StartPeriodicUpdaters(ctx, config)
	tmsfactory.StartCookieMonitor(ctx, config, func(notifyCtx context.Context) bool {
		chatIDs, listErr := db.ListAdminChatIDs(notifyCtx)
		if listErr != nil {
			logutils.Log.WithError(listErr).Warn("Failed to list administrators for yt-dlp cookie notification")
			return false
		}
		if len(chatIDs) == 0 {
			return false
		}
		message := lang.Translate("error.downloads.youtube_cookies_expired", nil)
		for _, chatID := range chatIDs {
			botInstance.SendMessage(chatID, message, nil)
		}
		return true
	})

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go processUpdates(ctx, a, botInstance)

	logutils.Log.Info("Telegram Media Server started successfully")

	<-sigChan
	logutils.Log.Info("Received shutdown signal, starting graceful shutdown...")

	cancel()

	if apiServer != nil {
		const apiShutdownTimeout = 10 * time.Second
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), apiShutdownTimeout)
		if err := apiServer.Shutdown(shutdownCtx); err != nil {
			logutils.Log.WithError(err).Warn("TMS API shutdown error")
		}
		shutdownCancel()
	}

	a.DownloadManager.StopAllDownloads()
	logutils.Log.Info("All downloads stopped")

	logutils.Log.Info("Telegram Media Server shutdown complete")
}

func processUpdates(
	ctx context.Context,
	a *app.App,
	botInstance *tmsbot.Bot,
) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := botInstance.Api.GetUpdatesChan(u)

	for {
		select {
		case update := <-updates:
			common.Router(a, &update)
		case <-ctx.Done():
			logutils.Log.Info("Stopping update processing")
			return
		}
	}
}
