package model

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupBrowserSessionTest(t *testing.T) (*User, int64) {
	t.Helper()
	db := setupBillingAdjustmentTestDB(t, &User{}, &BrowserSession{})
	user := &User{
		Username: "browser-session-user",
		Password: "password",
		AffCode:  "browser-session-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)
	return user, time.Now().Unix()
}

func TestBrowserSessionRevocationIsScopedToCurrentSession(t *testing.T) {
	user, now := setupBrowserSessionTest(t)
	first, err := CreateBrowserSession(user.Id, now)
	require.NoError(t, err)
	second, err := CreateBrowserSession(user.Id, now)
	require.NoError(t, err)

	require.NoError(t, RevokeBrowserSession(user.Id, first))

	firstUser, err := GetUserByBrowserSession(user.Id, first, now)
	require.NoError(t, err)
	assert.Nil(t, firstUser)
	secondUser, err := GetUserByBrowserSession(user.Id, second, now)
	require.NoError(t, err)
	require.NotNil(t, secondUser)
	assert.Equal(t, user.Id, secondUser.Id)
}

func TestBrowserSessionLogoutAllRevokesEverySession(t *testing.T) {
	user, now := setupBrowserSessionTest(t)
	first, err := CreateBrowserSession(user.Id, now)
	require.NoError(t, err)
	second, err := CreateBrowserSession(user.Id, now)
	require.NoError(t, err)

	require.NoError(t, RevokeUserSessions(user.Id))

	for _, sessionID := range []string{first, second} {
		resolved, err := GetUserByBrowserSession(user.Id, sessionID, now)
		require.NoError(t, err)
		assert.Nil(t, resolved)
	}
}

func TestBrowserSessionRejectsWrongUserMalformedAndExpiredCredentials(t *testing.T) {
	user, now := setupBrowserSessionTest(t)
	sessionID, err := CreateBrowserSession(user.Id, now)
	require.NoError(t, err)

	resolved, err := GetUserByBrowserSession(user.Id+1, sessionID, now)
	require.NoError(t, err)
	assert.Nil(t, resolved)

	_, err = GetUserByBrowserSession(user.Id, strings.Repeat("x", 32), now)
	require.ErrorContains(t, err, "encoding")

	resolved, err = GetUserByBrowserSession(
		user.Id,
		sessionID,
		now+BrowserSessionTTLSeconds,
	)
	require.NoError(t, err)
	assert.Nil(t, resolved)
}

func TestExpiredBrowserSessionCleanupIsBounded(t *testing.T) {
	user, now := setupBrowserSessionTest(t)
	sessionID, err := CreateBrowserSession(user.Id, now)
	require.NoError(t, err)

	deleted, err := CleanupExpiredBrowserSessions(now + BrowserSessionTTLSeconds)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	resolved, err := GetUserByBrowserSession(
		user.Id,
		sessionID,
		now+BrowserSessionTTLSeconds,
	)
	require.NoError(t, err)
	assert.Nil(t, resolved)
}
