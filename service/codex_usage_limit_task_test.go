package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupCodexUsageLimitTaskTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	oldDB := model.DB
	oldMainDatabaseType := common.MainDatabaseType()
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.LogDatabaseType())
	common.MemoryCacheEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))

	t.Cleanup(func() {
		model.DB = oldDB
		common.SetDatabaseTypes(oldMainDatabaseType, common.LogDatabaseType())
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestCodexWeeklyUsageRemainingPercent(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		remaining float64
		ok        bool
	}{
		{
			name:      "selects weekly window",
			body:      `{"rate_limit":{"primary_window":{"used_percent":40,"limit_window_seconds":18000},"secondary_window":{"used_percent":93,"limit_window_seconds":604800}}}`,
			remaining: 7,
			ok:        true,
		},
		{
			name: "clamps malformed percentage",
			body: `{"rate_limit":{"primary_window":{"used_percent":120,"limit_window_seconds":604800}}}`,
			ok:   true,
		},
		{
			name: "requires weekly window",
			body: `{"rate_limit":{"primary_window":{"used_percent":40,"limit_window_seconds":18000}}}`,
			ok:   false,
		},
		{
			name: "requires usage percentage",
			body: `{"rate_limit":{"primary_window":{"limit_window_seconds":604800}}}`,
			ok:   false,
		},
		{
			name: "rejects invalid payload",
			body: `not json`,
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remaining, ok := codexWeeklyUsageRemainingPercent([]byte(tt.body))
			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, tt.remaining, remaining)
			}
		})
	}
}

func TestCodexUsageLimitCheckPausesAndResumesOnlyMarkedChannels(t *testing.T) {
	db := setupCodexUsageLimitTaskTestDB(t)

	oldMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = oldMaster })
	setting := `{"codex_auto_pause_weekly_limit_enabled":true,"codex_auto_pause_weekly_limit_threshold":10}`
	key := `{"access_token":"access-token","account_id":"account-id"}`
	channels := []*model.Channel{
		{
			Type:    constant.ChannelTypeCodex,
			Key:     key,
			Name:    "pause",
			Status:  common.ChannelStatusEnabled,
			Models:  "gpt-5",
			Group:   "default",
			Setting: &setting,
			BaseURL: common.GetPointer("https://chatgpt.example"),
		},
		{
			Type:      constant.ChannelTypeCodex,
			Key:       key,
			Name:      "resume",
			Status:    common.ChannelStatusAutoDisabled,
			Models:    "gpt-5",
			Group:     "default",
			Setting:   &setting,
			BaseURL:   common.GetPointer("https://chatgpt.example"),
			OtherInfo: `{"status_reason":"Codex weekly usage remaining is below the configured auto-pause threshold"}`,
		},
		{
			Type:      constant.ChannelTypeCodex,
			Key:       key,
			Name:      "keep-disabled",
			Status:    common.ChannelStatusAutoDisabled,
			Models:    "gpt-5",
			Group:     "default",
			Setting:   &setting,
			BaseURL:   common.GetPointer("https://chatgpt.example"),
			OtherInfo: `{"status_reason":"upstream error"}`,
		},
	}
	for _, channel := range channels {
		require.NoError(t, db.Create(channel).Error)
	}

	previousFetcher := codexUsageFetcher
	codexUsageFetcher = func(_ context.Context, _ *http.Client, _ string, _ string, _ string) (int, []byte, error) {
		return http.StatusOK, []byte(`{"rate_limit":{"primary_window":{"used_percent":95,"limit_window_seconds":604800}}}`), nil
	}
	t.Cleanup(func() {
		codexUsageFetcher = previousFetcher
	})

	runCodexUsageLimitCheckOnce()

	var paused, resumed, keptDisabled model.Channel
	require.NoError(t, db.First(&paused, channels[0].Id).Error)
	require.NoError(t, db.First(&resumed, channels[1].Id).Error)
	require.NoError(t, db.First(&keptDisabled, channels[2].Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, paused.Status)
	assert.Equal(t, codexUsageLimitAutoPauseReason, paused.GetOtherInfo()["status_reason"])
	assert.Equal(t, common.ChannelStatusAutoDisabled, resumed.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, keptDisabled.Status)

	codexUsageFetcher = func(_ context.Context, _ *http.Client, _ string, _ string, _ string) (int, []byte, error) {
		return http.StatusOK, []byte(`{"rate_limit":{"primary_window":{"used_percent":90,"limit_window_seconds":604800}}}`), nil
	}
	runCodexUsageLimitCheckOnce()

	require.NoError(t, db.First(&paused, channels[0].Id).Error)
	require.NoError(t, db.First(&resumed, channels[1].Id).Error)
	require.NoError(t, db.First(&keptDisabled, channels[2].Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, paused.Status)
	assert.Equal(t, common.ChannelStatusEnabled, resumed.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, keptDisabled.Status)
}
