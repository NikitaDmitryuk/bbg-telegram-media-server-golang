package database

import (
	"context"
	"time"

	tmsconfig "github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
)

// MovieReader is the read-only subset of movie/file data. Use in handlers that only list or read movies.
type MovieReader interface {
	GetMovieList(ctx context.Context) ([]Movie, error)
	GetMovieByID(ctx context.Context, movieID uint) (Movie, error)
	GetFilesByMovieID(ctx context.Context, movieID uint) ([]MovieFile, error)
	GetTempFilesByMovieID(ctx context.Context, movieID uint) ([]MovieFile, error)
	MovieExistsId(ctx context.Context, movieID uint) (bool, error)
	MovieExistsFiles(ctx context.Context, files []string) (bool, error)
	MovieExistsUploadedFile(ctx context.Context, fileName string) (bool, error)
	GetIncompleteQBittorrentDownloads(ctx context.Context) ([]Movie, error)
}

// MovieWriter is the write subset for movies and files. Use together with MovieReader where both are needed.
type MovieWriter interface {
	AddMovie(ctx context.Context, name string, fileSize int64, mainFiles, tempFiles []string, totalEpisodes int) (uint, error)
	UpdateMovieName(ctx context.Context, movieID uint, name string) error
	UpdateEpisodesProgress(ctx context.Context, movieID uint, completedEpisodes int) error
	UpdateDownloadedPercentage(ctx context.Context, movieID uint, percentage int) error
	SetLoaded(ctx context.Context, movieID uint, movieRoot string) error
	// RefreshMovieFileSizeFromDisk sums sizes of main (non-temp) files on disk and updates movie.file_size when sum > 0.
	RefreshMovieFileSizeFromDisk(ctx context.Context, movieID uint, movieRoot string) (int64, error)
	RemoveMovie(ctx context.Context, movieID uint) error
	UpdateConversionStatus(ctx context.Context, movieID uint, status string) error
	UpdateConversionPercentage(ctx context.Context, movieID uint, percentage int) error
	SetTvCompatibility(ctx context.Context, movieID uint, compat string) error
	SetQBittorrentHash(ctx context.Context, movieID uint, hash string) error
	RemoveFilesByMovieID(ctx context.Context, movieID uint) error
	// ReplaceMainMovieFiles removes non-temp file rows and inserts new paths (e.g. after magnet metadata from qBittorrent).
	ReplaceMainMovieFiles(ctx context.Context, movieID uint, paths []string) error
	UpdateMovieFileSize(ctx context.Context, movieID uint, size int64) error
	UpdateMovieTotalEpisodes(ctx context.Context, movieID uint, total int) error
	RemoveTempFilesByMovieID(ctx context.Context, movieID uint) error
}

// AuthStore is the subset for authentication and user management. Use in auth handlers and middleware.
type AuthStore interface {
	Login(ctx context.Context, password string, chatID int64, userName string, config *tmsconfig.Config) (bool, error)
	GetUserRole(ctx context.Context, chatID int64) (UserRole, error)
	IsUserAccessAllowed(ctx context.Context, chatID int64) (bool, UserRole, error)
	AssignTemporaryPassword(ctx context.Context, password string, chatID int64) error
	ExtendTemporaryUser(ctx context.Context, chatID int64, newExpiration time.Time) error
	GenerateTemporaryPassword(ctx context.Context, duration time.Duration) (string, error)
	GetUserByChatID(ctx context.Context, chatID int64) (User, error)
	ListAdminChatIDs(ctx context.Context) ([]int64, error)
}

// Database is the full storage interface. Embed MovieReader, MovieWriter, AuthStore and Init for backward compatibility.
type Database interface {
	Init(config *tmsconfig.Config) error
	MovieReader
	MovieWriter
	AuthStore
}

// TorrentStallStore is an optional persistence capability used by the download
// manager. Keeping it separate avoids widening lightweight test and API stores.
type TorrentStallStore interface {
	TorrentStallNotified(ctx context.Context, movieID uint) (bool, error)
	SetTorrentStallNotified(ctx context.Context, movieID uint, notified bool) error
}

func NewDatabase(config *tmsconfig.Config) (Database, error) {
	database := NewSQLiteDatabase()
	if err := database.Init(config); err != nil {
		logutils.Log.WithError(err).Error("Failed to initialize the database")
		return nil, err
	}

	logutils.Log.Info("Database initialized successfully")
	return database, nil
}
