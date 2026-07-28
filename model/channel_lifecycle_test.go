package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func channelLifecycleString(value string) *string {
	return &value
}

func createLifecycleChannel(t *testing.T, dbChannel *Channel) {
	t.Helper()
	require.NoError(t, DB.Create(dbChannel).Error)
}

func TestChannelDeleteBlockedUntilAsyncTaskIsTerminal(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Channel{}, &Ability{}, &Task{}, &Midjourney{})
	channel := &Channel{
		Id:          7101,
		Type:        constant.ChannelTypeKling,
		Key:         "delete-guard-key",
		Name:        "delete-guard",
		Status:      common.ChannelStatusEnabled,
		CreatedTime: 710100,
	}
	createLifecycleChannel(t, channel)
	require.NoError(t, db.Create(&Ability{ChannelId: channel.Id, Model: "kling-test", Group: "default", Enabled: true}).Error)
	task := &Task{
		TaskID:    "delete-guard-task",
		Platform:  constant.TaskPlatform("kling"),
		ChannelId: channel.Id,
		Status:    TaskStatusSubmitted,
	}
	require.NoError(t, db.Create(task).Error)

	err := channel.Delete()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrChannelLifecycleBlocked)

	var channelCount int64
	require.NoError(t, db.Model(&Channel{}).Where("id = ?", channel.Id).Count(&channelCount).Error)
	assert.Equal(t, int64(1), channelCount)
	var abilityCount int64
	require.NoError(t, db.Model(&Ability{}).Where("channel_id = ?", channel.Id).Count(&abilityCount).Error)
	assert.Equal(t, int64(1), abilityCount)

	require.NoError(t, db.Model(task).Update("status", TaskStatusSuccess).Error)
	require.NoError(t, channel.Delete())
	require.NoError(t, db.Model(&Channel{}).Where("id = ?", channel.Id).Count(&channelCount).Error)
	assert.Zero(t, channelCount)
	require.NoError(t, db.Model(&Ability{}).Where("channel_id = ?", channel.Id).Count(&abilityCount).Error)
	assert.Zero(t, abilityCount)
}

func TestBatchDeleteChannelsIsAtomicWhenOneChannelHasPendingTask(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Channel{}, &Ability{}, &Task{}, &Midjourney{})
	blocked := &Channel{Id: 7201, Type: constant.ChannelTypeKling, Key: "blocked", Name: "blocked", Status: common.ChannelStatusEnabled}
	free := &Channel{Id: 7202, Type: constant.ChannelTypeKling, Key: "free", Name: "free", Status: common.ChannelStatusEnabled}
	createLifecycleChannel(t, blocked)
	createLifecycleChannel(t, free)
	require.NoError(t, db.Create(&Task{
		TaskID:    "batch-delete-guard-task",
		Platform:  constant.TaskPlatform("kling"),
		ChannelId: blocked.Id,
		Status:    TaskStatusInProgress,
	}).Error)

	err := BatchDeleteChannels([]int{blocked.Id, free.Id})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrChannelLifecycleBlocked)

	var count int64
	require.NoError(t, db.Model(&Channel{}).Where("id IN ?", []int{blocked.Id, free.Id}).Count(&count).Error)
	assert.Equal(t, int64(2), count, "the free channel must not be partially deleted")
}

func TestStatusChannelDeletionIsAtomicWhenMidjourneyTaskIsPending(t *testing.T) {
	tests := []struct {
		name          string
		blockedStatus int
		freeStatus    int
		delete        func() (int64, error)
	}{
		{
			name:          "specific status",
			blockedStatus: common.ChannelStatusManuallyDisabled,
			freeStatus:    common.ChannelStatusManuallyDisabled,
			delete: func() (int64, error) {
				return DeleteChannelByStatus(int64(common.ChannelStatusManuallyDisabled))
			},
		},
		{
			name:          "all disabled",
			blockedStatus: common.ChannelStatusManuallyDisabled,
			freeStatus:    common.ChannelStatusAutoDisabled,
			delete:        DeleteDisabledChannel,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupBillingAdjustmentTestDB(t, &Channel{}, &Ability{}, &Task{}, &Midjourney{})
			blocked := &Channel{
				Id:     7301 + index*10,
				Type:   constant.ChannelTypeMidjourney,
				Key:    "blocked",
				Name:   "blocked",
				Status: test.blockedStatus,
			}
			free := &Channel{
				Id:     blocked.Id + 1,
				Type:   constant.ChannelTypeMidjourney,
				Key:    "free",
				Name:   "free",
				Status: test.freeStatus,
			}
			createLifecycleChannel(t, blocked)
			createLifecycleChannel(t, free)
			task := &Midjourney{
				MjId:      "pending-midjourney-task",
				ChannelId: blocked.Id,
				Status:    "IN_PROGRESS",
				Progress:  "40%",
			}
			require.NoError(t, db.Create(task).Error)

			deleted, err := test.delete()
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrChannelLifecycleBlocked)
			assert.Zero(t, deleted)

			var count int64
			require.NoError(t, db.Model(&Channel{}).Where("id IN ?", []int{blocked.Id, free.Id}).Count(&count).Error)
			assert.Equal(t, int64(2), count)

			require.NoError(t, db.Model(task).Updates(map[string]any{
				"status":   "SUCCESS",
				"progress": "100%",
			}).Error)
			deleted, err = test.delete()
			require.NoError(t, err)
			assert.Equal(t, int64(2), deleted)
		})
	}
}

