package manager

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/testutils"
)

// firstEpisodeMockDB embeds DatabaseStub and records UpdateEpisodesProgress calls.
type firstEpisodeMockDB struct {
	testutils.DatabaseStub
	updateEpisodesCalls []struct {
		movieID   uint
		completed int
	}
}

func (m *firstEpisodeMockDB) UpdateEpisodesProgress(_ context.Context, movieID uint, completed int) error {
	m.updateEpisodesCalls = append(m.updateEpisodesCalls, struct {
		movieID   uint
		completed int
	}{movieID, completed})
	return nil
}

func (*firstEpisodeMockDB) AddMovie(_ context.Context, _ string, _ int64, _, _ []string, _ int) (uint, error) {
	return 1, nil
}

func (*firstEpisodeMockDB) MovieExistsId(_ context.Context, _ uint) (bool, error) {
	return true, nil
}

// firstEpisodeDownloader sends episode 1 first, then progress after delay so monitor sees first_episode_ready.
type firstEpisodeDownloader struct {
	title string
}

func (d *firstEpisodeDownloader) GetTitle() (string, error) { return d.title, nil }
func (*firstEpisodeDownloader) GetFiles() (mainFiles, tempFiles []string, err error) {
	return []string{"e01.mkv"}, []string{"t.torrent"}, nil
}
func (*firstEpisodeDownloader) GetFileSize() (int64, error) { return 1024, nil }
func (*firstEpisodeDownloader) TotalEpisodes() int          { return 8 }
func (*firstEpisodeDownloader) StoppedManually() bool       { return false }
func (*firstEpisodeDownloader) StopDownload() error         { return nil }

func (*firstEpisodeDownloader) StartDownload(
	_ context.Context,
) (progressChan chan float64, errChan chan error, episodesChan <-chan int, err error) {
	progressChan = make(chan float64, 1)
	errChan = make(chan error, 1)
	epCh := make(chan int, 1)
	epCh <- 1
	close(epCh)
	go func() {
		time.Sleep(100 * time.Millisecond) // let monitor read episodesChan first
		progressChan <- 100.0
		errChan <- nil
		close(progressChan)
		close(errChan)
	}()
	return progressChan, errChan, epCh, nil
}

// firstEpisodeTestNotifier records OnFirstEpisodeReady calls for assertion.
type firstEpisodeTestNotifier struct {
	mu       sync.Mutex
	movieID  uint
	title    string
	received bool
}

func (*firstEpisodeTestNotifier) OnQueued(uint, string, int, int) {}
func (*firstEpisodeTestNotifier) OnStarted(uint, string)          {}
func (n *firstEpisodeTestNotifier) OnFirstEpisodeReady(movieID uint, title string) {
	n.mu.Lock()
	n.movieID = movieID
	n.title = title
	n.received = true
	n.mu.Unlock()
}
func (*firstEpisodeTestNotifier) OnVideoNotSupported(uint, string) {}
func (*firstEpisodeTestNotifier) OnStalled(uint, string)           {}

func TestFirstEpisodeReadyNotification(t *testing.T) {
	logutils.InitLogger("debug")

	tempDir := testutils.TempDir(t)
	cfg := testutils.TestConfig(tempDir)
	mockDB := &firstEpisodeMockDB{}

	dm := NewDownloadManager(cfg, mockDB)
	const title = "Test Series S01"

	mockDl := &firstEpisodeDownloader{title: title}
	testNotifier := &firstEpisodeTestNotifier{}

	movieID, _, errCh, err := dm.StartDownload(mockDl, testNotifier)
	if err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	if movieID != 1 {
		t.Fatalf("expected movieID 1, got %d", movieID)
	}

	// Wait for either first_episode_ready or download failure
	timeout := time.After(5 * time.Second)
	for {
		select {
		case err := <-errCh:
			t.Fatalf("download failed: %v", err)
		case <-timeout:
			t.Fatal("timeout waiting for first_episode_ready notification")
		default:
			testNotifier.mu.Lock()
			ok := testNotifier.received
			testNotifier.mu.Unlock()
			if ok {
				goto asserted
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
asserted:
	testNotifier.mu.Lock()
	defer testNotifier.mu.Unlock()
	if testNotifier.title != title {
		t.Errorf("notification Title: want %q, got %q", title, testNotifier.title)
	}
	if testNotifier.movieID != 1 {
		t.Errorf("notification MovieID: want 1, got %d", testNotifier.movieID)
	}

	if len(mockDB.updateEpisodesCalls) < 1 {
		t.Errorf("expected at least one UpdateEpisodesProgress call, got %d", len(mockDB.updateEpisodesCalls))
	} else {
		call := mockDB.updateEpisodesCalls[0]
		if call.movieID != 1 || call.completed != 1 {
			t.Errorf("UpdateEpisodesProgress: want movieID=1 completed=1, got movieID=%d completed=%d", call.movieID, call.completed)
		}
	}

	// Wait for download to finish (progress 100, errChan closed) so monitor exits
	<-errCh
}
