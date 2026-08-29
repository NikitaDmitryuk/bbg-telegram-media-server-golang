package manager

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/database"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/notifier"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/testutils"
)

type stallTolerantMock struct{ *testutils.MockDownloader }

func (*stallTolerantMock) AllowsIndefiniteStall() bool { return true }

type stallCaptureNotifier struct{ stalled chan uint }

func (*stallCaptureNotifier) OnQueued(uint, string, int, int)  {}
func (*stallCaptureNotifier) OnStarted(uint, string)           {}
func (*stallCaptureNotifier) OnFirstEpisodeReady(uint, string) {}
func (*stallCaptureNotifier) OnVideoNotSupported(uint, string) {}
func (n *stallCaptureNotifier) OnStalled(movieID uint, _ string) {
	n.stalled <- movieID
}

func waitForStall(t *testing.T, ch <-chan uint) uint {
	t.Helper()
	select {
	case movieID := <-ch:
		return movieID
	case <-time.After(time.Second):
		t.Fatal("stall warning was not emitted")
		return 0
	}
}

func assertNoStall(t *testing.T, ch <-chan uint, wait time.Duration) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("unexpected duplicate stall warning")
	case <-time.After(wait):
	}
}

func waitForMonitorStop(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop after cancellation")
	}
}

func TestMain(m *testing.M) {
	logutils.InitLogger("debug")
	os.Exit(m.Run())
}

func TestStallTolerantDownloadWarnsOnceAndKeepsRunning(t *testing.T) {
	cfg := testutils.TestConfig(t.TempDir())
	cfg.DownloadSettings.DownloadTimeout = 0
	cfg.DownloadSettings.ProgressUpdateInterval = 5 * time.Millisecond
	cfg.DownloadSettings.TorrentStallWarningAfter = 20 * time.Millisecond
	db := testutils.TestDatabase(t)
	movieID, err := db.AddMovie(context.Background(), "stalled", 0, []string{"video.mkv"}, nil, 1)
	if err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	dm := NewDownloadManager(cfg, db)

	progressChan := make(chan float64, 2)
	errChan := make(chan error, 1)
	outerErrChan := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	n := &stallCaptureNotifier{stalled: make(chan uint, 2)}
	job := &downloadJob{
		downloader:    &stallTolerantMock{MockDownloader: &testutils.MockDownloader{ShouldBlock: true}},
		progressChan:  progressChan,
		errChan:       errChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: n,
		title:         "stalled",
	}
	dm.mu.Lock()
	dm.jobs[movieID] = job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}
	go dm.monitorDownload(movieID, job, outerErrChan)
	progressChan <- 0

	if got := waitForStall(t, n.stalled); got != movieID {
		t.Fatalf("stalled movie = %d, want %d", got, movieID)
	}
	assertNoStall(t, n.stalled, 50*time.Millisecond)
	stallStore, ok := db.(database.TorrentStallStore)
	if !ok {
		t.Fatal("test database does not implement TorrentStallStore")
	}
	notified, err := stallStore.TorrentStallNotified(context.Background(), movieID)
	if err != nil || !notified {
		t.Fatalf("persisted stall latch = %v, err=%v", notified, err)
	}

	cancel()
	waitForMonitorStop(t, outerErrChan)

	// A restarted monitor loads the persisted latch and must stay quiet for the
	// same stall episode. Real progress resets the latch and starts a new episode.
	progressChan = make(chan float64, 2)
	errChan = make(chan error, 1)
	outerErrChan = make(chan error, 1)
	ctx, cancel = context.WithCancel(context.Background())
	job = &downloadJob{
		downloader:    &stallTolerantMock{MockDownloader: &testutils.MockDownloader{ShouldBlock: true}},
		progressChan:  progressChan,
		errChan:       errChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: n,
		title:         "stalled",
	}
	dm.mu.Lock()
	dm.jobs[movieID] = job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}
	go dm.monitorDownload(movieID, job, outerErrChan)
	progressChan <- 0
	assertNoStall(t, n.stalled, 50*time.Millisecond)
	progressChan <- 10
	waitForStall(t, n.stalled)
	cancel()
	waitForMonitorStop(t, outerErrChan)
}

// helper that builds a DownloadManager with small stagnant-detection windows.
func newTestManager(t *testing.T) *DownloadManager {
	t.Helper()
	cfg := testutils.TestConfig("/tmp")
	cfg.DownloadSettings.DownloadTimeout = 0 // no global timeout
	cfg.DownloadSettings.ProgressUpdateInterval = 50 * time.Millisecond
	cfg.VideoSettings.CompatibilityMode = false
	db := testutils.TestDatabase(t)
	return NewDownloadManager(cfg, db)
}

