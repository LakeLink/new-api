package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func isolateSetupRuntimeState(t *testing.T) {
	t.Helper()

	originalSetup := constant.Setup.Load()
	originalSelfUseMode := operation_setting.SelfUseModeEnabled
	originalDemoSite := operation_setting.DemoSiteEnabled
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		constant.Setup.Store(originalSetup)
		operation_setting.SelfUseModeEnabled = originalSelfUseMode
		operation_setting.DemoSiteEnabled = originalDemoSite
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})
}

func TestCheckSetupFailsClosedOnProbeErrors(t *testing.T) {
	t.Run("setup marker read", func(t *testing.T) {
		setupBillingAdjustmentTestDB(t, &User{})
		isolateSetupRuntimeState(t)
		constant.Setup.Store(false)

		err := CheckSetup()

		require.ErrorContains(t, err, "read setup marker")
		assert.True(t, constant.Setup.Load())
	})

	t.Run("root user read", func(t *testing.T) {
		setupBillingAdjustmentTestDB(t, &Setup{})
		isolateSetupRuntimeState(t)
		constant.Setup.Store(false)

		err := CheckSetup()

		require.ErrorContains(t, err, "check root user")
		assert.True(t, constant.Setup.Load())
	})
}

func TestCheckSetupOpensOnlyConfirmedFreshDatabase(t *testing.T) {
	setupBillingAdjustmentTestDB(t, &Setup{}, &User{})
	isolateSetupRuntimeState(t)
	constant.Setup.Store(true)

	require.NoError(t, CheckSetup())
	assert.False(t, constant.Setup.Load())
}

func TestInitializeSetupHasSingleConcurrentWinner(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Setup{}, &User{}, &Option{})
	isolateSetupRuntimeState(t)

	passwordHash, err := common.Password2Hash("SecurePassword123")
	require.NoError(t, err)
	rootUsers := []*User{
		{
			Username: "first-root", Password: passwordHash, Role: common.RoleRootUser,
			Status: common.UserStatusEnabled, DisplayName: "Root User", Quota: 100000000,
		},
		{
			Username: "second-root", Password: passwordHash, Role: common.RoleRootUser,
			Status: common.UserStatusEnabled, DisplayName: "Root User", Quota: 100000000,
		},
	}

	start := make(chan struct{})
	results := make(chan error, len(rootUsers))
	var workers sync.WaitGroup
	for _, rootUser := range rootUsers {
		workers.Add(1)
		go func(rootUser *User) {
			defer workers.Done()
			<-start
			results <- InitializeSetup(rootUser, true, false)
		}(rootUser)
	}
	close(start)
	workers.Wait()
	close(results)

	var succeeded, alreadyCompleted int
	for result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, ErrSetupAlreadyCompleted):
			alreadyCompleted++
		default:
			require.NoError(t, result)
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, alreadyCompleted)

	var setupCount, rootCount int64
	require.NoError(t, db.Model(&Setup{}).Count(&setupCount).Error)
	require.NoError(t, db.Model(&User{}).Where("role = ?", common.RoleRootUser).Count(&rootCount).Error)
	assert.EqualValues(t, 1, setupCount)
	assert.EqualValues(t, 1, rootCount)

	var options []Option
	require.NoError(t, db.Order("key").Find(&options).Error)
	assert.Equal(t, []Option{
		{Key: "DemoSiteEnabled", Value: "false"},
		{Key: "SelfUseModeEnabled", Value: "true"},
	}, options)
}

func TestInitializeSetupRejectsExistingSingletonClaim(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Setup{}, &User{}, &Option{})
	isolateSetupRuntimeState(t)
	existing := Setup{
		ID:            setupSingletonID,
		Version:       "existing-version",
		InitializedAt: 123,
		ClaimToken:    "existing-claim",
	}
	require.NoError(t, db.Create(&existing).Error)

	err := InitializeSetup(&User{
		Username: "unexpected-root", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled,
	}, true, true)

	require.ErrorIs(t, err, ErrSetupAlreadyCompleted)
	var persisted Setup
	require.NoError(t, db.First(&persisted, setupSingletonID).Error)
	assert.Equal(t, existing.Version, persisted.Version)
	assert.Equal(t, existing.InitializedAt, persisted.InitializedAt)
	assert.Equal(t, existing.ClaimToken, persisted.ClaimToken)
	var rootCount, optionCount int64
	require.NoError(t, db.Model(&User{}).Where("role = ?", common.RoleRootUser).Count(&rootCount).Error)
	require.NoError(t, db.Model(&Option{}).Count(&optionCount).Error)
	assert.Zero(t, rootCount)
	assert.Zero(t, optionCount)
}

func TestInitializeSetupRollsBackClaimWhenRootCreationFails(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Setup{}, &User{}, &Option{})
	isolateSetupRuntimeState(t)

	existing := User{Username: "duplicate-root", Password: "existing-password", Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&existing).Error)
	rootUser := &User{
		Username: " duplicate-root ", Password: "root-password", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled,
	}

	err := InitializeSetup(rootUser, true, true)

	require.ErrorContains(t, err, "create root user")
	var setupCount, optionCount int64
	require.NoError(t, db.Model(&Setup{}).Count(&setupCount).Error)
	require.NoError(t, db.Model(&Option{}).Count(&optionCount).Error)
	assert.Zero(t, setupCount)
	assert.Zero(t, optionCount)
}

func TestInitializeSetupRejectsBlankTrimmedRootUsername(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Setup{}, &User{}, &Option{})
	isolateSetupRuntimeState(t)

	err := InitializeSetup(&User{Username: " \t\n ", Role: common.RoleRootUser}, false, false)

	require.ErrorContains(t, err, "root username is required")
	var setupCount, rootCount int64
	require.NoError(t, db.Model(&Setup{}).Count(&setupCount).Error)
	require.NoError(t, db.Model(&User{}).Where("role = ?", common.RoleRootUser).Count(&rootCount).Error)
	assert.Zero(t, setupCount)
	assert.Zero(t, rootCount)
}
