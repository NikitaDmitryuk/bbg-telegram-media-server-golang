package testutils

import (
	"context"
	"time"

	tmsconfig "github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/database"
)

// DatabaseStub implements database.Database with no-op methods.
// Embed it in test-specific mocks and override only the methods you need.
type DatabaseStub struct{}

// MovieReader methods.

func (*DatabaseStub) GetMovieList(_ context.Context) ([]database.Movie, error) {
	return nil, nil
}

func (*DatabaseStub) GetMovieByID(_ context.Context, _ uint) (database.Movie, error) {
	return database.Movie{}, nil
}

func (*DatabaseStub) GetFilesByMovieID(_ context.Context, _ uint) ([]database.MovieFile, error) {
	return nil, nil
}

func (*DatabaseStub) GetTempFilesByMovieID(_ context.Context, _ uint) ([]database.MovieFile, error) {
	return nil, nil
}

func (*DatabaseStub) MovieExistsId(_ context.Context, _ uint) (bool, error) {
	return false, nil
}

func (*DatabaseStub) MovieExistsFiles(_ context.Context, _ []string) (bool, error) {
	return false, nil
}

func (*DatabaseStub) MovieExistsUploadedFile(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (*DatabaseStub) GetIncompleteQBittorrentDownloads(_ context.Context) ([]database.Movie, error) {
	return nil, nil
}

// MovieWriter methods.

func (*DatabaseStub) AddMovie(_ context.Context, _ string, _ int64, _, _ []string, _ int) (uint, error) {
	return 0, nil
}

func (*DatabaseStub) UpdateMovieName(_ context.Context, _ uint, _ string) error { return nil }

func (*DatabaseStub) UpdateEpisodesProgress(_ context.Context, _ uint, _ int) error { return nil }

func (*DatabaseStub) UpdateDownloadedPercentage(_ context.Context, _ uint, _ int) error {
	return nil
}

func (*DatabaseStub) SetLoaded(_ context.Context, _ uint, _ string) error { return nil }

func (*DatabaseStub) RefreshMovieFileSizeFromDisk(_ context.Context, _ uint, _ string) (int64, error) {
	return 0, nil
}

func (*DatabaseStub) RemoveMovie(_ context.Context, _ uint) error { return nil }

func (*DatabaseStub) UpdateConversionStatus(_ context.Context, _ uint, _ string) error {
	return nil
}

func (*DatabaseStub) UpdateConversionPercentage(_ context.Context, _ uint, _ int) error {
	return nil
}

func (*DatabaseStub) SetTvCompatibility(_ context.Context, _ uint, _ string) error { return nil }

func (*DatabaseStub) SetQBittorrentHash(_ context.Context, _ uint, _ string) error { return nil }

func (*DatabaseStub) RemoveFilesByMovieID(_ context.Context, _ uint) error { return nil }

func (*DatabaseStub) ReplaceMainMovieFiles(_ context.Context, _ uint, _ []string) error { return nil }

func (*DatabaseStub) UpdateMovieFileSize(_ context.Context, _ uint, _ int64) error { return nil }

func (*DatabaseStub) UpdateMovieTotalEpisodes(_ context.Context, _ uint, _ int) error { return nil }

func (*DatabaseStub) RemoveTempFilesByMovieID(_ context.Context, _ uint) error { return nil }

// AuthStore methods.

func (*DatabaseStub) Login(_ context.Context, _ string, _ int64, _ string, _ *tmsconfig.Config) (bool, error) {
	return false, nil
}

func (*DatabaseStub) GetUserRole(_ context.Context, _ int64) (database.UserRole, error) {
	return "", nil
}

func (*DatabaseStub) IsUserAccessAllowed(_ context.Context, _ int64) (allowed bool, role database.UserRole, err error) {
	return false, "", nil
}

func (*DatabaseStub) AssignTemporaryPassword(_ context.Context, _ string, _ int64) error {
	return nil
}

func (*DatabaseStub) ExtendTemporaryUser(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (*DatabaseStub) GenerateTemporaryPassword(_ context.Context, _ time.Duration) (string, error) {
	return "", nil
}

func (*DatabaseStub) GetUserByChatID(_ context.Context, _ int64) (database.User, error) {
	return database.User{}, nil
}

func (*DatabaseStub) ListAdminChatIDs(_ context.Context) ([]int64, error) { return nil, nil }

// Init method.

func (*DatabaseStub) Init(_ *tmsconfig.Config) error { return nil }
