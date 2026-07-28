package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBuiltInOAuthIdentityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return setupBillingAdjustmentTestDB(t, &User{}, &BuiltInOAuthIdentity{})
}

func createBuiltInOAuthTestUser(t *testing.T, db *gorm.DB, username string) User {
	t.Helper()
	user := User{
		Username: username,
		Password: "password",
		AffCode:  username + "-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	return user
}

func TestBuiltInOAuthIdentityClaimIsIdempotentAndCrossAccountUnique(t *testing.T) {
	db := setupBuiltInOAuthIdentityTestDB(t)
	first := createBuiltInOAuthTestUser(t, db, "oauth-first")
	second := createBuiltInOAuthTestUser(t, db, "oauth-second")

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return BindBuiltInOAuthIdentityWithTx(
			tx,
			BuiltInOAuthProviderGitHub,
			"github-identity",
			first.Id,
		)
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return BindBuiltInOAuthIdentityWithTx(
			tx,
			BuiltInOAuthProviderGitHub,
			"github-identity",
			first.Id,
		)
	}))

	err := db.Transaction(func(tx *gorm.DB) error {
		return BindBuiltInOAuthIdentityWithTx(
			tx,
			BuiltInOAuthProviderGitHub,
			"github-identity",
			second.Id,
		)
	})
	var conflict *BuiltInOAuthIdentityConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, first.Id, conflict.ExistingUserID)

	var identities []BuiltInOAuthIdentity
	require.NoError(t, db.Find(&identities).Error)
	require.Len(t, identities, 1)
	assert.Equal(t, first.Id, identities[0].UserID)

	require.NoError(t, db.First(&first, first.Id).Error)
	require.NoError(t, db.First(&second, second.Id).Error)
	assert.Equal(t, "github-identity", first.GitHubId)
	assert.Empty(t, second.GitHubId)
}

func TestConcurrentBuiltInOAuthClaimsHaveOneWinner(t *testing.T) {
	db := setupBuiltInOAuthIdentityTestDB(t)
	users := []User{
		createBuiltInOAuthTestUser(t, db, "oauth-racer-one"),
		createBuiltInOAuthTestUser(t, db, "oauth-racer-two"),
	}

	start := make(chan struct{})
	results := make(chan error, len(users))
	var workers sync.WaitGroup
	for _, user := range users {
		workers.Add(1)
		go func(userID int) {
			defer workers.Done()
			<-start
			results <- db.Transaction(func(tx *gorm.DB) error {
				return BindBuiltInOAuthIdentityWithTx(
					tx,
					BuiltInOAuthProviderOIDC,
					"shared-subject",
					userID,
				)
			})
		}(user.Id)
	}
	close(start)
	workers.Wait()
	close(results)

	var succeeded, conflicted int
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		var conflict *BuiltInOAuthIdentityConflictError
		require.True(t, errors.As(err, &conflict), "unexpected claim error: %v", err)
		conflicted++
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, conflicted)

	var identity BuiltInOAuthIdentity
	require.NoError(t, db.Where(
		"provider = ? AND external_id = ?",
		BuiltInOAuthProviderOIDC,
		"shared-subject",
	).First(&identity).Error)
	assert.Contains(t, []int{users[0].Id, users[1].Id}, identity.UserID)

	var boundUsers int64
	require.NoError(t, db.Model(&User{}).
		Where("oidc_id = ?", "shared-subject").
		Count(&boundUsers).Error)
	assert.EqualValues(t, 1, boundUsers)
}

func TestBackfillBuiltInOAuthIdentitiesFailsClosedOnDuplicateHistory(t *testing.T) {
	db := setupBuiltInOAuthIdentityTestDB(t)
	first := createBuiltInOAuthTestUser(t, db, "duplicate-oauth-one")
	second := createBuiltInOAuthTestUser(t, db, "duplicate-oauth-two")
	require.NoError(t, db.Model(&first).Update("discord_id", "duplicate-external-id").Error)
	require.NoError(t, db.Model(&second).Update("discord_id", "duplicate-external-id").Error)

	err := backfillBuiltInOAuthIdentities()
	var conflict *BuiltInOAuthIdentityConflictError
	require.ErrorAs(t, err, &conflict)

	var identityCount int64
	require.NoError(t, db.Model(&BuiltInOAuthIdentity{}).Count(&identityCount).Error)
	assert.Zero(t, identityCount, "a failed startup backfill must roll back partial mappings")
}

func TestBackfillBuiltInOAuthIdentitiesPreservesSoftDeletedReservation(t *testing.T) {
	db := setupBuiltInOAuthIdentityTestDB(t)
	user := createBuiltInOAuthTestUser(t, db, "deleted-oauth-user")
	require.NoError(t, db.Model(&user).Update("github_id", "deleted-github-id").Error)
	require.NoError(t, db.Delete(&user).Error)

	require.NoError(t, backfillBuiltInOAuthIdentities())
	taken, err := IsBuiltInOAuthIdentityTaken(
		BuiltInOAuthProviderGitHub,
		"deleted-github-id",
	)
	require.NoError(t, err)
	assert.True(t, taken)

	resolved := User{}
	require.NoError(t, FillUserByBuiltInOAuthIdentity(
		&resolved,
		BuiltInOAuthProviderGitHub,
		"deleted-github-id",
	))
	assert.Zero(t, resolved.Id, "soft-deleted OAuth accounts must not become loginable")
}

