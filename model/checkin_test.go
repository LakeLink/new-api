package model

import (
	"fmt"
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
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:checkin-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Checkin{}))

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
