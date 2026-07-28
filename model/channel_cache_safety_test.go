package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useChannelMemoryCache(t *testing.T) {
	t.Helper()
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		channelPollingLocks.Range(func(key, _ any) bool {
			channelPollingLocks.Delete(key)
			return true
		})
		InitChannelCache()
	})
}

func TestChannelCacheReturnsIndependentSnapshots(t *testing.T) {
	useChannelMemoryCache(t)

	organization := "original-organization"
	weight := uint(17)
	setting := `{"proxy":"https://proxy.example"}`
	channel := &Channel{
		Id:                 120001,
		Type:               constant.ChannelTypeOpenAI,
		Key:                "key-a\nkey-b",
		OpenAIOrganization: &organization,
		Weight:             &weight,
		Setting:            &setting,
		Status:             common.ChannelStatusEnabled,
		Name:               "snapshot-channel",
		Keys:               []string{"key-a", "key-b"},
		ChannelInfo: ChannelInfo{
			IsMultiKey:             true,
			MultiKeyStatusList:     map[int]int{1: common.ChannelStatusAutoDisabled},
			MultiKeyDisabledReason: map[int]string{1: "test"},
			MultiKeyDisabledTime:   map[int]int64{1: 10},
			MultiKeyMode:           constant.MultiKeyModePolling,
		},
	}
	CacheUpdateChannel(channel)

	// CacheUpdateChannel must not retain mutable memory owned by its caller.
	organization = "mutated-organization"
	weight = 99
	channel.Keys[0] = "mutated-key"
	channel.ChannelInfo.MultiKeyStatusList[0] = common.ChannelStatusAutoDisabled

	first, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, "original-organization", *first.OpenAIOrganization)
	assert.Equal(t, uint(17), *first.Weight)
	assert.Equal(t, []string{"key-a", "key-b"}, first.Keys)
	assert.NotContains(t, first.ChannelInfo.MultiKeyStatusList, 0)

	// Cache reads must not expose the canonical entry or any of its nested maps.
	*first.OpenAIOrganization = "request-mutation"
	*first.Weight = 123
	first.Keys[0] = "request-key"
	first.ChannelInfo.MultiKeyStatusList[0] = common.ChannelStatusManuallyDisabled
	first.ChannelInfo.MultiKeyDisabledReason[1] = "request-reason"
	first.ChannelInfo.MultiKeyDisabledTime[1] = 999

	second, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, "original-organization", *second.OpenAIOrganization)
	assert.Equal(t, uint(17), *second.Weight)
	assert.Equal(t, []string{"key-a", "key-b"}, second.Keys)
	assert.NotContains(t, second.ChannelInfo.MultiKeyStatusList, 0)
	assert.Equal(t, "test", second.ChannelInfo.MultiKeyDisabledReason[1])
	assert.Equal(t, int64(10), second.ChannelInfo.MultiKeyDisabledTime[1])

	info, err := CacheGetChannelInfo(channel.Id)
	require.NoError(t, err)
	info.MultiKeyStatusList[0] = common.ChannelStatusAutoDisabled
	third, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.NotContains(t, third.ChannelInfo.MultiKeyStatusList, 0)
}

func TestInitChannelCachePreservesLastGoodSnapshotOnQueryError(t *testing.T) {
	useChannelMemoryCache(t)
	truncateTables(t)

	channel := &Channel{
		Id:     120004,
		Type:   constant.ChannelTypeOpenAI,
		Status: common.ChannelStatusEnabled,
		Name:   "last-good-cache-channel",
		Models: "last-good-model",
		Group:  "default",
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, InitChannelCache())

	callbackName := "inject_channel_cache_query_failure"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			tx.AddError(errors.New("injected channel cache query failure"))
		},
	))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Query().Remove(callbackName))
	})

	err := InitChannelCache()
	require.ErrorContains(t, err, "injected channel cache query failure")

	cached, cacheErr := CacheGetChannel(channel.Id)
	require.NoError(t, cacheErr)
	assert.Equal(t, channel.Id, cached.Id)
}

func TestInitChannelCacheDoesNotRouteDisabledAbility(t *testing.T) {
	useChannelMemoryCache(t)
	truncateTables(t)

	channel := &Channel{
		Id:     120005,
		Type:   constant.ChannelTypeOpenAI,
		Status: common.ChannelStatusEnabled,
		Name:   "disabled-ability-channel",
		Models: "disabled-ability-model",
		Group:  "default",
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "disabled-ability-model",
		ChannelId: channel.Id,
		Enabled:   false,
	}).Error)
	require.NoError(t, InitChannelCache())

	selected, err := GetRandomSatisfiedChannel("default", "disabled-ability-model", 0, "")
	require.NoError(t, err)
	assert.Nil(t, selected)
}