func TestGitHubLegacyIdentityBackfillAndMigrationAreCaseInsensitive(t *testing.T) {
	db := setupBuiltInOAuthIdentityTestDB(t)
	user := createBuiltInOAuthTestUser(t, db, "github-case-user")
	require.NoError(t, db.Model(&user).Update("github_id", "CaseSensitiveLogin").Error)
	require.NoError(t, db.Exec(
		"CREATE INDEX idx_users_github_id ON users(github_id)",
	).Error)

	require.NoError(t, backfillBuiltInOAuthIdentities())
	assert.False(t, db.Migrator().HasIndex(&User{}, "idx_users_github_id"))

	resolved := User{}
	require.NoError(t, FillUserByBuiltInOAuthIdentity(
		&resolved,
		BuiltInOAuthProviderGitHub,
		"cAsEsEnSiTiVeLoGiN",
	))
	assert.Equal(t, user.Id, resolved.Id)
	assert.Equal(t, "casesensitivelogin", resolved.GitHubId)

	require.NoError(t, resolved.UpdateGitHubId("123456789"))

	numericResolved := User{}
	require.NoError(t, FillUserByBuiltInOAuthIdentity(
		&numericResolved,
		BuiltInOAuthProviderGitHub,
		"123456789",
	))
	assert.Equal(t, user.Id, numericResolved.Id)

	taken, err := IsBuiltInOAuthIdentityTaken(
		BuiltInOAuthProviderGitHub,
		"CaseSensitiveLogin",
	)
	require.NoError(t, err)
	assert.False(t, taken)
}

func TestOIDCExternalIDsRemainCaseSensitiveAcrossDialects(t *testing.T) {
	db := setupBuiltInOAuthIdentityTestDB(t)
	upper := createBuiltInOAuthTestUser(t, db, "oidc-case-upper")
	lower := createBuiltInOAuthTestUser(t, db, "oidc-case-lower")

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return BindBuiltInOAuthIdentityWithTx(
			tx,
			BuiltInOAuthProviderOIDC,
			"Subject-A",
			upper.Id,
		)
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return BindBuiltInOAuthIdentityWithTx(
			tx,
			BuiltInOAuthProviderOIDC,
			"subject-a",
			lower.Id,
		)
	}))

	var upperResolved, lowerResolved User
	require.NoError(t, FillUserByBuiltInOAuthIdentity(
		&upperResolved,
		BuiltInOAuthProviderOIDC,
		"Subject-A",
	))
	require.NoError(t, FillUserByBuiltInOAuthIdentity(
		&lowerResolved,
		BuiltInOAuthProviderOIDC,
		"subject-a",
	))
	assert.Equal(t, upper.Id, upperResolved.Id)
	assert.Equal(t, lower.Id, lowerResolved.Id)
}

func TestBuiltInOAuthIdentityHashMismatchFailsClosedAndBackfillRejectsIt(t *testing.T) {
	db := setupBuiltInOAuthIdentityTestDB(t)
	user := createBuiltInOAuthTestUser(t, db, "oauth-hash-mismatch")
	require.NoError(t, db.Model(&user).Update("oidc_id", "stored-subject").Error)
	require.NoError(t, db.Create(&BuiltInOAuthIdentity{
		Provider:       BuiltInOAuthProviderOIDC,
		ExternalID:     "stored-subject",
		ExternalIDHash: oauthExternalIDHash("different-subject"),
		UserID:         user.Id,
	}).Error)

	taken, err := IsBuiltInOAuthIdentityTaken(
		BuiltInOAuthProviderOIDC,
		"different-subject",
	)
	assert.False(t, taken)
	require.ErrorContains(t, err, "hash collision")

	require.Error(t, backfillBuiltInOAuthIdentities())
}

func TestHardDeleteRemovesBuiltInOAuthIdentityBeforeBackfillValidation(t *testing.T) {
	db, user := setupUserDeletionGuardTest(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return BindBuiltInOAuthIdentityWithTx(
			tx,
			BuiltInOAuthProviderTelegram,
			"hard-delete-telegram-id",
			user.Id,
		)
	}))

	require.NoError(t, HardDeleteUserById(user.Id))

	var identityCount int64
	require.NoError(t, db.Model(&BuiltInOAuthIdentity{}).
		Where("user_id = ?", user.Id).
		Count(&identityCount).Error)
	assert.Zero(t, identityCount)
	require.NoError(t, backfillBuiltInOAuthIdentities())
}

func TestClearBindingRemovesNormalizedIdentityAndLegacyColumn(t *testing.T) {
	db := setupBuiltInOAuthIdentityTestDB(t)
	user := createBuiltInOAuthTestUser(t, db, "oauth-clear-binding")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return BindBuiltInOAuthIdentityWithTx(
			tx,
			BuiltInOAuthProviderGitHub,
			"ClearBindingLogin",
			user.Id,
		)
	}))

	require.NoError(t, user.ClearBinding("github"))

	taken, err := IsBuiltInOAuthIdentityTaken(
		BuiltInOAuthProviderGitHub,
		"clearbindinglogin",
	)
	require.NoError(t, err)
	assert.False(t, taken)

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Empty(t, user.GitHubId)
}
