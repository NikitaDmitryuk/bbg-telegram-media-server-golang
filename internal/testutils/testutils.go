package testutils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/database"
	"github.com/jackpal/bencode-go"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	testFileSize   = 1024
	tickerInterval = 10 * time.Millisecond
	testFileMode   = 0600
	byteRange      = 256
	loadedPercent  = 100
)

// TestConfig creates a configuration suitable for testing
func TestConfig(tempDir string) *config.Config {
	return &config.Config{
		BotToken:        "test-bot-token",
		MoviePath:       tempDir,
		AdminPassword:   "test-admin",
		RegularPassword: "test-regular",
		Lang:            "en",
		LangPath:        "./locales",
		LogLevel:        "debug",

		DownloadSettings: config.DownloadConfig{
			MaxConcurrentDownloads: 1,
			DownloadTimeout:        30 * time.Second,
			ProgressUpdateInterval: 100 * time.Millisecond,
		},

		SecuritySettings: config.SecurityConfig{
			PasswordMinLength: 4,
		},

		Aria2Settings: config.Aria2Config{
			MaxPeers:                 50,
			MaxConnectionsPerServer:  4,
			Split:                    4,
			MinSplitSize:             "1M",
			BTMaxPeers:               50,
			BTRequestPeerSpeedLimit:  "0",
			BTMaxOpenFiles:           10,
			MaxOverallUploadLimit:    "100K",
			MaxUploadLimit:           "50K",
			SeedRatio:                0.0,
			SeedTime:                 0,
			BTTrackerTimeout:         10,
			BTTrackerInterval:        0,
			EnableDHT:                false,
			EnablePeerExchange:       false,
			EnableLocalPeerDiscovery: false,
			FollowTorrent:            true,
			ListenPort:               "6881-6882",
			DHTPorts:                 "6881-6882",
			BTSaveMetadata:           false,
			BTHashCheckSeed:          false,
			BTRequireCrypto:          false,
			BTMinCryptoLevel:         "plain",
			CheckIntegrity:           false,
			ContinueDownload:         true,
			RemoteTime:               false,
			FileAllocation:           "none",
			Timeout:                  10,
			MaxTries:                 3,
			RetryWait:                1,
		},

		VideoSettings: config.VideoConfig{
			EnableReencoding:  false,
			ForceReencoding:   false,
			VideoCodec:        "h264",
			AudioCodec:        "mp3",
			OutputFormat:      "mp4",
			FFmpegExtraArgs:   "-pix_fmt yuv420p",
			QualitySelector:   "worst",
			MaxHeight:         0, // No limit by default
			CompatibilityMode: true,
			TvH264Level:       "4.1",
		},
	}
}

// TestDatabase creates an in-memory SQLite database for testing
func TestDatabase(t *testing.T) database.Database {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	testDB := &TestSQLiteDatabase{db: db}
	if err := testDB.runMigrations(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	return testDB
}

// TestSQLiteDatabase is a test-specific implementation
type TestSQLiteDatabase struct {
	db *gorm.DB
}

func (t *TestSQLiteDatabase) runMigrations() error {
	// Copy migration logic from the main database package
	return t.db.AutoMigrate(
		&database.Movie{},
		&database.MovieFile{},
		&database.User{},
		&database.TemporaryPassword{},
	)
}

func (*TestSQLiteDatabase) Init(_ *config.Config) error {
	return nil // Already initialized
}

// Implement all database interface methods by delegating to the real implementation
func (t *TestSQLiteDatabase) AddMovie(
	ctx context.Context,
	name string,
	fileSize int64,
	mainFiles, tempFiles []string,
	totalEpisodes int,
) (uint, error) {
	movie := database.Movie{
		Name:          name,
		FileSize:      fileSize,
		TotalEpisodes: totalEpisodes,
	}
	result := t.db.WithContext(ctx).Create(&movie)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to add movie to database: %w", result.Error)
	}

	if err := t.addFiles(ctx, movie.ID, mainFiles, false); err != nil {
		return 0, fmt.Errorf("failed to add main files for movie %d: %w", movie.ID, err)
	}

	if err := t.addFiles(ctx, movie.ID, tempFiles, true); err != nil {
		return 0, fmt.Errorf("failed to add temporary files for movie %d: %w", movie.ID, err)
	}

	return movie.ID, nil
}