func TestChannelIdentityUpdateBlockedWhileAsyncTaskIsPending(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Channel{}, &Ability{}, &Task{}, &Midjourney{})
	channel := &Channel{
		Id:                 7401,
		Type:               constant.ChannelTypeKling,
		Key:                "original-key",
		OpenAIOrganization: channelLifecycleString("original-org"),
		Name:               "identity-guard",
		Status:             common.ChannelStatusEnabled,
		CreatedTime:        740100,
		BaseURL:            nil,
		Other:              "original-other",
		Setting:            channelLifecycleString(`{"proxy":""}`),
		OtherSettings:      `{"disable_task_polling_sleep":false}`,
		HeaderOverride:     channelLifecycleString(`{"X-Tenant":"original"}`),
		ParamOverride:      channelLifecycleString(`{"tenant":"original"}`),
	}
	createLifecycleChannel(t, channel)
	task := &Task{
		TaskID:    "identity-guard-task",
		Platform:  constant.TaskPlatform("kling"),
		ChannelId: channel.Id,
		Status:    TaskStatusInProgress,
	}
	require.NoError(t, db.Create(task).Error)

	tests := []struct {
		name   string
		mutate func(*Channel)
	}{
		{name: "valid type zero", mutate: func(candidate *Channel) { candidate.Type = constant.ChannelTypeUnknown }},
		{name: "base URL", mutate: func(candidate *Channel) { candidate.BaseURL = channelLifecycleString("https://replacement.example") }},
		{name: "key", mutate: func(candidate *Channel) { candidate.Key = "replacement-key" }},
		{name: "provider other", mutate: func(candidate *Channel) { candidate.Other = "replacement-other" }},
		{name: "organization", mutate: func(candidate *Channel) { candidate.OpenAIOrganization = channelLifecycleString("replacement-org") }},
		{name: "provider settings", mutate: func(candidate *Channel) { candidate.OtherSettings = `{"disable_task_polling_sleep":true}` }},
		{name: "channel setting", mutate: func(candidate *Channel) {
			candidate.Setting = channelLifecycleString(`{"proxy":"http://replacement.example"}`)
		}},
		{name: "header override", mutate: func(candidate *Channel) {
			candidate.HeaderOverride = channelLifecycleString(`{"X-Tenant":"replacement"}`)
		}},
		{name: "parameter override", mutate: func(candidate *Channel) { candidate.ParamOverride = channelLifecycleString(`{"tenant":"replacement"}`) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var candidate Channel
			require.NoError(t, db.First(&candidate, channel.Id).Error)
			test.mutate(&candidate)

			err := candidate.Update()
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrChannelLifecycleBlocked))

			var persisted Channel
			require.NoError(t, db.First(&persisted, channel.Id).Error)
			assert.Equal(t, channel.Type, persisted.Type)
			assert.Equal(t, channel.Key, persisted.Key)
			assert.Equal(t, channel.GetBaseURL(), persisted.GetBaseURL())
			assert.Equal(t, channel.OpenAIOrganization, persisted.OpenAIOrganization)
			assert.Equal(t, channel.Other, persisted.Other)
			assert.Equal(t, channel.Setting, persisted.Setting)
			assert.Equal(t, channel.OtherSettings, persisted.OtherSettings)
			assert.Equal(t, channel.HeaderOverride, persisted.HeaderOverride)
			assert.Equal(t, channel.ParamOverride, persisted.ParamOverride)
		})
	}

	require.NoError(t, db.Model(task).Update("status", TaskStatusSuccess).Error)
	require.NoError(t, db.First(channel, channel.Id).Error)
	channel.Type = constant.ChannelTypeUnknown
	require.NoError(t, channel.Update())
	require.NoError(t, db.First(channel, channel.Id).Error)
	assert.Equal(t, constant.ChannelTypeUnknown, channel.Type, "type 0 must be persisted rather than omitted by GORM")
}
