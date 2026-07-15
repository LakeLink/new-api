package model

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBillingAdjustmentTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	oldDB := DB
	oldLogDB := LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldRedisClient := common.RDB
	oldBatchEnabled := common.BatchUpdateEnabled

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", url.QueryEscape(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	LOG_DB = db
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	require.NoError(t, db.AutoMigrate(models...))

	t.Cleanup(func() {
		DB = oldDB
		LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.RDB = oldRedisClient
		common.BatchUpdateEnabled = oldBatchEnabled
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestBillingAdjustmentWalletSettlementIsAtomicAndIdempotent(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &SystemTask{})
	user := User{Username: "billing-wallet", Password: "password", Quota: 1_000, AffCode: "wallet-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "wallet-token", RemainQuota: 500}
	require.NoError(t, db.Create(&token).Error)

	adjustment := BillingAdjustment{
		RequestID:     "request-wallet-settle",
		Kind:          BillingAdjustmentSettle,
		FundingSource: BillingAdjustmentWallet,
		UserID:        user.Id,
		TokenID:       token.Id,
		FundingDelta:  30,
		TokenDelta:    30,
	}
	taskID, err := EnqueueBillingAdjustment(adjustment)
	require.NoError(t, err)
	firstResult, err := ProcessBillingAdjustmentWithResult(taskID)
	require.NoError(t, err)
	assert.False(t, firstResult.AlreadyProcessed)
	secondResult, err := ProcessBillingAdjustmentWithResult(taskID)
	require.NoError(t, err)
	assert.True(t, secondResult.AlreadyProcessed)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 970, user.Quota)
	assert.Equal(t, 470, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)

	var task SystemTask
	require.NoError(t, db.Where("task_id = ?", taskID).First(&task).Error)
	assert.Equal(t, SystemTaskStatusSucceeded, task.Status)

	duplicateTaskID, err := EnqueueBillingAdjustment(adjustment)
	require.NoError(t, err)
	assert.Equal(t, taskID, duplicateTaskID)
	adjustment.TokenDelta++
	_, err = EnqueueBillingAdjustment(adjustment)
	assert.ErrorContains(t, err, "different payload")
}

func TestBillingAdjustmentFailureRollsBackAndRemainsPending(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &SystemTask{})
	user := User{Username: "billing-rollback", Password: "password", Quota: 1_000, AffCode: "rollback-aff"}
	require.NoError(t, db.Create(&user).Error)

	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:     "request-wallet-rollback",
		Kind:          BillingAdjustmentSettle,
		FundingSource: BillingAdjustmentWallet,
		UserID:        user.Id,
		TokenID:       999_999,
		FundingDelta:  40,
		TokenDelta:    40,
	})
	require.NoError(t, err)
	require.NoError(t, db.Migrator().DropTable(&Token{}))
	err = ProcessBillingAdjustment(taskID)
	require.Error(t, err)

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 1_000, user.Quota)
	var task SystemTask
	require.NoError(t, db.Where("task_id = ?", taskID).First(&task).Error)
	assert.Equal(t, SystemTaskStatusPending, task.Status)
	assert.NotEmpty(t, task.Error)
}

func TestBillingAdjustmentRejectsOutOfRangeDeltaBeforePersistence(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &SystemTask{})
	_, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:     "request-out-of-range-adjustment",
		Kind:          BillingAdjustmentSettle,
		FundingSource: BillingAdjustmentWallet,
		UserID:        1,
		FundingDelta:  common.MaxQuota + 1,
	})
	require.ErrorContains(t, err, "storage range")

	var count int64
	require.NoError(t, db.Model(&SystemTask{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestBillingAdjustmentSubscriptionRefundIsAtomicAndIdempotent(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &SystemTask{})
	user := User{Username: "billing-subscription", Password: "password", Quota: 100, AffCode: "subscription-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "subscription-token", RemainQuota: 380, UsedQuota: 120}
	require.NoError(t, db.Create(&token).Error)
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 1_000, AmountUsed: 150}
	require.NoError(t, db.Create(&subscription).Error)
	record := SubscriptionPreConsumeRecord{
		RequestId:          "request-subscription-refund",
		UserId:             user.Id,
		UserSubscriptionId: subscription.Id,
		PreConsumed:        100,
		Status:             "consumed",
	}
	require.NoError(t, db.Create(&record).Error)

	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:             record.RequestId,
		Kind:                  BillingAdjustmentRefund,
		FundingSource:         BillingAdjustmentSubscription,
		UserID:                user.Id,
		SubscriptionID:        subscription.Id,
		SubscriptionRequestID: record.RequestId,
		TokenID:               token.Id,
		FundingDelta:          -120,
		TokenDelta:            -120,
		ExtraReserved:         20,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessPendingBillingAdjustments(10))
	require.NoError(t, ProcessBillingAdjustment(taskID))

	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&record, record.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, int64(30), subscription.AmountUsed)
	assert.Equal(t, "refunded", record.Status)
	assert.Equal(t, 500, token.RemainQuota)
	assert.Equal(t, 0, token.UsedQuota)
}

