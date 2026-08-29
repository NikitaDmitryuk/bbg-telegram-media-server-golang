package database

import (
	"context"
	"testing"
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *SQLiteDatabase {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}

	// Auto migrate the schema
	err = db.AutoMigrate(&User{}, &TemporaryPassword{})
	if err != nil {
		t.Fatalf("Failed to migrate test database: %v", err)
	}

	return &SQLiteDatabase{db: db}
}

func TestSQLiteDatabase_ListAdminChatIDs(t *testing.T) {
	db := setupTestDB(t)
	defer closeTestDB(db)
	expired := time.Now().Add(-time.Hour)
	users := []User{
		{Name: "admin", ChatID: 10, Role: AdminRole},
		{Name: "regular", ChatID: 20, Role: RegularRole},
		{Name: "expired-admin", ChatID: 30, Role: AdminRole, ExpiresAt: &expired},
	}
	if err := db.db.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}
	chatIDs, err := db.ListAdminChatIDs(context.Background())
	if err != nil {
		t.Fatalf("list admins: %v", err)
	}
	if len(chatIDs) != 1 || chatIDs[0] != 10 {
		t.Fatalf("admin chat ids=%v, want [10]", chatIDs)
	}
}

func closeTestDB(db *SQLiteDatabase) {
	if sqlDB, err := db.db.DB(); err == nil {
		sqlDB.Close()
	}
}

func TestSQLiteDatabase_Login(t *testing.T) {
	tests := []struct {
		name           string
		password       string
		chatID         int64
		userName       string
		config         *config.Config
		expectedResult bool
		expectError    bool
		setupDB        func(*SQLiteDatabase)
	}{
		{
			name:     "successful admin login",
			password: "admin123",
			chatID:   12345,
			userName: "admin_user",
			config: &config.Config{
				AdminPassword:   "admin123",
				RegularPassword: "regular456",
			},
			expectedResult: true,
			expectError:    false,
		},
		{
			name:     "successful regular login",
			password: "regular456",
			chatID:   12345,
			userName: "regular_user",
			config: &config.Config{
				AdminPassword:   "admin123",
				RegularPassword: "regular456",
			},
			expectedResult: true,
			expectError:    false,
		},
		{
			name:     "successful temporary password login",
			password: "temp123456",
			chatID:   12345,
			userName: "temp_user",
			config: &config.Config{
				AdminPassword:   "admin123",
				RegularPassword: "regular456",
			},
			expectedResult: true,
			expectError:    false,
			setupDB: func(db *SQLiteDatabase) {
				// Create a valid temporary password
				tempPass := TemporaryPassword{
					Password:  "temp123456",
					ExpiresAt: time.Now().Add(time.Hour),
				}
				db.db.Create(&tempPass)
			},
		},
		{
			name:     "failed login - wrong password",
			password: "wrongpassword",
			chatID:   12345,
			userName: "user",
			config: &config.Config{
				AdminPassword:   "admin123",
				RegularPassword: "regular456",
			},
			expectedResult: false,
			expectError:    false,
		},
		{
			name:     "failed login - expired temporary password",
			password: "expired123",
			chatID:   12345,
			userName: "user",
			config: &config.Config{
				AdminPassword:   "admin123",
				RegularPassword: "regular456",
			},
			expectedResult: false,
			expectError:    false,
			setupDB: func(db *SQLiteDatabase) {
				// Create an expired temporary password
				tempPass := TemporaryPassword{
					Password:  "expired123",
					ExpiresAt: time.Now().Add(-time.Hour),
				}
				db.db.Create(&tempPass)
			},
		},
		{
			name:     "update existing user role",
			password: "admin123",
			chatID:   12345,
			userName: "existing_user",
			config: &config.Config{
				AdminPassword:   "admin123",
				RegularPassword: "regular456",
			},
			expectedResult: true,
			expectError:    false,
			setupDB: func(db *SQLiteDatabase) {
				// Create existing user with regular role
				user := User{
					ChatID: 12345,
					Name:   "existing_user",
					Role:   "regular",
				}
				db.db.Create(&user)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			defer closeTestDB(db)

			if tt.setupDB != nil {
				tt.setupDB(db)
			}

			result, err := db.Login(context.Background(), tt.password, tt.chatID, tt.userName, tt.config)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if result != tt.expectedResult {
				t.Errorf("Expected result %v, got %v", tt.expectedResult, result)
			}

			// Verify user was created/updated correctly
			if tt.expectedResult && !tt.expectError {
				var user User
				err := db.db.Where("chat_id = ?", tt.chatID).First(&user).Error
				if err != nil {
					t.Errorf("Failed to find created/updated user: %v", err)
				} else {
					if user.Name != tt.userName {
						t.Errorf("Expected username %s, got %s", tt.userName, user.Name)
					}

					var expectedRole string
					switch tt.password {
					case tt.config.AdminPassword:
						expectedRole = "admin"
					case tt.config.RegularPassword:
						expectedRole = "regular"
					default:
						expectedRole = "temporary"
					}

					if string(user.Role) != expectedRole {
						t.Errorf("Expected role %s, got %s", expectedRole, user.Role)
					}
				}
			}
		})
	}
}