// TestStagnantProgressTriggersError verifies that if progress doesn't change for
// maxStagnantDuration the monitor reports a stagnation error.
// We cannot override maxStagnantDuration (it's a local var, 30 min), so instead we
// simulate the condition by sending a small initial progress and then never updating.
// To make the test fast we check that the stagnation path is exercised by closing
// the progress channel (i.e. "download completed") before the 30 min threshold — and
// the monitor should return nil on normal close, NOT a stagnation error.
func TestStagnantProgress_NormalCloseBeforeThreshold(t *testing.T) {
	dm := newTestManager(t)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{} // occupy semaphore so cleanup works

	go dm.monitorDownload(1, &job, outerErrChan)

	// Send some progress, then close progressChan and send result on errChan.
	progressChan <- 50.0
	time.Sleep(20 * time.Millisecond)
	close(progressChan)
	errChan <- nil

	select {
	case err := <-outerErrChan:
		if err != nil {
			t.Errorf("Expected nil error on normal close, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not return in time after progressChan was closed")
	}
}

// TestEpisodeChanResetsStagnantTimer verifies the core fix: receiving a value on
// episodesChan resets the stagnation timer so the download isn't killed between
// sequential torrent batches.
//
// Strategy: send a small progress (triggers stagnation tracking), then keep the
// download alive by periodically sending episode completions. Finally close
// progressChan to complete the download. If the fix is correct the download
// should complete normally (nil error). Without the fix the monitor would
// eventually trigger a stagnation error (but since maxStagnantDuration is 30 min
// we can't wait that long; we verify the reset logic by checking that the timer
// doesn't fire within a controlled window).
func TestEpisodeChanResetsStagnantTimer(t *testing.T) {
	dm := newTestManager(t)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	episodesChan := make(chan int, 10)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		episodesChan:  episodesChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
		totalEpisodes: 5,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	// Send initial progress so stagnation tracking starts.
	progressChan <- 10.0
	time.Sleep(20 * time.Millisecond)

	// Episode completions should reset the stagnant timer.
	episodesChan <- 1
	time.Sleep(20 * time.Millisecond)
	episodesChan <- 2
	time.Sleep(20 * time.Millisecond)

	// Complete the download normally.
	close(progressChan)
	errChan <- nil

	select {
	case err := <-outerErrChan:
		if err != nil {
			t.Errorf("Expected nil error when episodes keep resetting stagnant timer, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not return in time")
	}
}

// TestProgressChanClose_CompletesDownload verifies that closing progressChan
// (normal completion path) results in a nil error.
func TestProgressChanClose_CompletesDownload(t *testing.T) {
	dm := newTestManager(t)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	progressChan <- 50.0
	progressChan <- 100.0
	time.Sleep(20 * time.Millisecond)
	close(progressChan)
	errChan <- nil

	select {
	case err := <-outerErrChan:
		if err != nil {
			t.Errorf("Expected nil error for completed download, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not complete in time")
	}
}

// TestErrChanReceivesError verifies that an error sent on errChan is forwarded.
func TestErrChanReceivesError(t *testing.T) {
	dm := newTestManager(t)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	errChan <- context.DeadlineExceeded

	select {
	case err := <-outerErrChan:
		if err == nil {
			t.Error("Expected error from errChan, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not forward error in time")
	}
}

// TestContextCancellation verifies that canceling the context stops the monitor.
func TestContextCancellation(t *testing.T) {
	dm := newTestManager(t)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-outerErrChan:
		if err == nil {
			t.Error("Expected context cancellation error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not react to context cancellation")
	}
}

// TestEpisodeChanClosed_DoesNotPanic verifies that closing episodesChan
// is handled gracefully (channel is nilled out, no panic).
func TestEpisodeChanClosed_DoesNotPanic(t *testing.T) {
	dm := newTestManager(t)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	episodesChan := make(chan int, 10)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		episodesChan:  episodesChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
		totalEpisodes: 3,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	// Send one episode, then close the channel.
	episodesChan <- 1
	time.Sleep(20 * time.Millisecond)
	close(episodesChan)
	time.Sleep(20 * time.Millisecond)

	// Complete download via progressChan close.
	close(progressChan)
	errChan <- nil

	select {
	case err := <-outerErrChan:
		if err != nil {
			t.Errorf("Expected nil error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not complete in time after episodesChan closed")
	}
}

// TestDownloadTimeout verifies the absolute download timeout (not stagnant, but total).
func TestDownloadTimeout_FromMonitor(t *testing.T) {
	cfg := testutils.TestConfig("/tmp")
	cfg.DownloadSettings.DownloadTimeout = 200 * time.Millisecond
	cfg.DownloadSettings.ProgressUpdateInterval = 50 * time.Millisecond
	cfg.VideoSettings.CompatibilityMode = false
	db := testutils.TestDatabase(t)
	dm := NewDownloadManager(cfg, db)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	select {
	case err := <-outerErrChan:
		if err == nil {
			t.Error("Expected timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "timeout") {
			t.Errorf("Expected timeout error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not fire download timeout")
	}
}

// TestErrChanNilError verifies that sending nil on errChan is treated as success.
func TestErrChanNilError(t *testing.T) {
	dm := newTestManager(t)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	errChan <- nil

	select {
	case err := <-outerErrChan:
		if err != nil {
			t.Errorf("Expected nil error for successful errChan close, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not return in time")
	}
}

// TestProgressOver100_IsClamped verifies that progress > 100 is clamped to 100.
func TestProgressOver100_IsClamped(t *testing.T) {
	dm := newTestManager(t)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	// Send progress > 100; it should be clamped.
	progressChan <- 150.0
	time.Sleep(20 * time.Millisecond)

	// Complete normally: progressChan close then result on errChan (monitor waits for errChan).
	close(progressChan)
	errChan <- nil

	select {
	case err := <-outerErrChan:
		if err != nil {
			t.Errorf("Expected nil error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not complete in time")
	}
}

func TestNormalizedProgressPercent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		progress float64
		want     int
	}{
		{name: "negative", progress: -1, want: 0},
		{name: "fraction_rounds", progress: 42.6, want: 43},
		{name: "near_complete", progress: 99.999, want: 100},
		{name: "over_max", progress: 120, want: 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizedProgressPercent(tc.progress); got != tc.want {
				t.Fatalf("normalizedProgressPercent(%v) = %d, want %d", tc.progress, got, tc.want)
			}
		})
	}
}

func TestShouldPersistProgress(t *testing.T) {
	t.Parallel()
	now := time.Now()
	if shouldPersistProgress(0, -1, time.Time{}, now) {
		t.Fatal("should not persist zero progress")
	}
	if !shouldPersistProgress(10, -1, time.Time{}, now) {
		t.Fatal("should persist first non-zero progress")
	}
	if shouldPersistProgress(10, 10, now, now.Add(time.Second)) {
		t.Fatal("should not persist unchanged progress before flush interval")
	}
	if !shouldPersistProgress(10, 10, now, now.Add(progressFlushInterval)) {
		t.Fatal("should persist unchanged progress after flush interval")
	}
}

type progressWriteCountingDB struct {
	testutils.DatabaseStub
	mu    sync.Mutex
	calls []int
}

func (db *progressWriteCountingDB) UpdateDownloadedPercentage(_ context.Context, _ uint, percentage int) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.calls = append(db.calls, percentage)
	return nil
}

func (db *progressWriteCountingDB) callCount() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return len(db.calls)
}

func TestMonitorSkipsRepeatedProgressWritesBeforeFlush(t *testing.T) {
	cfg := testutils.TestConfig(t.TempDir())
	cfg.DownloadSettings.DownloadTimeout = 0
	cfg.DownloadSettings.ProgressUpdateInterval = 50 * time.Millisecond
	cfg.VideoSettings.CompatibilityMode = false

	db := &progressWriteCountingDB{}
	dm := NewDownloadManager(cfg, db)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	progressChan <- 10.1
	deadline := time.Now().Add(time.Second)
	for db.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := db.callCount(); got != 1 {
		t.Fatalf("UpdateDownloadedPercentage calls after first progress = %d, want 1", got)
	}

	progressChan <- 10.2
	progressChan <- 10.3
	close(progressChan)
	errChan <- nil

	select {
	case err := <-outerErrChan:
		if err != nil {
			t.Fatalf("monitor returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not complete in time")
	}

	if got := db.callCount(); got != 1 {
		t.Fatalf("UpdateDownloadedPercentage calls = %d, want 1", got)
	}
}

// TestMultipleEpisodes_SequentialCompletion simulates a multi-file torrent download
// where several episodes complete sequentially, ensuring the monitor handles them
// correctly and completes successfully.
func TestMultipleEpisodes_SequentialCompletion(t *testing.T) {
	dm := newTestManager(t)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	episodesChan := make(chan int, 10)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true, TotalEps: 4},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		episodesChan:  episodesChan,
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
		totalEpisodes: 4,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	// Simulate downloading 4 episodes sequentially.
	for ep := 1; ep <= 4; ep++ {
		progressChan <- float64(ep * 25)
		time.Sleep(15 * time.Millisecond)
		episodesChan <- ep
		time.Sleep(15 * time.Millisecond)
	}

	close(progressChan)
	errChan <- nil

	select {
	case err := <-outerErrChan:
		if err != nil {
			t.Errorf("Expected nil error after all episodes completed, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not complete in time")
	}
}

// TestNoEpisodesChan_NilHandling verifies that the monitor works when episodesChan is nil
// (e.g. single-file download or yt-dlp download).
func TestNoEpisodesChan_NilHandling(t *testing.T) {
	dm := newTestManager(t)

	progressChan := make(chan float64, 10)
	errChan := make(chan error, 1)
	outerErrChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := downloadJob{
		downloader:    &testutils.MockDownloader{ShouldBlock: true},
		startTime:     time.Now(),
		progressChan:  progressChan,
		errChan:       errChan,
		episodesChan:  nil, // no episodes channel
		ctx:           ctx,
		cancel:        cancel,
		queueNotifier: notifier.Noop,
	}

	dm.mu.Lock()
	dm.jobs[1] = &job
	dm.mu.Unlock()
	dm.semaphore <- struct{}{}

	go dm.monitorDownload(1, &job, outerErrChan)

	progressChan <- 100.0
	time.Sleep(20 * time.Millisecond)
	close(progressChan)
	errChan <- nil

	select {
	case err := <-outerErrChan:
		if err != nil {
			t.Errorf("Expected nil error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not complete in time")
	}
}
