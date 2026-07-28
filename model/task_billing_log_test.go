package model

import (
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCreateBillingEventLogConcurrentReplayIsExactlyOnce(t *testing.T) {
	oldLogDB := LOG_DB
	oldLogDatabaseType := common.LogDatabaseType()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)", url.QueryEscape(t.Name()))
	logDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := logDB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	require.NoError(t, logDB.AutoMigrate(&Log{}))
	LOG_DB = logDB
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		LOG_DB = oldLogDB
		common.SetLogDatabaseType(oldLogDatabaseType)
		require.NoError(t, sqlDB.Close())
	})

	// Existing and ordinary logs have a NULL billing event identity, so the
	// unique index must not prevent unrelated historical rows from coexisting.
	require.NoError(t, logDB.Create(&Log{RequestId: "ordinary-1"}).Error)
	require.NoError(t, logDB.Create(&Log{RequestId: "ordinary-2"}).Error)

	const writers = 8
	const eventID = "task-billing-event-concurrent"
	start := make(chan struct{})
	errorsByWriter := make([]error, writers)
	var createdCount atomic.Int32
	var waitGroup sync.WaitGroup
	waitGroup.Add(writers)
	for i := 0; i < writers; i++ {
		go func(index int) {
			defer waitGroup.Done()
			<-start
			billingEventID := eventID
			created, createErr := createBillingEventLog(&Log{
				UserId:         17,
				Type:           LogTypeConsume,
				ChannelId:      23,
				ModelName:      "concurrent-model",
				Quota:          31,
				TokenId:        47,
				Group:          "default",
				RequestId:      eventID,
				BillingEventId: &billingEventID,
			})
			errorsByWriter[index] = createErr
			if created {
				createdCount.Add(1)
			}
		}(i)
	}
	close(start)
	waitGroup.Wait()

	for _, writerErr := range errorsByWriter {
		require.NoError(t, writerErr)
	}
	assert.Equal(t, int32(1), createdCount.Load())
	var eventCount int64
	require.NoError(t, logDB.Model(&Log{}).Where("billing_event_id = ?", eventID).Count(&eventCount).Error)
	assert.Equal(t, int64(1), eventCount)

	billingEventID := eventID
	created, err := createBillingEventLog(&Log{
		UserId:         17,
		Type:           LogTypeConsume,
		ChannelId:      23,
		ModelName:      "concurrent-model",
		Quota:          31,
		TokenId:        47,
		Group:          "default",
		RequestId:      eventID,
		BillingEventId: &billingEventID,
	})
	require.NoError(t, err)
	assert.False(t, created, "a deterministic replay must reuse the committed billing event")

	renamedEventID := eventID
	created, err = createBillingEventLog(&Log{
		UserId:         17,
		Type:           LogTypeConsume,
		Username:       "renamed-user",
		ChannelId:      23,
		ModelName:      "concurrent-model",
		Quota:          31,
		TokenId:        47,
		TokenName:      "renamed-token",
		Group:          "default",
		RequestId:      eventID,
		BillingEventId: &renamedEventID,
	})
	require.NoError(t, err)
	assert.False(t, created, "descriptive name changes must not poison accounting replay")

	conflictingEventID := eventID
	_, err = createBillingEventLog(&Log{
		UserId:         17,
		Type:           LogTypeConsume,
		ChannelId:      23,
		ModelName:      "concurrent-model",
		Quota:          32,
		TokenId:        47,
		Group:          "default",
		RequestId:      eventID,
		BillingEventId: &conflictingEventID,
	})
	assert.ErrorContains(t, err, "different accounting context")
}

func TestTaskBillingLogReplayUsesCanonicalQuotaDataDimensions(t *testing.T) {
	truncateTables(t)
	oldDataExportEnabled := common.DataExportEnabled
	common.DataExportEnabled = true
	t.Cleanup(func() {
		common.DataExportEnabled = oldDataExportEnabled
	})

	const eventID = "task-billing-canonical-analytics"
	params := RecordTaskBillingLogParams{
		UserId:    41,
		LogType:   LogTypeConsume,
		Username:  "original-name",
		ModelName: "canonical-model",
		Quota:     23,
		Group:     "default",
		NodeName:  "node-a",
	}
	require.NoError(t, RecordTaskBillingLogWithRequestID(params, eventID, 3_601))

	retry := params
	retry.Username = "renamed-after-log-commit"
	require.NoError(t, RecordTaskBillingLogWithRequestID(retry, eventID, 7_201))

	var logs []Log
	require.NoError(t, DB.Where("billing_event_id = ?", eventID).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, "original-name", logs[0].Username)
	assert.Equal(t, int64(3_601), logs[0].CreatedAt)

	var events []QuotaData
	require.NoError(t, DB.Where("billing_event_id = ?", eventID).Find(&events).Error)
	require.Len(t, events, 1)
	assert.Equal(t, "original-name", events[0].Username)
	assert.Equal(t, int64(3_600), events[0].CreatedAt)
}

func TestClaimTaskBillingLogUsesOneDatabaseTimedLease(t *testing.T) {
	truncateTables(t)

	const finalizationID = "task-billing-log-lease"
	require.NoError(t, DB.Create(&TaskBillingFinalization{
		FinalizationID: finalizationID,
		Payload:        `{}`,
		MainStatus:     taskBillingFinalizationSucceeded,
		LogStatus:      taskBillingFinalizationPending,
	}).Error)

	const claimers = 8
	start := make(chan struct{})
	errorsByClaimer := make([]error, claimers)
	var claimCount atomic.Int32
	var waitGroup sync.WaitGroup
	waitGroup.Add(claimers)
	for i := 0; i < claimers; i++ {
		go func(index int) {
			defer waitGroup.Done()
			<-start
			_, claimed, claimErr := claimTaskBillingLog(finalizationID)
			errorsByClaimer[index] = claimErr
			if claimed {
				claimCount.Add(1)
			}
		}(i)
	}
	close(start)
	waitGroup.Wait()

	for _, claimErr := range errorsByClaimer {
		require.NoError(t, claimErr)
	}
	assert.Equal(t, int32(1), claimCount.Load())

	var record TaskBillingFinalization
	require.NoError(t, DB.Where("finalization_id = ?", finalizationID).First(&record).Error)
	databaseNow := GetDBTimestamp()
	assert.Equal(t, taskBillingFinalizationProcessing, record.LogStatus)
	assert.NotEmpty(t, record.LogClaimToken)
	assert.GreaterOrEqual(t, record.LogClaimedAt, databaseNow-1)
	assert.LessOrEqual(t, record.LogClaimedAt, databaseNow+1)

	_, claimed, err := claimTaskBillingLog(finalizationID)
	require.NoError(t, err)
	assert.False(t, claimed, "an unexpired database-timed lease must not be stolen")

	require.NoError(t, DB.Model(&TaskBillingFinalization{}).
		Where("finalization_id = ?", finalizationID).
		Update("log_claimed_at", databaseNow-taskBillingLogClaimSeconds).Error)
	_, claimed, err = claimTaskBillingLog(finalizationID)
	require.NoError(t, err)
	assert.True(t, claimed, "an expired database-timed lease must be reclaimable")
}
