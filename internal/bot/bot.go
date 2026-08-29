package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tmsconfig "github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var telegramTokenPattern = regexp.MustCompile(`bot\d+:[A-Za-z0-9_-]+`)

// Service defines the interface for all bot operations.
// Implementations must support sending messages, handling callbacks,
// managing files, and editing messages.
type Service interface {
	SendMessage(chatID int64, text string, keyboard any)
	SendMessageReturningID(chatID int64, text string, keyboard any) (int, error)
	SendDocument(chatID int64, fileName string, data []byte) error
	DownloadFile(fileID, fileName string) error
	AnswerCallbackQuery(callbackConfig tgbotapi.CallbackConfig)
	DeleteMessage(chatID int64, messageID int) error
	SaveFile(fileName string, data []byte) error
	EditMessageTextAndMarkup(chatID int64, messageID int, text string, markup tgbotapi.InlineKeyboardMarkup) error
}

type Bot struct {
	Api        *tgbotapi.BotAPI
	Config     *tmsconfig.Config
	httpClient *http.Client
}

func InitBot(config *tmsconfig.Config) (*Bot, error) {
	httpClient, err := buildHTTPClient(config.TelegramProxy)
	if err != nil {
		return nil, fmt.Errorf("invalid TELEGRAM_PROXY: %w", err)
	}

	api, err := tgbotapi.NewBotAPIWithClient(config.BotToken, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		safeErr := sanitizeBotError(err, config.BotToken, config.TelegramProxy)
		logutils.Log.WithError(safeErr).Error("Error creating bot")
		return nil, fmt.Errorf("error creating bot: %w", safeErr)
	}
	logutils.Log.Infof("Authorized on account %s", api.Self.UserName)
	return &Bot{Api: api, Config: config, httpClient: httpClient}, nil
}

func sanitizeBotError(err error, sensitiveValues ...string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, value := range sensitiveValues {
		if value != "" {
			message = strings.ReplaceAll(message, value, "<redacted>")
		}
	}
	message = telegramTokenPattern.ReplaceAllString(message, "bot<redacted>")
	return errors.New(message)
}

func (b *Bot) safeError(err error) error {
	if b == nil || b.Config == nil {
		return sanitizeBotError(err)
	}
	return sanitizeBotError(err, b.Config.BotToken, b.Config.TelegramProxy)
}

func buildHTTPClient(proxyAddr string) (*http.Client, error) {
	if proxyAddr == "" {
		return http.DefaultClient, nil
	}

	proxyURL, err := url.Parse(proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("parse proxy URL %q: %w", proxyAddr, err)
	}

	logutils.Log.Infof("Using Telegram proxy: %s", proxyURL.Redacted())

	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}, nil
}

func (b *Bot) SendMessage(chatID int64, text string, keyboard any) {
	msg := tgbotapi.NewMessage(chatID, text)
	if keyboard != nil {
		switch k := keyboard.(type) {
		case tgbotapi.ReplyKeyboardMarkup:
			msg.ReplyMarkup = k
		case tgbotapi.ReplyKeyboardRemove:
			msg.ReplyMarkup = k
		case tgbotapi.InlineKeyboardMarkup:
			msg.ReplyMarkup = k
		}
	}
	if _, err := b.Api.Send(msg); err != nil {
		logutils.Log.WithError(b.safeError(err)).Errorf("Message not sent: %s", text)
	}
}

func (b *Bot) SendDocument(chatID int64, fileName string, data []byte) error {
	doc := tgbotapi.NewDocument(chatID, tgbotapi.FileBytes{
		Name:  fileName,
		Bytes: data,
	})
	if _, err := b.Api.Send(doc); err != nil {
		safeErr := b.safeError(err)
		logutils.Log.WithError(safeErr).Errorf("Failed to send document %s to chat %d", fileName, chatID)
		return safeErr
	}
	logutils.Log.Infof("Document sent: %s", fileName)
	return nil
}

func (b *Bot) DownloadFile(fileID, fileName string) error {
	file, err := b.Api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		safeErr := b.safeError(err)
		logutils.Log.WithError(safeErr).Error("Failed to get file")
		return safeErr
	}

	fileURL := file.Link(b.Api.Token)
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, http.NoBody)
	if err != nil {
		safeErr := b.safeError(err)
		logutils.Log.WithError(safeErr).Error("Failed to create HTTP request")
		return safeErr
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		safeErr := b.safeError(err)
		logutils.Log.WithError(safeErr).Error("Failed to download file")
		return safeErr
	}
	defer resp.Body.Close()

	out, err := os.Create(filepath.Join(b.Config.MoviePath, fileName))
	if err != nil {
		logutils.Log.WithError(err).Error("Failed to create file")
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, resp.Body); err != nil {
		logutils.Log.WithError(err).Error("Failed to save file")
		return err
	}

	logutils.Log.Info("File downloaded successfully")
	return nil
}

func (b *Bot) AnswerCallbackQuery(callbackConfig tgbotapi.CallbackConfig) {
	if _, err := b.Api.Request(callbackConfig); err != nil {
		logutils.Log.WithError(b.safeError(err)).Error("Failed to answer callback query")
	} else {
		logutils.Log.Info("Callback query answered successfully")
	}
}

func (b *Bot) DeleteMessage(chatID int64, messageID int) error {
	deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)
	_, err := b.Api.Request(deleteMsg)
	if err != nil {
		err = b.safeError(err)
		logutils.Log.WithError(err).Errorf("Failed to delete message %d in chat %d", messageID, chatID)
	}
	return err
}

func (b *Bot) SaveFile(fileName string, data []byte) error {
	path := filepath.Join(b.Config.MoviePath, fileName)
	f, err := os.Create(path)
	if err != nil {
		logutils.Log.WithError(err).Errorf("Failed to create file %s", path)
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	if err != nil {
		logutils.Log.WithError(err).Errorf("Failed to write file %s", path)
		return err
	}
	logutils.Log.Infof("File saved: %s", path)
	return nil
}

func (b *Bot) SendMessageReturningID(chatID int64, text string, keyboard any) (int, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	if keyboard != nil {
		switch k := keyboard.(type) {
		case tgbotapi.ReplyKeyboardMarkup:
			msg.ReplyMarkup = k
		case tgbotapi.ReplyKeyboardRemove:
			msg.ReplyMarkup = k
		case tgbotapi.InlineKeyboardMarkup:
			msg.ReplyMarkup = k
		}
	}
	m, err := b.Api.Send(msg)
	if err != nil {
		safeErr := b.safeError(err)
		logutils.Log.WithError(safeErr).Errorf("Message not sent: %s", text)
		return 0, safeErr
	}
	return m.MessageID, nil
}

func (b *Bot) EditMessageTextAndMarkup(chatID int64, messageID int, text string, markup tgbotapi.InlineKeyboardMarkup) error {
	editMsg := tgbotapi.NewEditMessageTextAndMarkup(chatID, messageID, text, markup)
	if _, err := b.Api.Send(editMsg); err != nil {
		safeErr := b.safeError(err)
		logutils.Log.WithError(safeErr).Errorf("Failed to edit message %d in chat %d", messageID, chatID)
		return safeErr
	}
	return nil
}