func (t *TestSQLiteDatabase) UpdateMovieName(ctx context.Context, movieID uint, name string) error {
	return t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).Update("name", name).Error
}

func (t *TestSQLiteDatabase) addFiles(ctx context.Context, movieID uint, files []string, isTemp bool) error {
	for _, file := range files {
		movieFile := database.MovieFile{
			MovieID:  movieID,
			FilePath: file,
			TempFile: isTemp,
		}
		if result := t.db.WithContext(ctx).Create(&movieFile); result.Error != nil {
			return fmt.Errorf("failed to add file %s: %w", file, result.Error)
		}
	}
	return nil
}

func (t *TestSQLiteDatabase) RemoveMovie(ctx context.Context, movieID uint) error {
	return t.db.WithContext(ctx).Delete(&database.Movie{}, movieID).Error
}

func (t *TestSQLiteDatabase) GetMovieList(ctx context.Context) ([]database.Movie, error) {
	var movies []database.Movie
	if err := t.db.WithContext(ctx).Find(&movies).Error; err != nil {
		return nil, err
	}
	return movies, nil
}

func (t *TestSQLiteDatabase) UpdateDownloadedPercentage(ctx context.Context, movieID uint, percentage int) error {
	return t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).
		Update("downloaded_percentage", percentage).Error
}

func (t *TestSQLiteDatabase) UpdateEpisodesProgress(ctx context.Context, movieID uint, completedEpisodes int) error {
	return t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).
		Update("completed_episodes", completedEpisodes).Error
}

func (t *TestSQLiteDatabase) SetLoaded(ctx context.Context, movieID uint, _ string) error {
	return t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).
		Update("downloaded_percentage", loadedPercent).Error
}

func (t *TestSQLiteDatabase) RefreshMovieFileSizeFromDisk(ctx context.Context, movieID uint, movieRoot string) (int64, error) {
	files, err := t.GetFilesByMovieID(ctx, movieID)
	if err != nil {
		return 0, err
	}
	var sum int64
	for i := range files {
		info, statErr := os.Stat(filepath.Join(movieRoot, files[i].FilePath))
		if statErr == nil {
			sum += info.Size()
		}
	}
	if sum > 0 {
		if err := t.UpdateMovieFileSize(ctx, movieID, sum); err != nil {
			return 0, err
		}
	}
	return sum, nil
}

func (t *TestSQLiteDatabase) UpdateConversionStatus(ctx context.Context, movieID uint, status string) error {
	return t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).
		Update("conversion_status", status).Error
}

func (t *TestSQLiteDatabase) UpdateConversionPercentage(ctx context.Context, movieID uint, percentage int) error {
	return t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).
		Update("conversion_percentage", percentage).Error
}

func (t *TestSQLiteDatabase) SetTvCompatibility(ctx context.Context, movieID uint, compat string) error {
	return t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).
		Update("tv_compatibility", compat).Error
}

func (t *TestSQLiteDatabase) SetQBittorrentHash(ctx context.Context, movieID uint, hash string) error {
	return t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).
		Update("qbittorrent_hash", hash).Error
}

func (t *TestSQLiteDatabase) GetMovieByID(ctx context.Context, movieID uint) (database.Movie, error) {
	var movie database.Movie
	if err := t.db.WithContext(ctx).First(&movie, movieID).Error; err != nil {
		return database.Movie{}, err
	}
	return movie, nil
}

func (t *TestSQLiteDatabase) GetIncompleteQBittorrentDownloads(ctx context.Context) ([]database.Movie, error) {
	var movies []database.Movie
	if err := t.db.WithContext(ctx).
		Where("qbittorrent_hash != '' AND downloaded_percentage < 100").
		Find(&movies).Error; err != nil {
		return nil, err
	}
	return movies, nil
}