func TestSQLiteDatabase_GetUserRole(t *testing.T) {
	tests := []struct {
		name         string
		chatID       int64
		setupDB      func(*SQLiteDatabase)
		expectedRole UserRole
		expectError  bool
	}{
		{
			name:         "user not found",
			chatID:       12345,
			expectedRole: "guest",
			expectError:  false,
		},
		{
			name:   "active admin user",
			chatID: 12345,
			setupDB: func(db *SQLiteDatabase) {
				user := User{
					ChatID: 12345,
					Name:   "admin",
					Role:   "admin",
				}
				db.db.Create(&user)
			},
			expectedRole: "admin",
			expectError:  false,
		},
		{
			name:   "active regular user",
			chatID: 12345,
			setupDB: func(db *SQLiteDatabase) {
				user := User{
					ChatID: 12345,
					Name:   "regular",
					Role:   "regular",
				}
				db.db.Create(&user)
			},
			expectedRole: "regular",
			expectError:  false,
		},
		{
			name:   "expired temporary user",
			chatID: 12345,
			setupDB: func(db *SQLiteDatabase) {
				expiredTime := time.Now().Add(-time.Hour)
				user := User{
					ChatID:    12345,
					Name:      "temp",
					Role:      "temporary",
					ExpiresAt: &expiredTime,
				}
				db.db.Create(&user)
			},
			expectedRole: "guest",
			expectError:  false,
		},
		{
			name:   "active temporary user",
			chatID: 12345,
			setupDB: func(db *SQLiteDatabase) {
				futureTime := time.Now().Add(time.Hour)
				user := User{
					ChatID:    12345,
					Name:      "temp",
					Role:      "temporary",
					ExpiresAt: &futureTime,
				}
				db.db.Create(&user)
			},
			expectedRole: "temporary",
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			defer closeTestDB(db)

			if tt.setupDB != nil {
				tt.setupDB(db)
			}

			role, err := db.GetUserRole(context.Background(), tt.chatID)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if role != tt.expectedRole {
				t.Errorf("Expected role %s, got %s", tt.expectedRole, role)
			}
		})
	}
}

func TestSQLiteDatabase_IsUserAccessAllowed(t *testing.T) {
	tests := []struct {
		name            string
		chatID          int64
		setupDB         func(*SQLiteDatabase)
		expectedAllowed bool
		expectedRole    UserRole
		expectError     bool
	}{
		{
			name:            "user not found",
			chatID:          12345,
			expectedAllowed: false,
			expectedRole:    "guest",
			expectError:     false,
		},
		{
			name:   "active admin user",
			chatID: 12345,
			setupDB: func(db *SQLiteDatabase) {
				user := User{
					ChatID: 12345,
					Name:   "admin",
					Role:   "admin",
				}
				db.db.Create(&user)
			},
			expectedAllowed: true,
			expectedRole:    "admin",
			expectError:     false,
		},
		{
			name:   "expired user",
			chatID: 12345,
			setupDB: func(db *SQLiteDatabase) {
				expiredTime := time.Now().Add(-time.Hour)
				user := User{
					ChatID:    12345,
					Name:      "expired",
					Role:      "temporary",
					ExpiresAt: &expiredTime,
				}
				db.db.Create(&user)
			},
			expectedAllowed: false,
			expectedRole:    "guest",
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			defer closeTestDB(db)

			if tt.setupDB != nil {
				tt.setupDB(db)
			}

			allowed, role, err := db.IsUserAccessAllowed(context.Background(), tt.chatID)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if allowed != tt.expectedAllowed {
				t.Errorf("Expected allowed %v, got %v", tt.expectedAllowed, allowed)
			}
			if role != tt.expectedRole {
				t.Errorf("Expected role %s, got %s", tt.expectedRole, role)
			}
		})
	}
}

