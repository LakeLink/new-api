package model

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UserOAuthBinding stores the binding relationship between users and custom OAuth providers
type UserOAuthBinding struct {
	Id                 int       `json:"id" gorm:"primaryKey"`
	UserId             int       `json:"user_id" gorm:"not null;uniqueIndex:ux_user_provider"` // User ID - one binding per user per provider
	ProviderId         int       `json:"provider_id" gorm:"not null;uniqueIndex:ux_user_provider;uniqueIndex:ux_provider_userid_hash"`
	ProviderUserId     string    `json:"provider_user_id" gorm:"type:varchar(256);not null"` // User ID from OAuth provider - one OAuth account per provider
	ProviderUserIdHash string    `json:"-" gorm:"type:char(64);uniqueIndex:ux_provider_userid_hash"`
	CreatedAt          time.Time `json:"created_at"`
}

func (UserOAuthBinding) TableName() string {
	return "user_oauth_bindings"
}

type UserOAuthBindingConflictError struct {
	ProviderID     int
	ProviderUserID string
	ExistingUserID int
}

func (e *UserOAuthBindingConflictError) Error() string {
	return fmt.Sprintf("OAuth provider %d identity is already bound", e.ProviderID)
}

func normalizeUserOAuthBinding(providerID int, providerUserID string) (string, string, error) {
	if providerID <= 0 {
		return "", "", errors.New("provider ID is required")
	}
	if providerUserID == "" {
		return "", "", errors.New("provider user ID is required")
	}
	if len(providerUserID) > 256 {
		return "", "", errors.New("provider user ID exceeds 256 bytes")
	}
	return providerUserID, oauthExternalIDHash(providerUserID), nil
}

func (binding *UserOAuthBinding) normalize() error {
	if binding.UserId <= 0 {
		return errors.New("user ID is required")
	}
	providerUserID, providerUserIDHash, err := normalizeUserOAuthBinding(
		binding.ProviderId,
		binding.ProviderUserId,
	)
	if err != nil {
		return err
	}
	binding.ProviderUserId = providerUserID
	binding.ProviderUserIdHash = providerUserIDHash
	return nil
}

func (binding *UserOAuthBinding) BeforeCreate(_ *gorm.DB) error {
	return binding.normalize()
}

func findUserOAuthBindingByExternalID(
	db *gorm.DB,
	providerID int,
	providerUserID string,
) (*UserOAuthBinding, error) {
	if db == nil {
		return nil, errors.New("OAuth binding database is nil")
	}
	providerUserID, providerUserIDHash, err := normalizeUserOAuthBinding(
		providerID,
		providerUserID,
	)
	if err != nil {
		return nil, err
	}
	var binding UserOAuthBinding
	if err := db.Where(
		"provider_id = ? AND provider_user_id_hash = ?",
		providerID,
		providerUserIDHash,
	).First(&binding).Error; err != nil {
		return nil, err
	}
	if binding.ProviderUserId != providerUserID {
		return nil, errors.New("OAuth provider user ID hash collision")
	}
	return &binding, nil
}

func lockUserOAuthBindingParents(tx *gorm.DB, userID int, providerID int) error {
	var user User
	if err := lockForUpdate(tx).
		Select("id").
		Where("id = ?", userID).
		First(&user).Error; err != nil {
		return err
	}
	var provider CustomOAuthProvider
	return lockForUpdate(tx).
		Select("id").
		Where("id = ?", providerID).
		First(&provider).Error
}

// GetUserOAuthBindingsByUserId returns all OAuth bindings for a user
func GetUserOAuthBindingsByUserId(userId int) ([]*UserOAuthBinding, error) {
	var bindings []*UserOAuthBinding
	err := DB.Where("user_id = ?", userId).Find(&bindings).Error
	return bindings, err
}

