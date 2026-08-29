package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	tmsconfig "github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"gorm.io/gorm"
)

const (
	guestRole = "guest"
)

func (s *SQLiteDatabase) Login(
	ctx context.Context,
	password string,
	chatID int64,
	userName string,
	config *tmsconfig.Config,
) (bool, error) {
	if password == config.AdminPassword {
		_, err := s.createOrUpdateUser(ctx, chatID, userName, "admin", nil)
		return true, err
	}
	if password == config.RegularPassword {
		_, err := s.createOrUpdateUser(ctx, chatID, userName, "regular", nil)
		return true, err
	}
	return s.handleTemporaryPassword(ctx, password, chatID, userName)
}

func (s *SQLiteDatabase) handleTemporaryPassword(ctx context.Context, password string, chatID int64, userName string) (bool, error) {
	var passwords []TemporaryPassword
	if err := s.withRetry(ctx, "handleTemporaryPassword.Find", func() error {
		return s.db.WithContext(ctx).Where("password = ?", password).Find(&passwords).Error
	}); err != nil {
		return false, err
	}

	if !isTemporaryPasswordValid(passwords) {
		return false, nil
	}

	tempPassword := passwords[0]
	expiresAt := tempPassword.ExpiresAt
	return s.createOrUpdateUser(ctx, chatID, userName, "temporary", &expiresAt)
}

func (s *SQLiteDatabase) createOrUpdateUser(
	ctx context.Context,
	chatID int64,
	userName string,
	role UserRole,
	expiresAt *time.Time,
) (bool, error) {
	var user User
	err := s.withRetry(ctx, "createOrUpdateUser", func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			resultErr := tx.Where("chat_id = ?", chatID).First(&user).Error
			if resultErr != nil && !errors.Is(resultErr, gorm.ErrRecordNotFound) {
				return resultErr
			}

			if errors.Is(resultErr, gorm.ErrRecordNotFound) {
				user = User{
					Name:      userName,
					ChatID:    chatID,
					Role:      role,
					ExpiresAt: expiresAt,
				}
				return tx.Create(&user).Error
			}

			user.Name = userName
			user.Role = role
			user.ExpiresAt = expiresAt
			return tx.Save(&user).Error
		})
	})
	return err == nil, err
}

func (s *SQLiteDatabase) GetUserRole(ctx context.Context, chatID int64) (UserRole, error) {
	var user User
	err := s.withRetry(ctx, "GetUserRole", func() error {
		return s.db.WithContext(ctx).Where("chat_id = ?", chatID).First(&user).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return guestRole, nil
		}
		return guestRole, err
	}

	if user.IsExpired() {
		return guestRole, nil
	}

	return user.Role, nil
}

func (s *SQLiteDatabase) IsUserAccessAllowed(ctx context.Context, chatID int64) (isAllowed bool, userRole UserRole, err error) {
	var user User
	err = s.withRetry(ctx, "IsUserAccessAllowed", func() error {
		return s.db.WithContext(ctx).Where("chat_id = ?", chatID).First(&user).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, guestRole, nil
		}
		return false, guestRole, err
	}

	if user.IsExpired() {
		return false, guestRole, nil
	}

	return true, user.Role, nil
}

func (s *SQLiteDatabase) ListAdminChatIDs(ctx context.Context) ([]int64, error) {
	var chatIDs []int64
	err := s.withRetry(ctx, "ListAdminChatIDs", func() error {
		return s.db.WithContext(ctx).
			Model(&User{}).
			Where("role = ? AND (expires_at IS NULL OR expires_at > ?)", AdminRole, time.Now()).
			Pluck("chat_id", &chatIDs).Error
	})
	return chatIDs, err
}

func isTemporaryPasswordValid(passwords []TemporaryPassword) bool {
	if len(passwords) == 0 {
		return false
	}
	tempPassword := passwords[0]
	return !tempPassword.IsExpired()
}

func (s *SQLiteDatabase) AssignTemporaryPassword(ctx context.Context, password string, chatID int64) error {
	return s.withRetry(ctx, "AssignTemporaryPassword", func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var tempPassword TemporaryPassword
			result := tx.Where("password = ?", password).First(&tempPassword)
			if result.Error != nil {
				return result.Error
			}

			var user User
			result = tx.Where("chat_id = ?", chatID).First(&user)
			if result.Error != nil {
				if errors.Is(result.Error, gorm.ErrRecordNotFound) {
					return fmt.Errorf("user not found")
				}
				return result.Error
			}

			return tx.Model(&tempPassword).Association("Users").Append(&user)
		})
	})
}

func (s *SQLiteDatabase) ExtendTemporaryUser(ctx context.Context, chatID int64, newExpiration time.Time) error {
	var user User
	return s.withRetry(ctx, "ExtendTemporaryUser", func() error {
		result := s.db.WithContext(ctx).Where("chat_id = ?", chatID).First(&user)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return fmt.Errorf("user not found")
			}
			return result.Error
		}

		if user.Role != "temporary" {
			return fmt.Errorf("user is not temporary")
		}

		user.ExpiresAt = &newExpiration
		return s.db.WithContext(ctx).Save(&user).Error
	})
}

func (s *SQLiteDatabase) GenerateTemporaryPassword(ctx context.Context, duration time.Duration) (string, error) {
	const passwordLength = 8
	bytes := make([]byte, passwordLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	password := hex.EncodeToString(bytes)

	expiresAt := time.Now().Add(duration)
	tempPassword := TemporaryPassword{
		Password:  password,
		ExpiresAt: expiresAt,
	}

	if err := s.withRetry(ctx, "GenerateTemporaryPassword", func() error {
		return s.db.WithContext(ctx).Create(&tempPassword).Error
	}); err != nil {
		return "", err
	}

	return password, nil
}

func (s *SQLiteDatabase) GetUserByChatID(ctx context.Context, chatID int64) (User, error) {
	var user User
	if err := s.withRetry(ctx, "GetUserByChatID", func() error {
		return s.db.WithContext(ctx).Where("chat_id = ?", chatID).First(&user).Error
	}); err != nil {
		return User{}, err
	}
	return user, nil
}
