package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const authenticationTokenCleanupBatchSize = 200

// AuthenticationToken is the server-side one-time boundary for authentication
// assertions whose full state is also carried in a signed browser cookie or
// supplied by an external identity provider. Only a digest is persisted.
type AuthenticationToken struct {
	Scope      string `json:"-" gorm:"type:varchar(32);primaryKey;autoIncrement:false"`
	TokenHash  string `json:"-" gorm:"type:char(64);primaryKey;autoIncrement:false"`
	ClaimToken string `json:"-" gorm:"type:varchar(32);not null"`
	ExpiresAt  int64  `json:"-" gorm:"type:bigint;not null;index"`
}

func (AuthenticationToken) TableName() string {
	return "authentication_tokens"
}

func normalizeAuthenticationToken(scope string, token string) (string, string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" || len(scope) > 32 {
		return "", "", errors.New("authentication token scope is invalid")
	}
	if token == "" {
		return "", "", errors.New("authentication token is empty")
	}
	digest := sha256.Sum256([]byte(token))
	return scope, hex.EncodeToString(digest[:]), nil
}

// RotateAuthenticationToken revokes the previous challenge for the same flow
// and registers the next challenge. Callers must store the matching browser
// state only after this transaction commits.
func RotateAuthenticationToken(
	scope string,
	previousToken string,
	nextToken string,
	expiresAt int64,
	now int64,
) error {
	scope, nextHash, err := normalizeAuthenticationToken(scope, nextToken)
	if err != nil {
		return err
	}
	if now <= 0 || expiresAt <= now {
		return errors.New("authentication token expiry is invalid")
	}
	var previousHash string
	if previousToken != "" {
		_, previousHash, err = normalizeAuthenticationToken(scope, previousToken)
		if err != nil {
			return err
		}
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		if previousHash != "" {
			if err := tx.Where(
				"scope = ? AND token_hash = ?",
				scope,
				previousHash,
			).Delete(&AuthenticationToken{}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&AuthenticationToken{
			Scope:      scope,
			TokenHash:  nextHash,
			ClaimToken: common.GetUUID(),
			ExpiresAt:  expiresAt,
		}).Error
	})
	if err != nil {
		return err
	}
	cleanupExpiredAuthenticationTokens(now)
	return nil
}

// ConsumeAuthenticationToken atomically consumes a registered, non-expired
// challenge. Replaying an older signed session cookie cannot recreate the row.
func ConsumeAuthenticationToken(
	scope string,
	token string,
	now int64,
) (bool, error) {
	scope, tokenHash, err := normalizeAuthenticationToken(scope, token)
	if err != nil {
		return false, err
	}
	if now <= 0 {
		return false, errors.New("authentication token time is invalid")
	}
	result := DB.Where(
		"scope = ? AND token_hash = ? AND expires_at > ?",
		scope,
		tokenHash,
		now,
	).Delete(&AuthenticationToken{})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 1 {
		return true, nil
	}
	if err := DB.Where(
		"scope = ? AND token_hash = ? AND expires_at <= ?",
		scope,
		tokenHash,
		now,
	).Delete(&AuthenticationToken{}).Error; err != nil {
		return false, err
	}
	return false, nil
}

// ClaimAuthenticationToken records an externally signed assertion on first
// use and rejects every replay until its trusted expiry.
func ClaimAuthenticationToken(
	scope string,
	token string,
	expiresAt int64,
	now int64,
) (bool, error) {
	scope, tokenHash, err := normalizeAuthenticationToken(scope, token)
	if err != nil {
		return false, err
	}
	if now <= 0 || expiresAt <= now {
		return false, nil
	}
	claimToken := common.GetUUID()
	claimed := false
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where(
			"scope = ? AND token_hash = ? AND expires_at <= ?",
			scope,
			tokenHash,
			now,
		).Delete(&AuthenticationToken{}).Error; err != nil {
			return err
		}
		row := AuthenticationToken{
			Scope:      scope,
			TokenHash:  tokenHash,
			ClaimToken: claimToken,
			ExpiresAt:  expiresAt,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&row).Error; err != nil {
			return err
		}
		var persisted AuthenticationToken
		if err := lockForUpdate(tx).Where(
			"scope = ? AND token_hash = ?",
			scope,
			tokenHash,
		).First(&persisted).Error; err != nil {
			return err
		}
		claimed = persisted.ClaimToken == claimToken
		return nil
	})
	if err != nil {
		return false, err
	}
	cleanupExpiredAuthenticationTokens(now)
	return claimed, nil
}

func DeleteAuthenticationToken(scope string, token string) error {
	scope, tokenHash, err := normalizeAuthenticationToken(scope, token)
	if err != nil {
		return err
	}
	return DB.Where(
		"scope = ? AND token_hash = ?",
		scope,
		tokenHash,
	).Delete(&AuthenticationToken{}).Error
}

func cleanupExpiredAuthenticationTokens(now int64) {
	var tokens []AuthenticationToken
	if err := DB.Where("expires_at <= ?", now).
		Order("expires_at asc").
		Limit(authenticationTokenCleanupBatchSize).
		Find(&tokens).Error; err != nil {
		common.SysLog("failed to scan expired authentication tokens: " + err.Error())
		return
	}
	for _, token := range tokens {
		if err := DB.Where(
			"scope = ? AND token_hash = ? AND expires_at <= ?",
			token.Scope,
			token.TokenHash,
			now,
		).Delete(&AuthenticationToken{}).Error; err != nil {
			common.SysLog("failed to delete expired authentication token: " + err.Error())
			return
		}
	}
}
