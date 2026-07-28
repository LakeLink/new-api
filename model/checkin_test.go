package model

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupCheckinTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "checkin.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Checkin{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)

	oldDB := DB
	oldRedisEnabled := common.RedisEnabled
	DB = db
	common.RedisEnabled = false
	setting := operation_setting.GetCheckinSetting()
	oldSetting := *setting
	t.Cleanup(func() {
		*setting = oldSetting
		DB = oldDB
		common.RedisEnabled = oldRedisEnabled
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestUserCheckinRejectsInvalidQuotaConfiguration(t *testing.T) {
	db := setupCheckinTestDB(t)
	user := User{Username: "checkin-invalid", Password: "password", Quota: 100, AffCode: "checkin-invalid-aff"}
	require.NoError(t, db.Create(&user).Error)

	setting := operation_setting.GetCheckinSetting()
	setting.Enabled = true
	for _, quotaRange := range [][2]int{{-1, 10}, {10, 9}, {0, common.MaxQuota + 1}} {
		setting.MinQuota = quotaRange[0]
		setting.MaxQuota = quotaRange[1]
		_, err := UserCheckin(user.Id)
		require.ErrorContains(t, err, "配置无效")
	}

	var count int64
	require.NoError(t, db.Model(&Checkin{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestUserCheckinRollsBackWhenQuotaWouldOverflow(t *testing.T) {
	db := setupCheckinTestDB(t)
	user := User{Username: "checkin-overflow", Password: "password", Quota: common.MaxQuota - 5, AffCode: "checkin-overflow-aff"}
	require.NoError(t, db.Create(&user).Error)

	setting := operation_setting.GetCheckinSetting()
	setting.Enabled = true
	setting.MinQuota = 10
	setting.MaxQuota = 10
	_, err := UserCheckin(user.Id)
	require.Error(t, err)

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, common.MaxQuota-5, user.Quota)
	var count int64
	require.NoError(t, db.Model(&Checkin{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestUserCheckinCreditsQuotaExactlyOnce(t *testing.T) {
	db := setupCheckinTestDB(t)
	user := User{Username: "checkin-success", Password: "password", Quota: 100, AffCode: "checkin-success-aff"}
	require.NoError(t, db.Create(&user).Error)

	setting := operation_setting.GetCheckinSetting()
	setting.Enabled = true
	setting.MinQuota = 10
	setting.MaxQuota = 10
	_, err := UserCheckin(user.Id)
	require.NoError(t, err)
	_, err = UserCheckin(user.Id)
	require.Error(t, err)

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 110, user.Quota)
	var count int64
	require.NoError(t, db.Model(&Checkin{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestGetUserCheckinStatsPropagatesAggregateQueryErrors(t *testing.T) {
	db := setupCheckinTestDB(t)
	user := User{
		Username: "checkin-stats-error",
		Password: "password",
		AffCode:  "checkin-stats-error-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		"test:fail_checkin_count",
		func(tx *gorm.DB) {
			if _, ok := tx.Statement.Dest.(*int64); ok {
				_ = tx.AddError(errors.New("injected check-in aggregate failure"))
			}
		},
	))

	stats, err := GetUserCheckinStats(user.Id, "2026-07")
	require.ErrorContains(t, err, "injected check-in aggregate failure")
	assert.Nil(t, stats)
}
