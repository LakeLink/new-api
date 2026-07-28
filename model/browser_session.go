package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	BrowserSessionTTLSeconds       int64 = 30 * 24 * 60 * 60
	browserSessionIDByteLength           = 16
	browserSessionCleanupBatchSize       = 200
)

// BrowserSession is the server-side liveness boundary for a signed browser
// cookie. Only a one-way hash is persisted, so a database read cannot recover
// a usable session credential.
type BrowserSession struct {
	SessionHash string `json:"-" gorm:"type:char(64);primaryKey;autoIncrement:false"`
	UserID      int    `json:"-" gorm:"not null;index"`
	ExpiresAt   int64  `json:"-" gorm:"type:bigint;not null;index"`
}

func (BrowserSession) TableName() string {
	return "browser_sessions"
}

func browserSessionHash(sessionID string) (string, error) {
	if len(sessionID) != hex.EncodedLen(browserSessionIDByteLength) {
		return "", errors.New("browser session id has invalid length")
	}
	raw := make([]byte, browserSessionIDByteLength)
	if _, err := hex.Decode(raw, []byte(sessionID)); err != nil {
		return "", errors.New("browser session id has invalid encoding")
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func CreateBrowserSession(userID int, now int64) (string, error) {
	return RotateBrowserSession(userID, 0, "", now)
}

func RotateBrowserSession(
	userID int,
	previousUserID int,
	previousSessionID string,
	now int64,
) (string, error) {
	if userID <= 0 {
		return "", errors.New("browser session user id is invalid")
	}
	if now <= 0 || now > math.MaxInt64-BrowserSessionTTLSeconds {
		return "", errors.New("browser session time is invalid")
	}
	sessionID := common.GetUUID()
	sessionHash, err := browserSessionHash(sessionID)
	if err != nil {
		return "", err
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).
			Select("id").
			Where("id = ?", userID).
			First(&user).Error; err != nil {
			return err
		}
		if previousSessionID != "" {
			if previousUserID <= 0 {
				return errors.New("previous browser session user id is invalid")
			}
			previousHash, err := browserSessionHash(previousSessionID)
			if err != nil {
				return err
			}
			if err := tx.Where(
				"session_hash = ? AND user_id = ?",
				previousHash,
				previousUserID,
			).Delete(&BrowserSession{}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&BrowserSession{
			SessionHash: sessionHash,
			UserID:      userID,
			ExpiresAt:   now + BrowserSessionTTLSeconds,
		}).Error
	})
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

// GetUserByBrowserSession validates the active session and loads its user in a
// single query, avoiding a second user read in the authentication middleware.
func GetUserByBrowserSession(
	userID int,
	sessionID string,
	now int64,
) (*User, error) {
	if userID <= 0 {
		return nil, errors.New("browser session user id is invalid")
	}
	if now <= 0 {
		return nil, errors.New("browser session time is invalid")
	}
	sessionHash, err := browserSessionHash(sessionID)
	if err != nil {
		return nil, err
	}

	var user User
	err = DB.Model(&User{}).
		Joins(
			"JOIN browser_sessions ON browser_sessions.user_id = users.id",
		).
		Where(
			"users.id = ? AND browser_sessions.session_hash = ? AND browser_sessions.expires_at > ?",
			userID,
			sessionHash,
			now,
		).
		Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func RevokeBrowserSession(userID int, sessionID string) error {
	if userID <= 0 {
		return errors.New("browser session user id is invalid")
	}
	sessionHash, err := browserSessionHash(sessionID)
	if err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.Where(
			"session_hash = ? AND user_id = ?",
			sessionHash,
			userID,
		).Delete(&BrowserSession{}).Error
	})
}

func RevokeAllBrowserSessionsWithTx(tx *gorm.DB, userID int) error {
	if tx == nil {
		return errors.New("browser session transaction is nil")
	}
	if userID <= 0 {
		return errors.New("browser session user id is invalid")
	}
	return tx.Where("user_id = ?", userID).Delete(&BrowserSession{}).Error
}

func CleanupExpiredBrowserSessions(now int64) (int64, error) {
	if now <= 0 {
		return 0, errors.New("browser session cleanup time is invalid")
	}
	var hashes []string
	if err := DB.Model(&BrowserSession{}).
		Where("expires_at <= ?", now).
		Order("expires_at asc").
		Limit(browserSessionCleanupBatchSize).
		Pluck("session_hash", &hashes).Error; err != nil {
		return 0, err
	}
	if len(hashes) == 0 {
		return 0, nil
	}
	result := DB.Where("session_hash IN ?", hashes).Delete(&BrowserSession{})
	if result.Error != nil {
		return 0, fmt.Errorf("delete expired browser sessions: %w", result.Error)
	}
	return result.RowsAffected, nil
}