func (t *TestSQLiteDatabase) GetFilesByMovieID(ctx context.Context, movieID uint) ([]database.MovieFile, error) {
	return t.getFiles(ctx, movieID, false)
}

func (t *TestSQLiteDatabase) RemoveFilesByMovieID(ctx context.Context, movieID uint) error {
	return t.db.WithContext(ctx).Where("movie_id = ? AND temp_file = ?", movieID, false).
		Delete(&database.MovieFile{}).Error
}

func (t *TestSQLiteDatabase) ReplaceMainMovieFiles(ctx context.Context, movieID uint, paths []string) error {
	if err := t.RemoveFilesByMovieID(ctx, movieID); err != nil {
		return err
	}
	return t.addFiles(ctx, movieID, paths, false)
}

func (t *TestSQLiteDatabase) UpdateMovieFileSize(ctx context.Context, movieID uint, size int64) error {
	return t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).
		Update("file_size", size).Error
}

func (t *TestSQLiteDatabase) UpdateMovieTotalEpisodes(ctx context.Context, movieID uint, total int) error {
	return t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).
		Update("total_episodes", total).Error
}

func (t *TestSQLiteDatabase) RemoveTempFilesByMovieID(ctx context.Context, movieID uint) error {
	return t.db.WithContext(ctx).Where("movie_id = ? AND temp_file = ?", movieID, true).
		Delete(&database.MovieFile{}).Error
}