func TestUpdateChannelStatusPublishesCacheOnlyAfterDatabaseCommit(t *testing.T) {
	useChannelMemoryCache(t)
	truncateTables(t)

	channel := &Channel{
		Id:     120006,
		Type:   constant.ChannelTypeOpenAI,
		Status: common.ChannelStatusEnabled,
		Name:   "atomic-status-channel",
		Models: "atomic-status-model",
		Group:  "default",
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.UpdateAbilities(nil))
	require.NoError(t, InitChannelCache())

	callbackName := "inject_channel_status_update_failure"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "channels" {
				tx.AddError(errors.New("injected channel status update failure"))
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(callbackName))
	})

	assert.False(t, UpdateChannelStatus(
		channel.Id,
		"",
		common.ChannelStatusAutoDisabled,
		"injected failure",
	))

	cached, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, cached.Status)

	var persisted Channel
	require.NoError(t, DB.First(&persisted, "id = ?", channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, persisted.Status)

	var ability Ability
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.True(t, ability.Enabled)
}

func TestPollingSnapshotsShareCanonicalRotationState(t *testing.T) {
	useChannelMemoryCache(t)
	truncateTables(t)

	channel := &Channel{
		Id:     120002,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		Name:   "polling-snapshot-channel",
		Models: "polling-model",
		Group:  "default",
		ChannelInfo: ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
			MultiKeyMode: constant.MultiKeyModePolling,
		},
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "polling-model",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error)
	InitChannelCache()

	first, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	second, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)

	firstKey, firstIndex, apiErr := first.GetNextEnabledKey()
	require.Nil(t, apiErr)
	secondKey, secondIndex, apiErr := second.GetNextEnabledKey()
	require.Nil(t, apiErr)
	assert.Equal(t, "key-a", firstKey)
	assert.Equal(t, 0, firstIndex)
	assert.Equal(t, "key-b", secondKey)
	assert.Equal(t, 1, secondIndex)
}

func TestUpdateChannelStatusPersistsMultiKeyReenableAndRestoresRouting(t *testing.T) {
	useChannelMemoryCache(t)
	truncateTables(t)

	channel := &Channel{
		Id:     120003,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		Name:   "multi-key-status-channel",
		Models: "status-model",
		Group:  "default",
		ChannelInfo: ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
			MultiKeyMode: constant.MultiKeyModePolling,
		},
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "status-model",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error)
	InitChannelCache()

	assert.True(t, UpdateChannelStatus(
		channel.Id,
		"key-a",
		common.ChannelStatusAutoDisabled,
		"upstream rejected the key",
	))
	var persisted Channel
	require.NoError(t, DB.First(&persisted, "id = ?", channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, persisted.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, persisted.ChannelInfo.MultiKeyStatusList[0])

	// The channel's overall status is already enabled. Re-enabling one key must
	// still persist the per-key change instead of returning early.
	assert.True(t, UpdateChannelStatus(
		channel.Id,
		"key-a",
		common.ChannelStatusEnabled,
		"",
	))
	require.NoError(t, DB.First(&persisted, "id = ?", channel.Id).Error)
	assert.NotContains(t, persisted.ChannelInfo.MultiKeyStatusList, 0)

	// Disabling every key removes the channel from routing; enabling a key must
	// restore it immediately without waiting for the periodic full cache sync.
	assert.True(t, UpdateChannelStatus(
		channel.Id,
		"key-a",
		common.ChannelStatusAutoDisabled,
		"disabled again",
	))
	assert.True(t, UpdateChannelStatus(
		channel.Id,
		"key-b",
		common.ChannelStatusAutoDisabled,
		"all keys disabled",
	))
	selected, err := GetRandomSatisfiedChannel("default", "status-model", 0, "")
	require.NoError(t, err)
	assert.Nil(t, selected)

	assert.True(t, UpdateChannelStatus(
		channel.Id,
		"key-b",
		common.ChannelStatusEnabled,
		"",
	))
	selected, err = GetRandomSatisfiedChannel("default", "status-model", 0, "")
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, channel.Id, selected.Id)
}

func TestInvalidChannelSettingsAreReadWithoutMutatingPersistenceState(t *testing.T) {
	invalidSetting := "{"
	channel := &Channel{
		Id:            120004,
		Setting:       &invalidSetting,
		OtherSettings: "{",
	}

	_ = channel.GetSetting()
	_ = channel.GetOtherSettings()

	require.NotNil(t, channel.Setting)
	assert.Equal(t, invalidSetting, *channel.Setting)
	assert.Equal(t, "{", channel.OtherSettings)
}
