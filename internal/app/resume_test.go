package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/database"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/downloader"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/lang"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/testutils"
)

type adminListDatabase struct {
	database.Database
	chatIDs []int64
}

type completionCapture struct{ failed error }

func (*completionCapture) OnStopped(uint, string)   {}
func (*completionCapture) OnCompleted(uint, string) {}
func (c *completionCapture) OnFailed(_ uint, _ string, err error) {
	c.failed = err
}

func (d *adminListDatabase) ListAdminChatIDs(context.Context) ([]int64, error) {
	return d.chatIDs, nil
}

func TestAdminQueueNotifierSendsStallToEveryAdmin(t *testing.T) {
	logutils.InitLogger("error")
	cfg := testutils.TestConfig(t.TempDir())
	cfg.Lang = "en"
	cfg.LangPath = filepath.Join("..", "..", "locales")
	if err := lang.InitLocalizer(cfg); err != nil {
		t.Fatalf("InitLocalizer: %v", err)
	}
	bot := &testutils.MockBot{}
	a := &App{
		Bot: bot,
		DB: &adminListDatabase{
			Database: testutils.TestDatabase(t),
			chatIDs:  []int64{101, 202},
		},
		Config: cfg,
	}

	NewAdminQueueNotifier(a).OnStalled(7, "Example")
	if len(bot.SentMessages) != 2 {
		t.Fatalf("sent messages = %d, want 2", len(bot.SentMessages))
	}
	if bot.SentMessages[0].ChatID != 101 || bot.SentMessages[1].ChatID != 202 {
		t.Fatalf("unexpected recipients: %+v", bot.SentMessages)
	}
}

func TestNextResumeDelayIsCapped(t *testing.T) {
	t.Parallel()
	delay := qbittorrentResumeInitialDelay
	want := []time.Duration{4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute}
	for i, expected := range want {
		delay = nextResumeDelay(delay)
		if delay != expected {
			t.Fatalf("step %d delay=%v, want %v", i, delay, expected)
		}
	}
}

func TestAmbiguousControlFailureKeepsMovieRecord(t *testing.T) {
	logutils.InitLogger("error")
	db := testutils.TestDatabase(t)
	movieID, err := db.AddMovie(context.Background(), "ambiguous", 0, []string{"video.mkv"}, nil, 1)
	if err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	a := &App{DB: db, Config: testutils.TestConfig(t.TempDir())}
	result := make(chan error, 1)
	result <- downloader.ErrAmbiguousControlPlane
	capture := &completionCapture{}

	RunCompletionLoop(a, result, &testutils.MockDownloader{}, movieID, "ambiguous", capture)
	exists, err := db.MovieExistsId(context.Background(), movieID)
	if err != nil || !exists {
		t.Fatalf("movie record was removed: exists=%v err=%v", exists, err)
	}
	if !errors.Is(capture.failed, downloader.ErrAmbiguousControlPlane) {
		t.Fatalf("failure notification error=%v", capture.failed)
	}
}
