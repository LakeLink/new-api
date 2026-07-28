package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserScalarQueriesRejectMissingUsers(t *testing.T) {
	const missingUserID = -12345

	_, err := GetUserQuota(missingUserID, false)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	_, err = GetUserUsedQuota(missingUserID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	_, err = GetUserEmail(missingUserID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	_, err = GetUserGroup(missingUserID, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	_, err = GetUserSetting(missingUserID, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	_, err = GetUsernameById(missingUserID, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
