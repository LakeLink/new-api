package model

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetBatchUpdateStores(t *testing.T) {
	t.Helper()
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		batchUpdateStores[i] = make(map[int]int64)
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		for i := 0; i < BatchUpdateTypeCount; i++ {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = make(map[int]int64)
			batchUpdateLocks[i].Unlock()
		}
	})
}

func TestBatchMetricFlushSaturatesQueuedOverflowAndUnderflow(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Channel{})
	resetBatchUpdateStores(t)

	overflowUser := User{
		Username:     "batch-overflow-user",
		Password:     "password",
		Quota:        100,
		UsedQuota:    common.MaxQuota - 2,
		RequestCount: common.MaxQuota - 1,
		AffCode:      "batch-overflow-aff",
	}
	require.NoError(t, db.Create(&overflowUser).Error)
	overflowChannel := Channel{
		Name:      "batch-overflow-channel",
		Key:       "test",
		UsedQuota: math.MaxInt64 - 2,
	}
	require.NoError(t, db.Create(&overflowChannel).Error)

	// Multiple individually valid request deltas can exceed the int32 database
	// range before a scheduled flush. They must never wrap into negative usage.
	addNewRecord(BatchUpdateTypeUsedQuota, overflowUser.Id, common.MaxQuota)
	addNewRecord(BatchUpdateTypeUsedQuota, overflowUser.Id, common.MaxQuota)
	addNewRecord(BatchUpdateTypeRequestCount, overflowUser.Id, 10)
	addNewRecord(BatchUpdateTypeChannelUsedQuota, overflowChannel.Id, 10)
	require.NoError(t, FlushBatchUpdates())

	require.NoError(t, db.First(&overflowUser, overflowUser.Id).Error)
	assert.Equal(t, common.MaxQuota, overflowUser.UsedQuota)
	assert.Equal(t, common.MaxQuota, overflowUser.RequestCount)
	assert.Equal(t, 100, overflowUser.Quota)
	require.NoError(t, db.First(&overflowChannel, overflowChannel.Id).Error)
	assert.Equal(t, int64(math.MaxInt64), overflowChannel.UsedQuota)

	underflowUser := User{
		Username:     "batch-underflow-user",
		Password:     "password",
		Quota:        100,
		UsedQuota:    3,
		RequestCount: 2,
		AffCode:      "batch-underflow-aff",
	}
	require.NoError(t, db.Create(&underflowUser).Error)
	underflowChannel := Channel{
		Name:      "batch-underflow-channel",
		Key:       "test",
		UsedQuota: 4,
	}
	require.NoError(t, db.Create(&underflowChannel).Error)

	addBatchUpdateDelta(BatchUpdateTypeUsedQuota, underflowUser.Id, math.MinInt64)
	addBatchUpdateDelta(BatchUpdateTypeRequestCount, underflowUser.Id, -10)
	addBatchUpdateDelta(BatchUpdateTypeChannelUsedQuota, underflowChannel.Id, math.MinInt64)
	require.NoError(t, FlushBatchUpdates())

	require.NoError(t, db.First(&underflowUser, underflowUser.Id).Error)
	assert.Zero(t, underflowUser.UsedQuota)
	assert.Zero(t, underflowUser.RequestCount)
	assert.Equal(t, 100, underflowUser.Quota)
	require.NoError(t, db.First(&underflowChannel, underflowChannel.Id).Error)
	assert.Zero(t, underflowChannel.UsedQuota)
}

func TestImmediateMetricUpdatesSaturateOverflowAndUnderflow(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Channel{})
	originalBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.BatchUpdateEnabled = originalBatchUpdateEnabled
	})

	user := User{
		Username:     "immediate-overflow-user",
		Password:     "password",
		Quota:        100,
		UsedQuota:    common.MaxQuota - 2,
		RequestCount: common.MaxQuota,
		AffCode:      "immediate-overflow-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	channel := Channel{
		Name:      "immediate-overflow-channel",
		Key:       "test",
		UsedQuota: math.MaxInt64 - 2,
	}
	require.NoError(t, db.Create(&channel).Error)

	UpdateUserUsedQuotaAndRequestCount(user.Id, 10)
	UpdateChannelUsedQuota(channel.Id, 10)

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, common.MaxQuota, user.UsedQuota)
	assert.Equal(t, common.MaxQuota, user.RequestCount)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	assert.Equal(t, int64(math.MaxInt64), channel.UsedQuota)

	user.UsedQuota = 3
	require.NoError(t, db.Model(&user).Update("used_quota", user.UsedQuota).Error)
	channel.UsedQuota = 4
	require.NoError(t, db.Model(&channel).Update("used_quota", channel.UsedQuota).Error)

	UpdateUserUsedQuotaAndRequestCount(user.Id, -10)
	UpdateChannelUsedQuota(channel.Id, -10)

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Zero(t, user.UsedQuota)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	assert.Zero(t, channel.UsedQuota)
}

func TestQuotaDataAggregationSaturatesCacheAndDatabaseValues(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &QuotaData{})
	CacheQuotaDataLock.Lock()
	oldCache := CacheQuotaData
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		CacheQuotaDataLock.Lock()
		CacheQuotaData = oldCache
		CacheQuotaDataLock.Unlock()
	})

	base := &QuotaData{
		UserID:    77,
		Username:  "quota-overflow-user",
		ModelName: "quota-overflow-model",
		CreatedAt: 7_200,
		UseGroup:  "default",
		TokenID:   88,
		ChannelID: 99,
		NodeName:  "quota-overflow-node",
		Count:     common.MaxQuota - 1,
		Quota:     common.MaxQuota - 2,
		TokenUsed: common.MaxQuota - 3,
	}
	CacheQuotaDataLock.Lock()
	logQuotaDataCache(base)
	logQuotaDataCache(&QuotaData{
		UserID:    base.UserID,
		Username:  base.Username,
		ModelName: base.ModelName,
		CreatedAt: base.CreatedAt,
		UseGroup:  base.UseGroup,
		TokenID:   base.TokenID,
		ChannelID: base.ChannelID,
		NodeName:  base.NodeName,
		Count:     10,
		Quota:     10,
		TokenUsed: 10,
	})
	require.Len(t, CacheQuotaData, 1)
	for _, cached := range CacheQuotaData {
		assert.Equal(t, common.MaxQuota, cached.Count)
		assert.Equal(t, common.MaxQuota, cached.Quota)
		assert.Equal(t, common.MaxQuota, cached.TokenUsed)
	}
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	persisted := *base
	persisted.Id = 0
	persisted.Count = common.MaxQuota - 1
	persisted.Quota = common.MaxQuota - 2
	persisted.TokenUsed = common.MaxQuota - 3
	require.NoError(t, db.Create(&persisted).Error)

	LogQuotaData(QuotaDataLogParams{
		UserID:    base.UserID,
		Username:  base.Username,
		ModelName: base.ModelName,
		CreatedAt: base.CreatedAt,
		TokenUsed: 10,
		Quota:     10,
		UseGroup:  base.UseGroup,
		TokenID:   base.TokenID,
		ChannelID: base.ChannelID,
		NodeName:  base.NodeName,
	})
	SaveQuotaDataCache()

	require.NoError(t, db.First(&persisted, persisted.Id).Error)
	assert.Equal(t, common.MaxQuota, persisted.Count)
	assert.Equal(t, common.MaxQuota, persisted.Quota)
	assert.Equal(t, common.MaxQuota, persisted.TokenUsed)
}
