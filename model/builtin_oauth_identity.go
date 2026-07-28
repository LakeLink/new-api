package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BuiltInOAuthProviderGitHub   = "github"
	BuiltInOAuthProviderDiscord  = "discord"
	BuiltInOAuthProviderOIDC     = "oidc"
	BuiltInOAuthProviderLinuxDO  = "linuxdo"
	BuiltInOAuthProviderWeChat   = "wechat"
	BuiltInOAuthProviderTelegram = "telegram"

	builtInOAuthBackfillBatchSize = 500
)

// BuiltInOAuthIdentity is the cross-database uniqueness boundary for legacy
// OAuth identities stored on users. The legacy columns remain populated for
// compatibility, while these two unique indexes prevent one external account
// from being attached to multiple users and one user from silently replacing
// an existing identity for the same provider.
type BuiltInOAuthIdentity struct {
	ID             int    `json:"id"`
	Provider       string `json:"provider" gorm:"type:varchar(32);not null;uniqueIndex:ux_builtin_oauth_external_hash,priority:1;uniqueIndex:ux_builtin_oauth_user,priority:1"`
	ExternalID     string `json:"external_id" gorm:"type:varchar(256);not null"`
	ExternalIDHash string `json:"-" gorm:"type:char(64);not null;uniqueIndex:ux_builtin_oauth_external_hash,priority:2"`
	UserID         int    `json:"user_id" gorm:"not null;uniqueIndex:ux_builtin_oauth_user,priority:2;index"`
}

func (BuiltInOAuthIdentity) TableName() string {
	return "built_in_oauth_identities"
}

// BuiltInOAuthIdentityConflictError reports the already-bound account without
// relying on dialect-specific duplicate-key errors.
type BuiltInOAuthIdentityConflictError struct {
	Provider       string
	ExternalID     string
	ExistingUserID int
}

func (e *BuiltInOAuthIdentityConflictError) Error() string {
	return fmt.Sprintf("%s OAuth identity is already bound", e.Provider)
}

func builtInOAuthIdentityColumn(provider string) (string, error) {
	switch provider {
	case BuiltInOAuthProviderGitHub:
		return "github_id", nil
	case BuiltInOAuthProviderDiscord:
		return "discord_id", nil
	case BuiltInOAuthProviderOIDC:
		return "oidc_id", nil
	case BuiltInOAuthProviderLinuxDO:
		return "linux_do_id", nil
	case BuiltInOAuthProviderWeChat:
		return "wechat_id", nil
	case BuiltInOAuthProviderTelegram:
		return "telegram_id", nil
	default:
		return "", fmt.Errorf("unsupported built-in OAuth provider %q", provider)
	}
}

func normalizeBuiltInOAuthIdentity(provider string, externalID string) (string, string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if _, err := builtInOAuthIdentityColumn(provider); err != nil {
		return "", "", err
	}
	if externalID == "" {
		return "", "", errors.New("built-in OAuth external id is empty")
	}
	if provider == BuiltInOAuthProviderGitHub && !isDecimalOAuthID(externalID) {
		// GitHub logins are case-insensitive. Numeric IDs are permanent and are
		// already canonical decimal strings, while legacy login bindings must
		// compare consistently on every database collation.
		externalID = strings.ToLower(externalID)
	}
	if len(externalID) > 256 {
		return "", "", errors.New("built-in OAuth external id exceeds 256 bytes")
	}
	return provider, externalID, nil
}