// GetUserOAuthBinding returns a specific binding for a user and provider
func GetUserOAuthBinding(userId, providerId int) (*UserOAuthBinding, error) {
	var binding UserOAuthBinding
	err := DB.Where("user_id = ? AND provider_id = ?", userId, providerId).First(&binding).Error
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

// GetUserByOAuthBinding finds a user by provider ID and provider user ID
func GetUserByOAuthBinding(providerId int, providerUserId string) (*User, error) {
	binding, err := findUserOAuthBindingByExternalID(DB, providerId, providerUserId)
	if err != nil {
		return nil, err
	}

	var user User
	err = DB.First(&user, binding.UserId).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Soft-deleted users keep their OAuth reservation so the external
		// account cannot be registered again, but must not become loginable.
		return &User{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// IsProviderUserIdTaken checks if a provider user ID is already bound to any user.
func IsProviderUserIdTaken(providerId int, providerUserId string) (bool, error) {
	_, err := findUserOAuthBindingByExternalID(DB, providerId, providerUserId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CreateUserOAuthBinding creates a new OAuth binding
func CreateUserOAuthBinding(binding *UserOAuthBinding) error {
	if binding == nil {
		return errors.New("OAuth binding is nil")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return CreateUserOAuthBindingWithTx(tx, binding)
	})
}

// CreateUserOAuthBindingWithTx creates a new OAuth binding within a transaction
func CreateUserOAuthBindingWithTx(tx *gorm.DB, binding *UserOAuthBinding) error {
	if tx == nil {
		return errors.New("OAuth binding transaction is nil")
	}
	if binding == nil {
		return errors.New("OAuth binding is nil")
	}
	if err := binding.normalize(); err != nil {
		return err
	}
	if err := lockUserOAuthBindingParents(tx, binding.UserId, binding.ProviderId); err != nil {
		return err
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = time.Now()
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(binding).Error; err != nil {
		return err
	}

	byExternal, err := findUserOAuthBindingByExternalID(
		lockForUpdate(tx),
		binding.ProviderId,
		binding.ProviderUserId,
	)
	if err != nil {
		return err
	}
	if byExternal.UserId != binding.UserId {
		return &UserOAuthBindingConflictError{
			ProviderID:     binding.ProviderId,
			ProviderUserID: binding.ProviderUserId,
			ExistingUserID: byExternal.UserId,
		}
	}

	var byUser UserOAuthBinding
	if err := lockForUpdate(tx).
		Where("user_id = ? AND provider_id = ?", binding.UserId, binding.ProviderId).
		First(&byUser).Error; err != nil {
		return err
	}
	if byUser.ProviderUserIdHash != binding.ProviderUserIdHash ||
		byUser.ProviderUserId != binding.ProviderUserId {
		return fmt.Errorf(
			"user %d already has a different binding for OAuth provider %d",
			binding.UserId,
			binding.ProviderId,
		)
	}
	binding.Id = byUser.Id
	binding.CreatedAt = byUser.CreatedAt
	return nil
}

// UpdateUserOAuthBinding updates an existing OAuth binding (e.g., rebind to different OAuth account)
func UpdateUserOAuthBinding(userId, providerId int, newProviderUserId string) error {
	if userId <= 0 {
		return errors.New("user ID is required")
	}
	newProviderUserId, newProviderUserIdHash, err := normalizeUserOAuthBinding(
		providerId,
		newProviderUserId,
	)
	if err != nil {
		return err
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		if err := lockUserOAuthBindingParents(tx, userId, providerId); err != nil {
			return err
		}
		var binding UserOAuthBinding
		err := lockForUpdate(tx).
			Where("user_id = ? AND provider_id = ?", userId, providerId).
			First(&binding).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CreateUserOAuthBindingWithTx(tx, &UserOAuthBinding{
				UserId:         userId,
				ProviderId:     providerId,
				ProviderUserId: newProviderUserId,
			})
		}
		if err != nil {
			return err
		}
		if binding.ProviderUserId == newProviderUserId &&
			binding.ProviderUserIdHash == newProviderUserIdHash {
			return nil
		}

		byExternal, err := findUserOAuthBindingByExternalID(
			lockForUpdate(tx),
			providerId,
			newProviderUserId,
		)
		if err == nil && byExternal.UserId != userId {
			return &UserOAuthBindingConflictError{
				ProviderID:     providerId,
				ProviderUserID: newProviderUserId,
				ExistingUserID: byExternal.UserId,
			}
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		result := tx.Model(&binding).Updates(map[string]any{
			"provider_user_id":      newProviderUserId,
			"provider_user_id_hash": newProviderUserIdHash,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("OAuth binding changed before update")
		}
		return nil
	})
}

// DeleteUserOAuthBinding deletes an OAuth binding
func DeleteUserOAuthBinding(userId, providerId int) error {
	if userId <= 0 {
		return errors.New("user ID is required")
	}
	if providerId <= 0 {
		return errors.New("provider ID is required")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := lockUserOAuthBindingParents(tx, userId, providerId); err != nil {
			return err
		}
		return tx.Where("user_id = ? AND provider_id = ?", userId, providerId).
			Delete(&UserOAuthBinding{}).Error
	})
}

func deleteUserOAuthBindingsByUserId(tx *gorm.DB, userId int) error {
	return tx.Where("user_id = ?", userId).Delete(&UserOAuthBinding{}).Error
}

// GetBindingCountByProviderId returns the number of bindings for a provider
func GetBindingCountByProviderId(providerId int) (int64, error) {
	var count int64
	err := DB.Model(&UserOAuthBinding{}).Where("provider_id = ?", providerId).Count(&count).Error
	return count, err
}

// migrateUserOAuthBindingHashes upgrades the old text-key uniqueness boundary
// without relying on a database collation. AutoMigrate first adds the nullable
// hash column and its compact unique index; this pass fills and validates every
// historical row before removing the legacy, potentially oversized text index.
func migrateUserOAuthBindingHashes() error {
	err := DB.Transaction(func(tx *gorm.DB) error {
		var bindings []UserOAuthBinding
		err := tx.Select(
			"id",
			"user_id",
			"provider_id",
			"provider_user_id",
			"provider_user_id_hash",
		).FindInBatches(&bindings, builtInOAuthBackfillBatchSize, func(_ *gorm.DB, _ int) error {
			for _, binding := range bindings {
				providerUserID, providerUserIDHash, err := normalizeUserOAuthBinding(
					binding.ProviderId,
					binding.ProviderUserId,
				)
				if err != nil {
					return fmt.Errorf("migrate OAuth binding %d: %w", binding.Id, err)
				}
				if binding.ProviderUserId == providerUserID &&
					binding.ProviderUserIdHash == providerUserIDHash {
					continue
				}
				result := tx.Model(&UserOAuthBinding{}).
					Where("id = ?", binding.Id).
					Update("provider_user_id_hash", providerUserIDHash)
				if result.Error != nil {
					return fmt.Errorf("migrate OAuth binding %d: %w", binding.Id, result.Error)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf("migrate OAuth binding %d: row disappeared", binding.Id)
				}
			}
			return nil
		}).Error
		if err != nil {
			return err
		}

		var migrated []UserOAuthBinding
		return tx.Select(
			"id",
			"user_id",
			"provider_id",
			"provider_user_id",
			"provider_user_id_hash",
		).FindInBatches(&migrated, builtInOAuthBackfillBatchSize, func(_ *gorm.DB, _ int) error {
			for _, binding := range migrated {
				_, expectedHash, err := normalizeUserOAuthBinding(
					binding.ProviderId,
					binding.ProviderUserId,
				)
				if err != nil {
					return fmt.Errorf("validate OAuth binding %d: %w", binding.Id, err)
				}
				if binding.UserId <= 0 || binding.ProviderUserIdHash != expectedHash {
					return fmt.Errorf("OAuth binding %d has invalid normalization data", binding.Id)
				}
			}
			return nil
		}).Error
	})
	if err != nil {
		return err
	}

	migrator := DB.Migrator()
	if migrator.HasIndex(&UserOAuthBinding{}, "ux_provider_userid") {
		if err := migrator.DropIndex(&UserOAuthBinding{}, "ux_provider_userid"); err != nil {
			return fmt.Errorf("drop legacy OAuth identity index: %w", err)
		}
	}
	return nil
}