func (t *TestSQLiteDatabase) MovieExistsFiles(ctx context.Context, files []string) (bool, error) {
	for _, file := range files {
		var count int64
		if err := t.db.WithContext(ctx).Model(&database.MovieFile{}).Where("file_path = ?", file).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (t *TestSQLiteDatabase) MovieExistsUploadedFile(ctx context.Context, fileName string) (bool, error) {
	var count int64
	if err := t.db.WithContext(ctx).Model(&database.MovieFile{}).
		Where("file_path LIKE ?", "%"+fileName).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (t *TestSQLiteDatabase) GetTempFilesByMovieID(ctx context.Context, movieID uint) ([]database.MovieFile, error) {
	return t.getFiles(ctx, movieID, true)
}

func (t *TestSQLiteDatabase) getFiles(ctx context.Context, movieID uint, temp bool) ([]database.MovieFile, error) {
	var files []database.MovieFile
	if err := t.db.WithContext(ctx).Where("movie_id = ? AND temp_file = ?", movieID, temp).Find(&files).Error; err != nil {
		return nil, err
	}
	return files, nil
}

func (*TestSQLiteDatabase) Login(
	_ context.Context,
	_ string,
	_ int64,
	_ string,
	_ *config.Config,
) (bool, error) {
	return false, nil
}
func (*TestSQLiteDatabase) GetUserRole(_ context.Context, _ int64) (database.UserRole, error) {
	return "guest", nil
}
func (*TestSQLiteDatabase) IsUserAccessAllowed(_ context.Context, _ int64) (isAllowed bool, userRole database.UserRole, err error) {
	return false, "guest", nil
}
func (*TestSQLiteDatabase) AssignTemporaryPassword(_ context.Context, _ string, _ int64) error {
	return nil
}
func (*TestSQLiteDatabase) ExtendTemporaryUser(_ context.Context, _ int64, _ time.Time) error {
	return nil
}
func (*TestSQLiteDatabase) GenerateTemporaryPassword(_ context.Context, _ time.Duration) (string, error) {
	return "", nil
}
func (*TestSQLiteDatabase) GetUserByChatID(_ context.Context, _ int64) (database.User, error) {
	return database.User{}, nil
}

func (*TestSQLiteDatabase) ListAdminChatIDs(_ context.Context) ([]int64, error) { return nil, nil }
func (t *TestSQLiteDatabase) MovieExistsId(ctx context.Context, movieID uint) (bool, error) {
	var count int64
	if err := t.db.WithContext(ctx).Model(&database.Movie{}).Where("id = ?", movieID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// TempDir creates a temporary directory for testing
func TempDir(t *testing.T) string {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "telegram-media-server-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})

	return tempDir
}

// CreateTestTorrent creates a minimal valid torrent file for testing
func CreateTestTorrent(t *testing.T, dir, name string) string {
	t.Helper()

	// Create a simple bencode torrent file
	// This is a minimal valid torrent structure
	nameLen := len(name)
	torrentContent := fmt.Sprintf(
		"d8:announce9:test-url13:creation datei1609459200e4:infod6:lengthi1024e4:name%d:%s"+
			"12:piece lengthi16384e6:pieces20:01234567890123456789ee",
		nameLen,
		name,
	)

	filePath := filepath.Join(dir, name+".torrent")
	if err := os.WriteFile(filePath, []byte(torrentContent), testFileMode); err != nil {
		t.Fatalf("Failed to create test torrent: %v", err)
	}

	return filePath
}

// CreateRealTestTorrent creates a more realistic torrent file using bencode library
func CreateRealTestTorrent(t *testing.T, dir, name string) string {
	t.Helper()

	// Create actual test file content
	testFilePath := filepath.Join(dir, name+".txt")
	testContent := []byte("This is a test file for torrent creation")
	if err := os.WriteFile(testFilePath, testContent, testFileMode); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create torrent metadata
	torrentMeta := map[string]any{
		"announce":      "http://tracker.example.com:8080/announce",
		"creation date": time.Now().Unix(),
		"comment":       "Test torrent created for integration testing",
		"created by":    "telegram-media-server-test",
		"info": map[string]any{
			"name":         name + ".txt",
			"length":       int64(len(testContent)),
			"piece length": 16384,
			"pieces":       "12345678901234567890", // 20-byte SHA1 hash placeholder
		},
	}

	torrentPath := filepath.Join(dir, name+".torrent")
	f, err := os.Create(torrentPath)
	if err != nil {
		t.Fatalf("Failed to create torrent file: %v", err)
	}
	defer f.Close()

	if err := bencode.Marshal(f, torrentMeta); err != nil {
		t.Fatalf("Failed to encode torrent: %v", err)
	}

	return torrentPath
}

// CreateTestDataFile creates a test data file with specified size
func CreateTestDataFile(t *testing.T, dir, name string, size int64) string {
	t.Helper()

	filePath := filepath.Join(dir, name)
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("Failed to create test data file: %v", err)
	}
	defer f.Close()

	// Write test data
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % byteRange)
	}

	if _, err := f.Write(data); err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}

	return filePath
}

// CreateInvalidTorrent creates an invalid torrent file (HTML) for testing
func CreateInvalidTorrent(t *testing.T, dir, name string) string {
	t.Helper()

	htmlContent := `<!DOCTYPE html>
<html>
<head><title>Error</title></head>
<body><h1>File not found</h1></body>
</html>`

	filePath := filepath.Join(dir, name+".torrent")
	if err := os.WriteFile(filePath, []byte(htmlContent), testFileMode); err != nil {
		t.Fatalf("Failed to create invalid torrent: %v", err)
	}

	return filePath
}

// CreateMagnetLink creates a magnet link file for testing
func CreateMagnetLink(t *testing.T, dir, name string) string {
	t.Helper()

	magnetContent := "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678&dn=test"

	filePath := filepath.Join(dir, name+".torrent")
	if err := os.WriteFile(filePath, []byte(magnetContent), testFileMode); err != nil {
		t.Fatalf("Failed to create magnet link file: %v", err)
	}

	return filePath
}

// MockHTTPServer creates a mock HTTP server for testing
func MockHTTPServer(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if response, exists := responses[path]; exists {
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, err := io.WriteString(w, response)
			if err != nil {
				t.Errorf("Failed to write response: %v", err)
			}
		} else {
			http.NotFound(w, r)
		}
	}))

	t.Cleanup(server.Close)
	return server
}