func TestBillingAdjustmentSubscriptionSettlementSplitsWalletOverflow(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &UserSubscription{}, &SystemTask{})
	user := User{Username: "billing-subscription-overflow", Password: "password", Quota: 200, AffCode: "subscription-overflow-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "subscription-overflow-token", RemainQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	subscription := UserSubscription{
		UserId:              user.Id,
		AmountTotal:         100,
		AmountUsed:          90,
		AllowWalletOverflow: true,
	}
	require.NoError(t, db.Create(&subscription).Error)

	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      "request-subscription-overflow",
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         user.Id,
		SubscriptionID: subscription.Id,
		TokenID:        token.Id,
		FundingDelta:   30,
		TokenDelta:     30,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessPendingBillingAdjustments(10))
	require.NoError(t, ProcessBillingAdjustment(taskID))

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 180, user.Quota)
	assert.Equal(t, int64(100), subscription.AmountUsed)
	assert.Equal(t, 70, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)

	result, err := ProcessBillingAdjustmentWithResult(taskID)
	require.NoError(t, err)
	assert.Equal(t, BillingAdjustmentResult{
		SubscriptionDelta: 10,
		WalletDelta:       20,
		TokenDelta:        30,
		AlreadyProcessed:  true,
	}, result)
}

func TestBillingAdjustmentStrictSubscriptionRetainsOverage(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &UserSubscription{}, &SystemTask{})
	user := User{Username: "billing-strict-subscription", Password: "password", Quota: 200, AffCode: "strict-subscription-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "strict-subscription-token", RemainQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	subscription := UserSubscription{
		UserId:              user.Id,
		AmountTotal:         100,
		AmountUsed:          90,
		AllowWalletOverflow: false,
	}
	require.NoError(t, db.Create(&subscription).Error)

	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      "request-strict-subscription",
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         user.Id,
		SubscriptionID: subscription.Id,
		TokenID:        token.Id,
		FundingDelta:   30,
		TokenDelta:     30,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessBillingAdjustment(taskID))

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 200, user.Quota)
	assert.Equal(t, int64(120), subscription.AmountUsed)
	assert.Equal(t, 70, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)

	result, err := ProcessBillingAdjustmentWithResult(taskID)
	require.NoError(t, err)
	assert.Equal(t, BillingAdjustmentResult{
		SubscriptionDelta: 30,
		TokenDelta:        30,
		AlreadyProcessed:  true,
	}, result)
}

func TestBillingAdjustmentActiveStrictPlanBlocksOtherSubscriptionOverflow(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &UserSubscription{}, &SystemTask{})
	user := User{Username: "billing-mixed-subscriptions", Password: "password", Quota: 200, AffCode: "mixed-subscriptions-aff"}
	require.NoError(t, db.Create(&user).Error)
	selected := UserSubscription{
		UserId:              user.Id,
		AmountTotal:         100,
		AmountUsed:          90,
		AllowWalletOverflow: true,
	}
	require.NoError(t, db.Create(&selected).Error)
	strict := UserSubscription{
		UserId:              user.Id,
		AmountTotal:         100,
		Status:              "active",
		EndTime:             time.Now().Add(time.Hour).Unix(),
		AllowWalletOverflow: false,
	}
	require.NoError(t, db.Create(&strict).Error)

	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      "request-mixed-subscriptions",
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         user.Id,
		SubscriptionID: selected.Id,
		FundingDelta:   30,
	})
	require.NoError(t, err)
	result, err := ProcessBillingAdjustmentWithResult(taskID)
	require.NoError(t, err)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&selected, selected.Id).Error)
	assert.Equal(t, 200, user.Quota)
	assert.Equal(t, int64(120), selected.AmountUsed)
	assert.Equal(t, 30, result.SubscriptionDelta)
	assert.Zero(t, result.WalletDelta)
}

