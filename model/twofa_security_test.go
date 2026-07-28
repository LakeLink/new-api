package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTwoFASecurityTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(
		&TwoFA{},
		&TwoFABackupCode{},
		&BrowserSession{},
	))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&BrowserSession{}).Error)
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&TwoFABackupCode{}).Error)
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&TwoFA{}).Error)
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&User{}).Error)
	t.Cleanup(func() {
		_ = DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&BrowserSession{}).Error
		_ = DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&TwoFABackupCode{}).Error
		_ = DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&TwoFA{}).Error
		_ = DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&User{}).Error
	})
}

func createTwoFATestRecord(t *testing.T) *TwoFA {
	t.Helper()
	user := User{Username: "twofa-user", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	twoFA := &TwoFA{
		UserId:    user.Id,
		Secret:    "JBSWY3DPEHPK3PXP",
		IsEnabled: true,
	}
	require.NoError(t, DB.Create(twoFA).Error)
	return twoFA
}

func TestTOTPValidationRejectsSameTimeStepReplay(t *testing.T) {
	setupTwoFASecurityTest(t)
	twoFA := createTwoFATestRecord(t)
	code, err := totp.GenerateCode(twoFA.Secret, time.Now())
	require.NoError(t, err)

	valid, err := twoFA.ValidateTOTPAndUpdateUsage(code)
	require.NoError(t, err)
	assert.True(t, valid)

	valid, err = twoFA.ValidateTOTPAndUpdateUsage(code)
	require.NoError(t, err)
	assert.False(t, valid)

	stored, err := GetTwoFAByUserId(twoFA.UserId)
	require.NoError(t, err)
	assert.Positive(t, stored.LastUsedStep)
	assert.Equal(t, 1, stored.FailedAttempts)
}

func TestBackupCodeConsumptionAndFailureCounterAreAtomic(t *testing.T) {
	setupTwoFASecurityTest(t)
	twoFA := createTwoFATestRecord(t)
	require.NoError(t, CreateBackupCodes(twoFA.UserId, []string{"ABCD-1234"}))

	valid, err := twoFA.ValidateCodeAndUpdateUsage("ABCD-1234")
	require.NoError(t, err)
	assert.True(t, valid)

	valid, err = twoFA.ValidateCodeAndUpdateUsage("ABCD-1234")
	require.NoError(t, err)
	assert.False(t, valid)

	stored, err := GetTwoFAByUserId(twoFA.UserId)
	require.NoError(t, err)
	assert.Equal(t, 1, stored.FailedAttempts)
	count, err := GetUnusedBackupCodeCount(twoFA.UserId)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestPasswordUpdateAndResetRevokeExistingSessions(t *testing.T) {
	setupTwoFASecurityTest(t)
	accessToken := "dashboard-access-token"
	user := User{
		Username:    "session-user",
		Password:    "OldPassword123",
		Status:      common.UserStatusEnabled,
		Email:       "session@example.com",
		AccessToken: &accessToken,
	}
	require.NoError(t, user.Insert(0))
	assert.Zero(t, user.SessionVersion)

	user.Password = "NewPassword123"
	require.NoError(t, user.Update(true))
	assert.Equal(t, int64(1), user.SessionVersion)
	assert.Empty(t, user.GetAccessToken())
	browserSessionID, err := CreateBrowserSession(user.Id, time.Now().Unix())
	require.NoError(t, err)

	require.NoError(t, ResetUserPasswordByEmail(user.Email, "ResetPassword123"))
	stored, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, int64(2), stored.SessionVersion)
	assert.Empty(t, stored.GetAccessToken())
	resolved, err := GetUserByBrowserSession(
		user.Id,
		browserSessionID,
		time.Now().Unix(),
	)
	require.NoError(t, err)
	assert.Nil(t, resolved)
}
