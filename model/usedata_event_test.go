package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordQuotaDataEventIsIdempotentAndRejectsConflicts(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &QuotaData{})
	params := QuotaDataLogParams{
		UserID:    101,
		Username:  "analytics-user",
		ModelName: "analytics-model",
		Quota:     77,
		CreatedAt: 7_234,
		TokenUsed: 33,
		UseGroup:  "default",
		TokenID:   202,
		ChannelID: 303,
		NodeName:  "node-a",
	}

	require.NoError(t, RecordQuotaDataEvent("billing-event-1", params))
	require.NoError(t, RecordQuotaDataEvent("billing-event-1", params))

	var rows []QuotaData
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(7_200), rows[0].CreatedAt)
	assert.Equal(t, 1, rows[0].Count)
	assert.Equal(t, 77, rows[0].Quota)
	assert.Equal(t, 33, rows[0].TokenUsed)

	conflict := params
	conflict.Quota++
	require.ErrorContains(t, RecordQuotaDataEvent("billing-event-1", conflict), "different analytics")
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, 77, rows[0].Quota)
}

func TestCachedQuotaAggregationDoesNotMutateDurableEventRow(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &QuotaData{})
	params := QuotaDataLogParams{
		UserID:    111,
		Username:  "mixed-analytics-user",
		ModelName: "mixed-analytics-model",
		Quota:     10,
		CreatedAt: 7_234,
		TokenUsed: 5,
		UseGroup:  "default",
		TokenID:   222,
		ChannelID: 333,
		NodeName:  "node-a",
	}
	require.NoError(t, RecordQuotaDataEvent("billing-event-mixed", params))

	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		CacheQuotaDataLock.Lock()
		CacheQuotaData = make(map[string]*QuotaData)
		CacheQuotaDataLock.Unlock()
	})
	LogQuotaData(params)
	SaveQuotaDataCache()

	var event QuotaData
	require.NoError(t, db.Where("billing_event_id = ?", "billing-event-mixed").First(&event).Error)
	assert.Equal(t, 1, event.Count)
	assert.Equal(t, 10, event.Quota)
	assert.Equal(t, 5, event.TokenUsed)

	var aggregate QuotaData
	require.NoError(t, db.Where("billing_event_id IS NULL").First(&aggregate).Error)
	assert.Equal(t, 1, aggregate.Count)
	assert.Equal(t, 10, aggregate.Quota)
	assert.Equal(t, 5, aggregate.TokenUsed)
}

func TestSaveQuotaDataCacheRetainsFailedRowsForRetry(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &QuotaData{})
	params := QuotaDataLogParams{
		UserID:    121,
		Username:  "retry-user",
		ModelName: "retry-model",
		Quota:     15,
		CreatedAt: 7_234,
		TokenUsed: 6,
		UseGroup:  "default",
		TokenID:   232,
		ChannelID: 343,
		NodeName:  "node-retry",
	}

	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		CacheQuotaDataLock.Lock()
		CacheQuotaData = make(map[string]*QuotaData)
		CacheQuotaDataLock.Unlock()
	})
	LogQuotaData(params)
	require.NoError(t, db.Migrator().DropTable(&QuotaData{}))

	SaveQuotaDataCache()

	CacheQuotaDataLock.Lock()
	assert.Len(t, CacheQuotaData, 1)
	CacheQuotaDataLock.Unlock()

	require.NoError(t, db.AutoMigrate(&QuotaData{}))
	SaveQuotaDataCache()

	CacheQuotaDataLock.Lock()
	assert.Empty(t, CacheQuotaData)
	CacheQuotaDataLock.Unlock()
	var persisted QuotaData
	require.NoError(t, db.Where("billing_event_id IS NULL").First(&persisted).Error)
	assert.Equal(t, 15, persisted.Quota)
	assert.Equal(t, 6, persisted.TokenUsed)
}
