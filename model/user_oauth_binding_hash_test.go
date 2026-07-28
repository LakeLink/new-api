package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type legacyUserOAuthBinding struct {
	Id             int    `gorm:"primaryKey"`
	UserId         int    `gorm:"not null;uniqueIndex:ux_user_provider"`
	ProviderId     int    `gorm:"not null;uniqueIndex:ux_user_provider;uniqueIndex:ux_provider_userid"`
	ProviderUserId string `gorm:"type:varchar(256);not null;uniqueIndex:ux_provider_userid"`
	CreatedAt      time.Time
}

func (legacyUserOAuthBinding) TableName() string {
	return "user_oauth_bindings"
}

func createCustomOAuthTestProvider(t *testing.T, db *gorm.DB, id int) {
	t.Helper()
	require.NoError(t, db.Create(&CustomOAuthProvider{
		Id:   id,
		Name: "Custom OAuth",
		Slug: fmt.Sprintf("custom-oauth-%d", id),
	}).Error)
}

func TestCustomOAuthExternalIDsRemainCaseSensitive(t *testing.T) {
	db := setupBillingAdjustmentTestDB(
		t,
		&User{},
		&CustomOAuthProvider{},
		&UserOAuthBinding{},
	)
	first := createBuiltInOAuthTestUser(t, db, "custom-oauth-upper")
	second := createBuiltInOAuthTestUser(t, db, "custom-oauth-lower")
	createCustomOAuthTestProvider(t, db, 7)

	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{
		UserId:         first.Id,
		ProviderId:     7,
		ProviderUserId: "Subject-A",
	}))
	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{
		UserId:         second.Id,
		ProviderId:     7,
		ProviderUserId: "subject-a",
	}))

	upper, err := GetUserByOAuthBinding(7, "Subject-A")
	require.NoError(t, err)
	lower, err := GetUserByOAuthBinding(7, "subject-a")
	require.NoError(t, err)
	assert.Equal(t, first.Id, upper.Id)
	assert.Equal(t, second.Id, lower.Id)
}

func TestCustomOAuthHashCollisionFailsClosed(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &UserOAuthBinding{})
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&UserOAuthBinding{
		UserId:             1,
		ProviderId:         8,
		ProviderUserId:     "stored-subject",
		ProviderUserIdHash: oauthExternalIDHash("different-subject"),
	}).Error)

	taken, err := IsProviderUserIdTaken(8, "different-subject")
	assert.False(t, taken)
	require.ErrorContains(t, err, "hash collision")
}

func TestCustomOAuthSoftDeletedUserRemainsReservedButCannotLogin(t *testing.T) {
	db := setupBillingAdjustmentTestDB(
		t,
		&User{},
		&CustomOAuthProvider{},
		&UserOAuthBinding{},
	)
	user := createBuiltInOAuthTestUser(t, db, "custom-oauth-deleted")
	createCustomOAuthTestProvider(t, db, 12)
	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{
		UserId:         user.Id,
		ProviderId:     12,
		ProviderUserId: "reserved-subject",
	}))
	require.NoError(t, db.Delete(&user).Error)

	taken, err := IsProviderUserIdTaken(12, "reserved-subject")
	require.NoError(t, err)
	assert.True(t, taken)

	resolved, err := GetUserByOAuthBinding(12, "reserved-subject")
	require.NoError(t, err)
	assert.Zero(t, resolved.Id)
}

func TestCustomOAuthProviderDeletionRemovesBindingsAtomically(t *testing.T) {
	db := setupBillingAdjustmentTestDB(
		t,
		&User{},
		&CustomOAuthProvider{},
		&UserOAuthBinding{},
	)
	user := createBuiltInOAuthTestUser(t, db, "custom-oauth-provider-delete")
	createCustomOAuthTestProvider(t, db, 13)
	require.NoError(t, CreateUserOAuthBinding(&UserOAuthBinding{
		UserId:         user.Id,
		ProviderId:     13,
		ProviderUserId: "provider-delete-subject",
	}))

	require.NoError(t, DeleteCustomOAuthProvider(13))

	var providerCount, bindingCount int64
	require.NoError(t, db.Model(&CustomOAuthProvider{}).
		Where("id = ?", 13).
		Count(&providerCount).Error)
	require.NoError(t, db.Model(&UserOAuthBinding{}).
		Where("provider_id = ?", 13).
		Count(&bindingCount).Error)
	assert.Zero(t, providerCount)
	assert.Zero(t, bindingCount)
}

func TestCustomOAuthHashMigrationBackfillsBeforeDroppingLegacyIndex(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t)
	require.NoError(t, db.AutoMigrate(&legacyUserOAuthBinding{}))
	require.NoError(t, db.Create(&legacyUserOAuthBinding{
		UserId:         10,
		ProviderId:     9,
		ProviderUserId: "Subject-A",
	}).Error)
	require.NoError(t, db.Create(&legacyUserOAuthBinding{
		UserId:         11,
		ProviderId:     9,
		ProviderUserId: "subject-a",
	}).Error)
	require.True(t, db.Migrator().HasIndex(&legacyUserOAuthBinding{}, "ux_provider_userid"))

	require.NoError(t, db.AutoMigrate(&UserOAuthBinding{}))
	require.NoError(t, migrateUserOAuthBindingHashes())

	require.False(t, db.Migrator().HasIndex(&UserOAuthBinding{}, "ux_provider_userid"))
	require.True(t, db.Migrator().HasIndex(&UserOAuthBinding{}, "ux_provider_userid_hash"))

	var bindings []UserOAuthBinding
	require.NoError(t, db.Order("id").Find(&bindings).Error)
	require.Len(t, bindings, 2)
	assert.Equal(t, oauthExternalIDHash("Subject-A"), bindings[0].ProviderUserIdHash)
	assert.Equal(t, oauthExternalIDHash("subject-a"), bindings[1].ProviderUserIdHash)
}