func isDecimalOAuthID(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func oauthExternalIDHash(externalID string) string {
	sum := sha256.Sum256([]byte(externalID))
	encoded := make([]byte, hex.EncodedLen(len(sum)))
	hex.Encode(encoded, sum[:])
	return string(encoded)
}

func IsBuiltInOAuthIdentityTaken(provider string, externalID string) (bool, error) {
	provider, externalID, err := normalizeBuiltInOAuthIdentity(provider, externalID)
	if err != nil {
		return false, err
	}
	var identity BuiltInOAuthIdentity
	err = DB.Where(
		"provider = ? AND external_id_hash = ?",
		provider,
		oauthExternalIDHash(externalID),
	).First(&identity).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if identity.ExternalID != externalID {
		return false, errors.New("built-in OAuth identity hash collision")
	}
	return true, nil
}

// FillUserByBuiltInOAuthIdentity resolves the normalized identity and then
// loads only an active user. A binding to a soft-deleted account intentionally
// returns an empty user with no error, preserving the existing "account
// deleted" login behavior without allowing the identity to be registered again.
func FillUserByBuiltInOAuthIdentity(user *User, provider string, externalID string) error {
	if user == nil {
		return errors.New("OAuth user destination is nil")
	}
	provider, externalID, err := normalizeBuiltInOAuthIdentity(provider, externalID)
	if err != nil {
		return err
	}
	var identity BuiltInOAuthIdentity
	if err := DB.Where(
		"provider = ? AND external_id_hash = ?",
		provider,
		oauthExternalIDHash(externalID),
	).
		First(&identity).Error; err != nil {
		return err
	}
	if identity.ExternalID != externalID {
		return errors.New("built-in OAuth identity hash collision")
	}
	var active User
	err = DB.Where("id = ?", identity.UserID).First(&active).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		*user = User{}
		return nil
	}
	if err != nil {
		return err
	}
	*user = active
	return nil
}

// BindBuiltInOAuthIdentityWithTx atomically claims an external identity and
// writes the matching legacy user column. Both unique relationships are
// inspected after an insert-on-conflict so behavior is portable and does not
// depend on parsing MySQL/PostgreSQL/SQLite error strings.
func BindBuiltInOAuthIdentityWithTx(
	tx *gorm.DB,
	provider string,
	externalID string,
	userID int,
) error {
	if tx == nil {
		return errors.New("built-in OAuth transaction is nil")
	}
	if userID <= 0 {
		return errors.New("built-in OAuth user id is invalid")
	}
	provider, externalID, err := normalizeBuiltInOAuthIdentity(provider, externalID)
	if err != nil {
		return err
	}
	column, _ := builtInOAuthIdentityColumn(provider)

	var user User
	if err := lockForUpdate(tx.Unscoped()).
		Select("id", column).
		Where("id = ?", userID).
		First(&user).Error; err != nil {
		return err
	}
	currentExternalID := builtInOAuthIdentityValue(user, provider)
	if currentExternalID != "" {
		_, normalizedCurrentExternalID, err := normalizeBuiltInOAuthIdentity(provider, currentExternalID)
		if err != nil {
			return err
		}
		if normalizedCurrentExternalID != externalID {
			return fmt.Errorf(
				"user %d already has a different %s OAuth identity",
				userID,
				provider,
			)
		}
	}

	candidate := BuiltInOAuthIdentity{
		Provider:       provider,
		ExternalID:     externalID,
		ExternalIDHash: oauthExternalIDHash(externalID),
		UserID:         userID,
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
		return err
	}

	var byExternal BuiltInOAuthIdentity
	externalErr := lockForUpdate(tx).
		Where(
			"provider = ? AND external_id_hash = ?",
			provider,
			candidate.ExternalIDHash,
		).
		First(&byExternal).Error
	if externalErr == nil && byExternal.ExternalID != externalID {
		return errors.New("built-in OAuth identity hash collision")
	}
	if externalErr == nil && byExternal.UserID != userID {
		return &BuiltInOAuthIdentityConflictError{
			Provider:       provider,
			ExternalID:     externalID,
			ExistingUserID: byExternal.UserID,
		}
	}
	if externalErr != nil && !errors.Is(externalErr, gorm.ErrRecordNotFound) {
		return externalErr
	}

	var byUser BuiltInOAuthIdentity
	userErr := lockForUpdate(tx).
		Where("provider = ? AND user_id = ?", provider, userID).
		First(&byUser).Error
	if userErr != nil {
		return userErr
	}
	if byUser.ExternalID != externalID {
		return fmt.Errorf(
			"user %d already has a different %s OAuth identity",
			userID,
			provider,
		)
	}
	if externalErr != nil {
		return errors.New("built-in OAuth identity claim was not persisted")
	}

	if currentExternalID == externalID {
		return nil
	}
	result := tx.Model(&User{}).Where("id = ?", userID).Update(column, externalID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("built-in OAuth user %d no longer exists", userID)
	}
	return nil
}