// MockYTDLPServer creates a mock server that responds like a video hosting service
func MockYTDLPServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case strings.Contains(path, "/test-video"):
			// Simulate a simple video page
			w.Header().Set("Content-Type", "text/html")
			_, err := io.WriteString(w, `
<!DOCTYPE html>
<html>
<head><title>Test Video</title></head>
<body>
	<video src="/test-video.mp4"></video>
	<script>
		window.videoData = {
			title: "Test Video",
			duration: 60,
			formats: [
				{url: "/test-video.mp4", quality: "720p"}
			]
		};
	</script>
</body>
</html>`)
			if err != nil {
				t.Errorf("Failed to write response: %v", err)
			}
		case strings.Contains(path, "/test-video.mp4"):
			// Simulate a small video file
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Content-Length", "1024")
			_, err := w.Write(make([]byte, testFileSize))
			if err != nil {
				t.Errorf("Failed to write video response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))

	t.Cleanup(server.Close)
	return server
}

// AssertFileExists checks if a file exists
func AssertFileExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("Expected file %s to exist, but it doesn't", path)
	}
}

// AssertFileNotExists checks if a file doesn't exist
func AssertFileNotExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err == nil {
		t.Errorf("Expected file %s to not exist, but it does", path)
	}
}

// WaitForCondition waits for a condition to be true or times out
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, message string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(tickerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for condition: %s", message)
		case <-ticker.C:
			if condition() {
				return
			}
		}
	}
}

// MockDownloader implements the downloader interface for testing.
// It provides methods for controlling download behavior in tests.
type MockDownloader struct {
	ShouldBlock  bool
	ShouldError  bool
	ErrorMessage string
	Title        string
	Files        []string
	TempFiles    []string
	FileSize     int64
	// EpisodesChan if set is returned from StartDownload; test can send completed episode counts (e.g. 1 for first_episode_ready).
	EpisodesChan chan int
	// TotalEps overrides TotalEpisodes() return value when > 0.
	TotalEps        int
	stoppedManually bool
}

func (m *MockDownloader) GetTitle() (string, error) {
	if m.ShouldError {
		return "", fmt.Errorf("mock title error")
	}
	if m.Title != "" {
		return m.Title, nil
	}
	return "Mock Download", nil
}

func (m *MockDownloader) GetFiles() (mainFiles, tempFiles []string, err error) {
	if m.ShouldError {
		return nil, nil, fmt.Errorf("mock files error")
	}
	mainFiles = m.Files
	if len(mainFiles) == 0 {
		mainFiles = []string{"/tmp/mock_file.mp4"}
	}
	tempFiles = m.TempFiles
	if len(tempFiles) == 0 {
		tempFiles = []string{"/tmp/mock_temp.torrent"}
	}
	return mainFiles, tempFiles, nil
}

func (m *MockDownloader) GetFileSize() (int64, error) {
	if m.ShouldError {
		return 0, fmt.Errorf("mock filesize error")
	}
	if m.FileSize > 0 {
		return m.FileSize, nil
	}
	const defaultFileSize = 1024
	return defaultFileSize, nil
}

func (m *MockDownloader) StoppedManually() bool {
	return m.stoppedManually
}

func (m *MockDownloader) TotalEpisodes() int {
	if m.TotalEps > 0 {
		return m.TotalEps
	}
	if m.EpisodesChan != nil {
		return 1
	}
	return 0
}

func (m *MockDownloader) StartDownload(
	ctx context.Context,
) (progressChan chan float64, errChan chan error, episodesChan <-chan int, err error) {
	progressChan = make(chan float64, 1)
	errChan = make(chan error, 1)

	if m.ShouldError && m.ErrorMessage != "" {
		return nil, nil, nil, fmt.Errorf("%s", m.ErrorMessage)
	}

	var epCh <-chan int
	if m.EpisodesChan != nil {
		epCh = m.EpisodesChan
	}

	go func() {
		defer close(progressChan)
		defer close(errChan)

		if m.ShouldError {
			errChan <- fmt.Errorf("mock download error")
			return
		}

		if m.ShouldBlock {
			<-ctx.Done()
			errChan <- ctx.Err()
			return
		}

		progressChan <- 100.0
		errChan <- nil
	}()

	return progressChan, errChan, epCh, nil
}

func (m *MockDownloader) StopDownload() error {
	m.stoppedManually = true
	return nil
}