func TestBillingAdjustmentSubscriptionOverflowRollsBackAtomically(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &UserSubscription{}, &SystemTask{})
	user := User{Username: "billing-overflow-rollback", Password: "password", Quota: 200, AffCode: "overflow-rollback-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "overflow-rollback-token", RemainQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	subscription := UserSubscription{
		UserId:              user.Id,
		AmountTotal:         100,
		AmountUsed:          90,
		AllowWalletOverflow: true,
	}
	require.NoError(t, db.Create(&subscription).Error)

	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      "request-overflow-rollback",
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         user.Id,
		SubscriptionID: subscription.Id,
		TokenID:        token.Id,
		FundingDelta:   30,
		TokenDelta:     30,
	})
	require.NoError(t, err)
	require.NoError(t, db.Migrator().DropTable(&Token{}))
	require.Error(t, ProcessBillingAdjustment(taskID))

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Equal(t, 200, user.Quota)
	assert.Equal(t, int64(90), subscription.AmountUsed)
	var task SystemTask
	require.NoError(t, db.Where("task_id = ?", taskID).First(&task).Error)
	assert.Equal(t, SystemTaskStatusPending, task.Status)
	assert.NotEmpty(t, task.Error)
}

func TestBalanceMutationsBypassBatchQueue(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{})
	user := User{Username: "billing-batch", Password: "password", Quota: 100, AffCode: "batch-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "batch-token", RemainQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	common.BatchUpdateEnabled = true

	require.NoError(t, DecreaseUserQuota(user.Id, 25, false))
	require.NoError(t, DecreaseTokenQuota(token.Id, token.Key, 25))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 75, user.Quota)
	assert.Equal(t, 75, token.RemainQuota)
	assert.Equal(t, 25, token.UsedQuota)
}

func TestFlushBatchUpdatesRequeuesFailedMetricDelta(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Channel{})
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		for i := 0; i < BatchUpdateTypeCount; i++ {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = make(map[int]int)
			batchUpdateLocks[i].Unlock()
		}
	})

	channel := Channel{Id: 42, Name: "batch-retry", Key: "test", UsedQuota: 0}
	require.NoError(t, db.Create(&channel).Error)
	addNewRecord(BatchUpdateTypeChannelUsedQuota, channel.Id, 17)
	require.NoError(t, db.Migrator().DropTable(&Channel{}))

	err := FlushBatchUpdates()
	require.Error(t, err)
	batchUpdateLocks[BatchUpdateTypeChannelUsedQuota].Lock()
	pending := batchUpdateStores[BatchUpdateTypeChannelUsedQuota][channel.Id]
	batchUpdateLocks[BatchUpdateTypeChannelUsedQuota].Unlock()
	assert.Equal(t, 17, pending)

	require.NoError(t, db.AutoMigrate(&Channel{}))
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, FlushBatchUpdates())
	require.NoError(t, db.First(&channel, channel.Id).Error)
	assert.Equal(t, int64(17), channel.UsedQuota)
}

func TestFinancialReadsDoNotTrustQuotaSnapshots(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{})
	user := User{Username: "billing-cache", Password: "password", Quota: 80, AffCode: "cache-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "cache-token", RemainQuota: 60}
	require.NoError(t, db.Create(&token).Error)

	// A nil Redis client makes accidental cache access fail loudly. Financial
	// reads must still succeed because the database is authoritative.
	common.RedisEnabled = true
	common.RDB = nil
	quota, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 80, quota)
	loadedToken, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 60, loadedToken.RemainQuota)
}

func TestBalanceMutationsRejectDatabaseRangeOverflow(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{})
	user := User{Username: "billing-overflow", Password: "password", Quota: common.MaxQuota, AffCode: "overflow-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "overflow-token", RemainQuota: common.MaxQuota, UsedQuota: 0}
	require.NoError(t, db.Create(&token).Error)

	require.ErrorContains(t, IncreaseUserQuota(user.Id, 1, false), "overflow")
	require.ErrorContains(t, IncreaseTokenQuota(token.Id, token.Key, 1), "overflow")
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, common.MaxQuota, user.Quota)
	assert.Equal(t, common.MaxQuota, token.RemainQuota)
	assert.Equal(t, 0, token.UsedQuota)
}