func builtInOAuthIdentityValue(user User, provider string) string {
	switch provider {
	case BuiltInOAuthProviderGitHub:
		return user.GitHubId
	case BuiltInOAuthProviderDiscord:
		return user.DiscordId
	case BuiltInOAuthProviderOIDC:
		return user.OidcId
	case BuiltInOAuthProviderLinuxDO:
		return user.LinuxDOId
	case BuiltInOAuthProviderWeChat:
		return user.WeChatId
	case BuiltInOAuthProviderTelegram:
		return user.TelegramId
	default:
		return ""
	}
}

func deleteBuiltInOAuthIdentitiesByUserID(tx *gorm.DB, userID int) error {
	if tx == nil {
		return errors.New("built-in OAuth deletion transaction is nil")
	}
	if userID <= 0 {
		return errors.New("built-in OAuth deletion user id is invalid")
	}
	return tx.Where("user_id = ?", userID).Delete(&BuiltInOAuthIdentity{}).Error
}

func replaceBuiltInOAuthIdentity(
	userID int,
	provider string,
	oldExternalID string,
	newExternalID string,
) error {
	provider, oldExternalID, err := normalizeBuiltInOAuthIdentity(provider, oldExternalID)
	if err != nil {
		return err
	}
	_, newExternalID, err = normalizeBuiltInOAuthIdentity(provider, newExternalID)
	if err != nil {
		return err
	}
	if oldExternalID == newExternalID {
		return nil
	}
	column, _ := builtInOAuthIdentityColumn(provider)

	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).
			Select("id", column).
			Where("id = ?", userID).
			First(&user).Error; err != nil {
			return err
		}
		_, currentExternalID, err := normalizeBuiltInOAuthIdentity(
			provider,
			builtInOAuthIdentityValue(user, provider),
		)
		if err != nil {
			return err
		}
		if currentExternalID != oldExternalID {
			return errors.New("built-in OAuth identity changed before migration")
		}

		var identity BuiltInOAuthIdentity
		if err := lockForUpdate(tx).
			Where(
				"provider = ? AND external_id_hash = ? AND user_id = ?",
				provider,
				oauthExternalIDHash(oldExternalID),
				userID,
			).
			First(&identity).Error; err != nil {
			return err
		}
		if identity.ExternalID != oldExternalID {
			return errors.New("built-in OAuth identity hash collision")
		}
		var occupied BuiltInOAuthIdentity
		err = lockForUpdate(tx).
			Where(
				"provider = ? AND external_id_hash = ?",
				provider,
				oauthExternalIDHash(newExternalID),
			).
			First(&occupied).Error
		if err == nil {
			if occupied.ExternalID != newExternalID {
				return errors.New("built-in OAuth identity hash collision")
			}
			return &BuiltInOAuthIdentityConflictError{
				Provider:       provider,
				ExternalID:     newExternalID,
				ExistingUserID: occupied.UserID,
			}
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Model(&identity).Updates(map[string]any{
			"external_id":      newExternalID,
			"external_id_hash": oauthExternalIDHash(newExternalID),
		}).Error; err != nil {
			return err
		}
		result := tx.Model(&User{}).Where("id = ?", userID).Update(column, newExternalID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("built-in OAuth user %d no longer exists", userID)
		}
		return nil
	})
}

