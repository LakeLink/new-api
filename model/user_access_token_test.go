package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpdateUserAccessTokenOnlyChangesCredential(t *testing.T) {
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}))
	DB = db
	t.Cleanup(func() { DB = oldDB })

	user := User{
		Username: "access-token-update",
		Email:    "before@example.com",
		AffCode:  "access-token-update-aff",
		Setting:  `{"language":"en"}`,
	}
	require.NoError(t, db.Create(&user).Error)

	stale := user
	require.NoError(t, db.Model(&User{}).
		Where("id = ?", user.Id).
		Updates(map[string]any{
			"email":   "after@example.com",
			"setting": `{"language":"fr"}`,
		}).Error)

	require.NoError(t, UpdateUserAccessToken(stale.Id, "fresh-access-token"))

	var saved User
	require.NoError(t, db.First(&saved, user.Id).Error)
	assert.Equal(t, "fresh-access-token", saved.GetAccessToken())
	assert.Equal(t, "after@example.com", saved.Email)
	assert.Equal(t, `{"language":"fr"}`, saved.Setting)
}

func TestUpdateUserAccessTokenRejectsMissingUser(t *testing.T) {
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}))
	DB = db
	t.Cleanup(func() { DB = oldDB })

	assert.ErrorIs(t, UpdateUserAccessToken(999, "fresh-access-token"), gorm.ErrRecordNotFound)
}
