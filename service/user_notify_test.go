package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNotifyRootUserHandlesMissingRootAccount(t *testing.T) {
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
	})

	require.NotPanics(t, func() {
		NotifyRootUser("test", "subject", "content")
	})
}

func TestNotifyUserRejectsUnsupportedStoredNotificationType(t *testing.T) {
	originalRedisEnabled := common.RedisEnabled
	originalLimit := constant.NotifyLimitCount
	originalDuration := constant.NotificationLimitDurationMinute
	common.RedisEnabled = false
	constant.NotifyLimitCount = 1
	constant.NotificationLimitDurationMinute = 10
	t.Cleanup(func() {
		common.RedisEnabled = originalRedisEnabled
		constant.NotifyLimitCount = originalLimit
		constant.NotificationLimitDurationMinute = originalDuration
	})

	err := NotifyUser(
		987654,
		"user@example.com",
		dto.UserSetting{NotifyType: "legacy-invalid-type"},
		dto.NewNotify("quota", "subject", "content", nil),
	)

	require.Error(t, err)
	assert.ErrorContains(t, err, "unsupported notification type")
}