// backfillBuiltInOAuthIdentities converts legacy user columns into the
// normalized uniqueness table. Any duplicate or mismatched historical binding
// aborts startup instead of selecting an arbitrary account.
func backfillBuiltInOAuthIdentities() error {
	if err := DB.Transaction(func(tx *gorm.DB) error {
		var users []User
		err := tx.Unscoped().
			Select("id", "github_id", "discord_id", "oidc_id", "linux_do_id", "wechat_id", "telegram_id").
			FindInBatches(&users, builtInOAuthBackfillBatchSize, func(_ *gorm.DB, _ int) error {
				for _, user := range users {
					identities := []struct {
						provider   string
						externalID string
					}{
						{BuiltInOAuthProviderGitHub, user.GitHubId},
						{BuiltInOAuthProviderDiscord, user.DiscordId},
						{BuiltInOAuthProviderOIDC, user.OidcId},
						{BuiltInOAuthProviderLinuxDO, user.LinuxDOId},
						{BuiltInOAuthProviderWeChat, user.WeChatId},
						{BuiltInOAuthProviderTelegram, user.TelegramId},
					}
					for _, identity := range identities {
						if identity.externalID == "" {
							continue
						}
						if err := BindBuiltInOAuthIdentityWithTx(
							tx,
							identity.provider,
							identity.externalID,
							user.Id,
						); err != nil {
							return fmt.Errorf(
								"backfill %s OAuth identity for user %d: %w",
								identity.provider,
								user.Id,
								err,
							)
						}
					}
				}
				return nil
			}).Error
		if err != nil {
			return err
		}

		var identities []BuiltInOAuthIdentity
		return tx.FindInBatches(
			&identities,
			builtInOAuthBackfillBatchSize,
			func(_ *gorm.DB, _ int) error {
				for _, identity := range identities {
					column, err := builtInOAuthIdentityColumn(identity.Provider)
					if err != nil {
						return err
					}
					_, normalizedExternalID, err := normalizeBuiltInOAuthIdentity(
						identity.Provider,
						identity.ExternalID,
					)
					if err != nil {
						return err
					}
					if normalizedExternalID != identity.ExternalID ||
						identity.ExternalIDHash != oauthExternalIDHash(identity.ExternalID) {
						return fmt.Errorf(
							"%s OAuth identity %q has invalid normalization data",
							identity.Provider,
							identity.ExternalID,
						)
					}
					var user User
					if err := tx.Unscoped().
						Select("id", column).
						Where("id = ?", identity.UserID).
						First(&user).Error; err != nil {
						return fmt.Errorf(
							"validate %s OAuth identity %q: %w",
							identity.Provider,
							identity.ExternalID,
							err,
						)
					}
					if builtInOAuthIdentityValue(user, identity.Provider) != identity.ExternalID {
						return fmt.Errorf(
							"%s OAuth identity %q does not match user %d",
							identity.Provider,
							identity.ExternalID,
							identity.UserID,
						)
					}
				}
				return nil
			},
		).Error
	}); err != nil {
		return err
	}

	// Exact authentication lookups now use the compact normalized identity
	// table. The legacy user columns remain compatibility mirrors, but their
	// single-column indexes are redundant and can exceed conservative MySQL
	// utf8mb4 index budgets when inferred as wide strings.
	legacyIndexes := []string{
		"idx_users_github_id",
		"idx_users_discord_id",
		"idx_users_oidc_id",
		"idx_users_wechat_id",
		"idx_users_telegram_id",
		"idx_users_linux_do_id",
	}
	for _, indexName := range legacyIndexes {
		if !DB.Migrator().HasIndex(&User{}, indexName) {
			continue
		}
		if err := DB.Migrator().DropIndex(&User{}, indexName); err != nil {
			return fmt.Errorf("drop legacy OAuth index %s: %w", indexName, err)
		}
	}
	return nil
}
