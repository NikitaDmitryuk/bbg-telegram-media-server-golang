package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gorm.io/gorm"
)

func (s *SQLiteDatabase) AddMovie(
	ctx context.Context,
	name string,
	fileSize int64,
	mainFiles, tempFiles []string,
	totalEpisodes int,
) (uint, error) {
	var movieID uint
	err := s.withRetry(ctx, "AddMovie", func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			movie := Movie{
				Name:                 name,
				FileSize:             fileSize,
				DownloadedPercentage: 0,
				TotalEpisodes:        totalEpisodes,
			}

			if err := tx.Create(&movie).Error; err != nil {
				return err
			}

			if err := addFilesWithDB(tx, movie.ID, mainFiles, false); err != nil {
				return err
			}

			if err := addFilesWithDB(tx, movie.ID, tempFiles, true); err != nil {
				return err
			}

			movieID = movie.ID
			return nil
		})
	})
	if err != nil {
		return 0, err
	}
	return movieID, nil
}

func (s *SQLiteDatabase) UpdateMovieName(ctx context.Context, movieID uint, name string) error {
	return s.withRetry(ctx, "UpdateMovieName", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).Where("id = ?", movieID).Update("name", name).Error
	})
}

func (s *SQLiteDatabase) UpdateMovieFileSize(ctx context.Context, movieID uint, size int64) error {
	return s.withRetry(ctx, "UpdateMovieFileSize", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).Where("id = ?", movieID).Update("file_size", size).Error
	})
}

func (s *SQLiteDatabase) UpdateMovieTotalEpisodes(ctx context.Context, movieID uint, total int) error {
	return s.withRetry(ctx, "UpdateMovieTotalEpisodes", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).Where("id = ?", movieID).Update("total_episodes", total).Error
	})
}

func (s *SQLiteDatabase) RemoveMovie(ctx context.Context, movieID uint) error {
	return s.withRetry(ctx, "RemoveMovie", func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("movie_id = ?", movieID).Delete(&MovieFile{}).Error; err != nil {
				return err
			}
			return tx.Delete(&Movie{}, movieID).Error
		})
	})
}

func (s *SQLiteDatabase) GetMovieList(ctx context.Context) ([]Movie, error) {
	var movies []Movie
	if err := s.withRetry(ctx, "GetMovieList", func() error {
		return s.db.WithContext(ctx).Find(&movies).Error
	}); err != nil {
		return nil, err
	}
	return movies, nil
}

func (s *SQLiteDatabase) UpdateDownloadedPercentage(ctx context.Context, movieID uint, percentage int) error {
	return s.withRetry(ctx, "UpdateDownloadedPercentage", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).
			Where("id = ? AND downloaded_percentage <> ?", movieID, percentage).
			Update("downloaded_percentage", percentage).Error
	})
}

func (s *SQLiteDatabase) TorrentStallNotified(ctx context.Context, movieID uint) (bool, error) {
	var notified bool
	err := s.withRetry(ctx, "TorrentStallNotified", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).Where("id = ?", movieID).
			Pluck("torrent_stall_notified", &notified).Error
	})
	return notified, err
}

func (s *SQLiteDatabase) SetTorrentStallNotified(ctx context.Context, movieID uint, notified bool) error {
	return s.withRetry(ctx, "SetTorrentStallNotified", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).Where("id = ?", movieID).
			Update("torrent_stall_notified", notified).Error
	})
}

func (s *SQLiteDatabase) UpdateEpisodesProgress(ctx context.Context, movieID uint, completedEpisodes int) error {
	return s.withRetry(ctx, "UpdateEpisodesProgress", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).
			Where("id = ? AND completed_episodes <> ?", movieID, completedEpisodes).
			Update("completed_episodes", completedEpisodes).Error
	})
}

func (s *SQLiteDatabase) SetLoaded(ctx context.Context, movieID uint, movieRoot string) error {
	const completePercentage = 100
	updates := map[string]any{"downloaded_percentage": completePercentage}
	if sum, err := s.sumMainFilesSizeOnDisk(ctx, movieID, movieRoot); err == nil && sum > 0 {
		updates["file_size"] = sum
	}
	return s.withRetry(ctx, "SetLoaded", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).Where("id = ?", movieID).Updates(updates).Error
	})
}

