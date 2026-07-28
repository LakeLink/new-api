package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"gorm.io/gorm"
)

const oauthStateCleanupBatchSize = 200

// OAuthState is the authoritative one-time CSRF credential for OAuth
// callbacks. Browser sessions are stored in signed cookies, so deleting a
// session value alone cannot prevent replay of an older valid cookie.
type OAuthState struct {
	StateHash string `json:"-" gorm:"type:char(64);primaryKey;autoIncrement:false"`
	ExpiresAt int64  `json:"-" gorm:"type:bigint;not null;index"`
}

func oauthStateHash(state string) (string, error) {
	if state == "" {
		return "", errors.New("OAuth state is empty")
	}
	digest := sha256.Sum256([]byte(state))
	return hex.EncodeToString(digest[:]), nil
}

// RotateOAuthState revokes the previous cookie-bound state and creates a new
// one in one database transaction.
func RotateOAuthState(previousState string, nextState string, expiresAt int64) error {
	if expiresAt <= 0 {
		return errors.New("OAuth state expiry is invalid")
	}
	nextHash, err := oauthStateHash(nextState)
	if err != nil {
		return err
	}
	var previousHash string
	if previousState != "" {
		previousHash, err = oauthStateHash(previousState)
		if err != nil {
			return err
		}
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if previousHash != "" {
			if err := tx.Where("state_hash = ?", previousHash).Delete(&OAuthState{}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&OAuthState{
			StateHash: nextHash,
			ExpiresAt: expiresAt,
		}).Error
	})
}

// ConsumeOAuthState atomically claims a non-expired state. Exactly one
// callback can observe true, even if every request replays the same original
// signed cookie or lands on a different application node.
func ConsumeOAuthState(state string, now int64) (bool, error) {
	stateHash, err := oauthStateHash(state)
	if err != nil {
		return false, err
	}
	result := DB.Where("state_hash = ? AND expires_at >= ?", stateHash, now).
		Delete(&OAuthState{})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 1 {
		return true, nil
	}
	// Remove the matching expired row without affecting a concurrently rotated
	// state, whose cryptographic hash is different.
	if err := DB.Where("state_hash = ? AND expires_at < ?", stateHash, now).
		Delete(&OAuthState{}).Error; err != nil {
		return false, err
	}
	return false, nil
}

func DeleteOAuthState(state string) error {
	stateHash, err := oauthStateHash(state)
	if err != nil {
		return err
	}
	return DB.Where("state_hash = ?", stateHash).Delete(&OAuthState{}).Error
}

func CleanupExpiredOAuthStates(now int64) (int64, error) {
	var hashes []string
	if err := DB.Model(&OAuthState{}).
		Where("expires_at < ?", now).
		Order("expires_at asc").
		Limit(oauthStateCleanupBatchSize).
		Pluck("state_hash", &hashes).Error; err != nil {
		return 0, err
	}
	if len(hashes) == 0 {
		return 0, nil
	}
	result := DB.Where("state_hash IN ?", hashes).Delete(&OAuthState{})
	return result.RowsAffected, result.Error
}