func TestSQLiteDatabase_GenerateTemporaryPassword(t *testing.T) {
	tests := []struct {
		name         string
		duration     time.Duration
		expectError  bool
		validateFunc func(string, *SQLiteDatabase) error
	}{
		{
			name:        "generate 1 hour password",
			duration:    time.Hour,
			expectError: false,
			validateFunc: func(password string, _ *SQLiteDatabase) error {
				if password == "" {
					t.Error("Generated password is empty")
				}

				// For now, we'll just verify the password is not empty
				// In a real implementation, we'd add a method to check if password exists
				if password == "" {
					t.Error("Generated password is empty")
				}

				return nil
			},
		},
		{
			name:        "generate 24 hour password",
			duration:    24 * time.Hour,
			expectError: false,
			validateFunc: func(password string, _ *SQLiteDatabase) error {
				if password == "" {
					t.Error("Generated password is empty")
				}
				return nil
			},
		},
		{
			name:        "generate very short duration password",
			duration:    time.Minute,
			expectError: false,
			validateFunc: func(password string, _ *SQLiteDatabase) error {
				if password == "" {
					t.Error("Generated password is empty")
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			defer closeTestDB(db)

			password, err := db.GenerateTemporaryPassword(context.Background(), tt.duration)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectError && tt.validateFunc != nil {
				_ = tt.validateFunc(password, db)
			}
		})
	}
}

func TestSQLiteDatabase_ExtendTemporaryUser(t *testing.T) {
	tests := []struct {
		name          string
		chatID        int64
		newExpiration time.Time
		setupDB       func(*SQLiteDatabase)
		expectError   bool
		errorContains string
	}{
		{
			name:          "user not found",
			chatID:        12345,
			newExpiration: time.Now().Add(time.Hour),
			expectError:   true,
			errorContains: "user not found",
		},
		{
			name:          "extend temporary user",
			chatID:        12345,
			newExpiration: time.Now().Add(2 * time.Hour),
			setupDB: func(db *SQLiteDatabase) {
				expiry := time.Now().Add(time.Hour)
				user := User{
					ChatID:    12345,
					Name:      "temp_user",
					Role:      "temporary",
					ExpiresAt: &expiry,
				}
				db.db.Create(&user)
			},
			expectError: false,
		},
		{
			name:          "try to extend non-temporary user",
			chatID:        12345,
			newExpiration: time.Now().Add(time.Hour),
			setupDB: func(db *SQLiteDatabase) {
				user := User{
					ChatID: 12345,
					Name:   "regular_user",
					Role:   "regular",
				}
				db.db.Create(&user)
			},
			expectError:   true,
			errorContains: "user is not temporary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			defer closeTestDB(db)

			if tt.setupDB != nil {
				tt.setupDB(db)
			}

			err := db.ExtendTemporaryUser(context.Background(), tt.chatID, tt.newExpiration)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if tt.expectError && tt.errorContains != "" && err != nil {
				if err.Error() != tt.errorContains {
					t.Errorf("Expected error to contain %q, got %q", tt.errorContains, err.Error())
				}
			}

			// Verify extension worked if no error expected
			if !tt.expectError {
				var user User
				err := db.db.Where("chat_id = ?", tt.chatID).First(&user).Error
				if err != nil {
					t.Errorf("Failed to find extended user: %v", err)
				} else if user.ExpiresAt == nil {
					t.Error("User expiration time is nil after extension")
				} else if !user.ExpiresAt.Equal(tt.newExpiration) {
					t.Errorf("Expected expiration %v, got %v", tt.newExpiration, *user.ExpiresAt)
				}
			}
		})
	}
}

func TestSQLiteDatabase_GetUserByChatID(t *testing.T) {
	tests := []struct {
		name        string
		chatID      int64
		setupDB     func(*SQLiteDatabase)
		expectError bool
		expectUser  bool
	}{
		{
			name:        "user not found",
			chatID:      12345,
			expectError: true,
			expectUser:  false,
		},
		{
			name:   "user found",
			chatID: 12345,
			setupDB: func(db *SQLiteDatabase) {
				user := User{
					ChatID: 12345,
					Name:   "test_user",
					Role:   "regular",
				}
				db.db.Create(&user)
			},
			expectError: false,
			expectUser:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			defer closeTestDB(db)

			if tt.setupDB != nil {
				tt.setupDB(db)
			}

			user, err := db.GetUserByChatID(context.Background(), tt.chatID)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if tt.expectUser && user.ChatID != tt.chatID {
				t.Errorf("Expected user with chatID %d, got %d", tt.chatID, user.ChatID)
			}
		})
	}
}

func TestIsTemporaryPasswordValid(t *testing.T) {
	tests := []struct {
		name      string
		passwords []TemporaryPassword
		expected  bool
	}{
		{
			name:      "empty passwords slice",
			passwords: []TemporaryPassword{},
			expected:  false,
		},
		{
			name: "valid password",
			passwords: []TemporaryPassword{
				{
					Password:  "valid123",
					ExpiresAt: time.Now().Add(time.Hour),
				},
			},
			expected: true,
		},
		{
			name: "expired password",
			passwords: []TemporaryPassword{
				{
					Password:  "expired123",
					ExpiresAt: time.Now().Add(-time.Hour),
				},
			},
			expected: false,
		},
		{
			name: "multiple passwords - first is valid",
			passwords: []TemporaryPassword{
				{
					Password:  "valid123",
					ExpiresAt: time.Now().Add(time.Hour),
				},
				{
					Password:  "expired123",
					ExpiresAt: time.Now().Add(-time.Hour),
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTemporaryPasswordValid(tt.passwords)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