// RefreshMovieFileSizeFromDisk updates file_size from actual main files on disk (e.g. magnet had unknown size at add time).
func (s *SQLiteDatabase) RefreshMovieFileSizeFromDisk(ctx context.Context, movieID uint, movieRoot string) (int64, error) {
	sum, err := s.sumMainFilesSizeOnDisk(ctx, movieID, movieRoot)
	if err != nil || sum <= 0 {
		return sum, err
	}
	if err := s.withRetry(ctx, "RefreshMovieFileSizeFromDisk", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).Where("id = ?", movieID).Update("file_size", sum).Error
	}); err != nil {
		return 0, err
	}
	return sum, nil
}

func (s *SQLiteDatabase) sumMainFilesSizeOnDisk(ctx context.Context, movieID uint, movieRoot string) (int64, error) {
	if movieRoot == "" {
		return 0, nil
	}
	files, err := s.GetFilesByMovieID(ctx, movieID)
	if err != nil {
		return 0, err
	}
	var sum int64
	for i := range files {
		p := filepath.Join(movieRoot, files[i].FilePath)
		fi, statErr := os.Stat(p)
		if statErr != nil {
			continue
		}
		sum += fi.Size()
	}
	return sum, nil
}

func (s *SQLiteDatabase) UpdateConversionStatus(ctx context.Context, movieID uint, status string) error {
	return s.withRetry(ctx, "UpdateConversionStatus", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).
			Where("id = ? AND conversion_status <> ?", movieID, status).
			Update("conversion_status", status).Error
	})
}

func (s *SQLiteDatabase) UpdateConversionPercentage(ctx context.Context, movieID uint, percentage int) error {
	return s.withRetry(ctx, "UpdateConversionPercentage", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).
			Where("id = ? AND conversion_percentage <> ?", movieID, percentage).
			Update("conversion_percentage", percentage).Error
	})
}

func (s *SQLiteDatabase) SetTvCompatibility(ctx context.Context, movieID uint, compat string) error {
	return s.withRetry(ctx, "SetTvCompatibility", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).
			Where("id = ? AND tv_compatibility <> ?", movieID, compat).
			Update("tv_compatibility", compat).Error
	})
}

func (s *SQLiteDatabase) SetQBittorrentHash(ctx context.Context, movieID uint, hash string) error {
	return s.withRetry(ctx, "SetQBittorrentHash", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).
			Where("id = ? AND qbittorrent_hash <> ?", movieID, hash).
			Update("qbittorrent_hash", hash).Error
	})
}

func (s *SQLiteDatabase) GetMovieByID(ctx context.Context, movieID uint) (Movie, error) {
	var movie Movie
	if err := s.withRetry(ctx, "GetMovieByID", func() error {
		return s.db.WithContext(ctx).First(&movie, movieID).Error
	}); err != nil {
		return Movie{}, err
	}
	return movie, nil
}

func (s *SQLiteDatabase) GetIncompleteQBittorrentDownloads(ctx context.Context) ([]Movie, error) {
	var movies []Movie
	if err := s.withRetry(ctx, "GetIncompleteQBittorrentDownloads", func() error {
		return s.db.WithContext(ctx).
			Where("qbittorrent_hash != '' AND downloaded_percentage < 100").
			Find(&movies).Error
	}); err != nil {
		return nil, err
	}
	return movies, nil
}

func (s *SQLiteDatabase) MovieExistsFiles(ctx context.Context, files []string) (bool, error) {
	for _, file := range files {
		var count int64
		if err := s.withRetry(ctx, "MovieExistsFiles", func() error {
			return s.db.WithContext(ctx).Model(&MovieFile{}).Where("file_path = ?", file).Count(&count).Error
		}); err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *SQLiteDatabase) MovieExistsId(ctx context.Context, movieID uint) (bool, error) {
	var count int64
	if err := s.withRetry(ctx, "MovieExistsId", func() error {
		return s.db.WithContext(ctx).Model(&Movie{}).Where("id = ?", movieID).Count(&count).Error
	}); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *SQLiteDatabase) MovieExistsUploadedFile(ctx context.Context, fileName string) (bool, error) {
	var count int64
	if err := s.withRetry(ctx, "MovieExistsUploadedFile", func() error {
		return s.db.WithContext(ctx).Model(&MovieFile{}).Where("file_path LIKE ?", fmt.Sprintf("%%%s", fileName)).Count(&count).Error
	}); err != nil {
		return false, err
	}
	return count > 0, nil
}
