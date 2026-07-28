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
	models = append(models, &BillingReservation{}, &TaskBillingFinalization{}, &QuotaData{})
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
	adjustment.FundingDelta++
	adjustment.TokenDelta++
	_, err = EnqueueBillingAdjustment(adjustment)
	assert.ErrorContains(t, err, "different payload")
}

func TestBillingAdjustmentReversalWaitsForChargeAndRestoresExactFundingSplit(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &UserSubscription{}, &SystemTask{})
	user := User{Username: "billing-reversal", Password: "password", Quota: 200, AffCode: "reversal-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "reversal-token", RemainQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	subscription := UserSubscription{
		UserId:              user.Id,
		AmountTotal:         100,
		AmountUsed:          90,
		AllowWalletOverflow: true,
	}
	require.NoError(t, db.Create(&subscription).Error)

	chargeTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      "request-reversal-charge",
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         user.Id,
		SubscriptionID: subscription.Id,
		TokenID:        token.Id,
		FundingDelta:   30,
		TokenDelta:     30,
	})
	require.NoError(t, err)
	reversalTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:        "request-reversal-refund",
		Kind:             BillingAdjustmentRefund,
		FundingSource:    BillingAdjustmentSubscription,
		UserID:           user.Id,
		SubscriptionID:   subscription.Id,
		TokenID:          token.Id,
		ReversalOfTaskID: chargeTaskID,
	})
	require.NoError(t, err)

	_, err = ProcessBillingAdjustmentWithResult(reversalTaskID)
	require.ErrorContains(t, err, "not ready")
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Equal(t, 200, user.Quota)
	assert.Equal(t, 100, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, int64(90), subscription.AmountUsed)

	chargeResult, err := ProcessBillingAdjustmentWithResult(chargeTaskID)
	require.NoError(t, err)
	assert.Equal(t, BillingAdjustmentResult{SubscriptionDelta: 10, WalletDelta: 20, TokenDelta: 30}, chargeResult)
	reversalResult, err := ProcessBillingAdjustmentWithResult(reversalTaskID)
	require.NoError(t, err)
	assert.Equal(t, BillingAdjustmentResult{SubscriptionDelta: -10, WalletDelta: -20, TokenDelta: -30}, reversalResult)

	secondResult, err := ProcessBillingAdjustmentWithResult(reversalTaskID)
	require.NoError(t, err)
	assert.True(t, secondResult.AlreadyProcessed)
	_, err = EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:        "request-reversal-refund-duplicate",
		Kind:             BillingAdjustmentRefund,
		FundingSource:    BillingAdjustmentSubscription,
		UserID:           user.Id,
		SubscriptionID:   subscription.Id,
		TokenID:          token.Id,
		ReversalOfTaskID: chargeTaskID,
	})
	require.ErrorContains(t, err, "idempotency key reused with different payload")
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Equal(t, 200, user.Quota)
	assert.Equal(t, 100, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, int64(90), subscription.AmountUsed)
	var taskCount int64
	require.NoError(t, db.Model(&SystemTask{}).Count(&taskCount).Error)
	assert.EqualValues(t, 2, taskCount)
}

func TestBillingAdjustmentReversalAfterSubscriptionResetPreservesNewPeriodUsage(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &UserSubscription{}, &SystemTask{})
	user := User{Username: "billing-reset-reversal", Password: "password", Quota: 900, AffCode: "billing-reset-reversal-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "billing-reset-reversal-token", RemainQuota: 400, UsedQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	subscription := UserSubscription{
		UserId:              user.Id,
		AmountTotal:         100,
		AmountUsed:          90,
		LastResetTime:       100,
		AllowWalletOverflow: true,
	}
	require.NoError(t, db.Create(&subscription).Error)

	settlementTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      "billing-reset-reversal-settlement",
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         user.Id,
		SubscriptionID: subscription.Id,
		TokenID:        token.Id,
		FundingDelta:   30,
		TokenDelta:     30,
	})
	require.NoError(t, err)
	settlementResult, err := ProcessBillingAdjustmentWithResult(settlementTaskID)
	require.NoError(t, err)
	assert.Equal(t, BillingAdjustmentResult{
		SubscriptionDelta:       10,
		SubscriptionPeriodStart: 100,
		WalletDelta:             20,
		TokenDelta:              30,
	}, settlementResult)

	// The reset clears the old-period charge. Seven units are then consumed in
	// the new period and must not be erased by the old task's late reversal.
	require.NoError(t, db.Model(&subscription).Updates(map[string]any{
		"amount_used":     7,
		"last_reset_time": 200,
	}).Error)
	reversalTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:        "reversal-of:" + settlementTaskID,
		Kind:             BillingAdjustmentRefund,
		FundingSource:    BillingAdjustmentSubscription,
		UserID:           user.Id,
		SubscriptionID:   subscription.Id,
		TokenID:          token.Id,
		ReversalOfTaskID: settlementTaskID,
	})
	require.NoError(t, err)
	reversalResult, err := ProcessBillingAdjustmentWithResult(reversalTaskID)
	require.NoError(t, err)
	assert.Equal(t, BillingAdjustmentResult{WalletDelta: -20, TokenDelta: -30}, reversalResult)

	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, int64(7), subscription.AmountUsed)
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 400, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
}

func TestBillingAdjustmentReversalAfterManualResetPreservesNewUsageAndSchedule(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &SubscriptionPlan{}, &UserSubscription{}, &SystemTask{})
	user := User{Username: "billing-manual-reset-reversal", Password: "password", Quota: 900, AffCode: "billing-manual-reset-reversal-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "billing-manual-reset-reversal-token", RemainQuota: 400, UsedQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	plan := SubscriptionPlan{Title: "manual reset plan", QuotaResetPeriod: SubscriptionResetDaily}
	require.NoError(t, db.Create(&plan).Error)
	now := common.GetTimestamp()
	subscription := UserSubscription{
		UserId:              user.Id,
		PlanId:              plan.Id,
		AmountTotal:         100,
		AmountUsed:          90,
		Status:              "active",
		EndTime:             now + 86_400,
		LastResetTime:       now - 3_600,
		NextResetTime:       now + 3_600,
		AllowWalletOverflow: true,
	}
	require.NoError(t, db.Create(&subscription).Error)

	settlementTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      "billing-manual-reset-reversal-settlement",
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         user.Id,
		SubscriptionID: subscription.Id,
		TokenID:        token.Id,
		FundingDelta:   30,
		TokenDelta:     30,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessBillingAdjustment(settlementTaskID))

	previousLastReset := subscription.LastResetTime
	previousNextReset := subscription.NextResetTime
	_, err = AdminResetUserSubscriptionsByPlan(user.Id, plan.Id, false)
	require.NoError(t, err)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Equal(t, previousLastReset, subscription.LastResetTime)
	assert.Equal(t, previousNextReset, subscription.NextResetTime)
	assert.Equal(t, int64(1), subscription.QuotaResetVersion)
	require.NoError(t, db.Model(&subscription).Update("amount_used", 7).Error)

	reversalTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:        "reversal-of:" + settlementTaskID,
		Kind:             BillingAdjustmentRefund,
		FundingSource:    BillingAdjustmentSubscription,
		UserID:           user.Id,
		SubscriptionID:   subscription.Id,
		TokenID:          token.Id,
		ReversalOfTaskID: settlementTaskID,
	})
	require.NoError(t, err)
	reversalResult, err := ProcessBillingAdjustmentWithResult(reversalTaskID)
	require.NoError(t, err)
	assert.Equal(t, BillingAdjustmentResult{WalletDelta: -20, TokenDelta: -30}, reversalResult)

	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, int64(7), subscription.AmountUsed)
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 400, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
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
	tests := []BillingAdjustment{
		{
			RequestID:     "request-over-max-adjustment",
			Kind:          BillingAdjustmentSettle,
			FundingSource: BillingAdjustmentWallet,
			UserID:        1,
			FundingDelta:  common.MaxQuota + 1,
		},
		{
			RequestID:     "request-under-min-adjustment",
			Kind:          BillingAdjustmentRefund,
			FundingSource: BillingAdjustmentWallet,
			UserID:        1,
			FundingDelta:  -common.MaxQuota - 1,
		},
	}
	for _, adjustment := range tests {
		_, err := EnqueueBillingAdjustment(adjustment)
		require.ErrorContains(t, err, "storage range")
	}

	var count int64
	require.NoError(t, db.Model(&SystemTask{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestBillingAdjustmentRejectsInconsistentTokenAndExtraReservationDeltas(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &SystemTask{})
	user := User{Username: "billing-adjustment-validation", Password: "password", AffCode: "billing-adjustment-validation-aff"}
	require.NoError(t, db.Create(&user).Error)

	tests := []BillingAdjustment{
		{
			RequestID:     "missing-token-id",
			Kind:          BillingAdjustmentSettle,
			FundingSource: BillingAdjustmentWallet,
			UserID:        user.Id,
			FundingDelta:  10,
			TokenDelta:    10,
		},
		{
			RequestID:     "mismatched-token-delta",
			Kind:          BillingAdjustmentSettle,
			FundingSource: BillingAdjustmentWallet,
			UserID:        user.Id,
			TokenID:       1,
			FundingDelta:  10,
			TokenDelta:    9,
		},
		{
			RequestID:     "unproved-extra-reservation",
			Kind:          BillingAdjustmentRefund,
			FundingSource: BillingAdjustmentWallet,
			UserID:        user.Id,
			ExtraReserved: 1,
		},
	}
	for _, adjustment := range tests {
		_, err := EnqueueBillingAdjustment(adjustment)
		require.Error(t, err)
	}

	var taskCount int64
	require.NoError(t, db.Model(&SystemTask{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount)
}

func TestBillingAdjustmentRequiresLiveUserBeforePersistence(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &UserSubscription{}, &SystemTask{})
	orphanedSubscription := UserSubscription{UserId: 404, AmountTotal: 100}
	require.NoError(t, db.Create(&orphanedSubscription).Error)

	tests := []BillingAdjustment{
		{
			RequestID:     "missing-wallet-user",
			Kind:          BillingAdjustmentSettle,
			FundingSource: BillingAdjustmentWallet,
			UserID:        orphanedSubscription.UserId,
		},
		{
			RequestID:      "missing-subscription-user",
			Kind:           BillingAdjustmentSettle,
			FundingSource:  BillingAdjustmentSubscription,
			UserID:         orphanedSubscription.UserId,
			SubscriptionID: orphanedSubscription.Id,
		},
	}
	for _, adjustment := range tests {
		_, err := EnqueueBillingAdjustment(adjustment)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	}

	var taskCount int64
	require.NoError(t, db.Model(&SystemTask{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount)
}

func TestTaskBillingFinalizationRequiresLiveUserBeforePersistence(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &SystemTask{})
	_, err := EnqueueTaskBillingFinalization(TaskBillingFinalizationPayload{
		Adjustment: BillingAdjustment{
			RequestID:     "missing-finalization-user",
			Kind:          BillingAdjustmentSettle,
			FundingSource: BillingAdjustmentWallet,
			UserID:        405,
		},
		Log: TaskBillingFinalizationLog{
			UserID:  405,
			LogType: LogTypeConsume,
		},
	})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var finalizationCount int64
	require.NoError(t, db.Model(&TaskBillingFinalization{}).Count(&finalizationCount).Error)
	assert.Zero(t, finalizationCount)
}

func TestTaskBillingFinalizationRejectsInvalidLogCountersBeforePersistence(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &SystemTask{})
	user := User{Username: "invalid-finalization-log", Password: "password", AffCode: "invalid-finalization-log-aff"}
	require.NoError(t, db.Create(&user).Error)

	tests := []TaskBillingFinalizationLog{
		{PromptTokens: -1},
		{CompletionTokens: common.MaxTokensLimit + 1},
		{PromptTokens: common.MaxTokensLimit, CompletionTokens: 1},
		{UseTime: -1},
	}
	for index, log := range tests {
		log.UserID = user.Id
		log.LogType = LogTypeConsume
		_, err := EnqueueTaskBillingFinalization(TaskBillingFinalizationPayload{
			Adjustment: BillingAdjustment{
				RequestID:     fmt.Sprintf("invalid-finalization-log-%d", index),
				Kind:          BillingAdjustmentSettle,
				FundingSource: BillingAdjustmentWallet,
				UserID:        user.Id,
			},
			Log: log,
		})
		require.Error(t, err)
	}

	var finalizationCount int64
	require.NoError(t, db.Model(&TaskBillingFinalization{}).Count(&finalizationCount).Error)
	assert.Zero(t, finalizationCount)
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

func TestBillingAdjustmentRejectsNegativeSubscriptionDeltaWithoutReservationProof(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &UserSubscription{}, &SystemTask{})
	subscription := UserSubscription{UserId: 812, AmountTotal: 1_000, AmountUsed: 200}
	require.NoError(t, db.Create(&subscription).Error)

	unsafeAdjustment := BillingAdjustment{
		RequestID:      "legacy-task-refund-without-proof",
		Kind:           BillingAdjustmentRefund,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         subscription.UserId,
		SubscriptionID: subscription.Id,
		FundingDelta:   -100,
		TokenDelta:     -100,
	}
	_, err := EnqueueBillingAdjustment(unsafeAdjustment)
	require.ErrorContains(t, err, "requires reservation proof")

	var taskCount int64
	require.NoError(t, db.Model(&SystemTask{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Equal(t, int64(200), subscription.AmountUsed)

	payload, err := common.Marshal(unsafeAdjustment)
	require.NoError(t, err)
	persistedTask := SystemTask{
		TaskID:  BillingAdjustmentTaskID(unsafeAdjustment.RequestID, unsafeAdjustment.Kind),
		Type:    SystemTaskTypeBillingAdjustment,
		Status:  SystemTaskStatusPending,
		Payload: string(payload),
	}
	require.NoError(t, db.Create(&persistedTask).Error)
	_, err = ProcessBillingAdjustmentWithResult(persistedTask.TaskID)
	require.ErrorContains(t, err, "requires reservation proof")
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Equal(t, int64(200), subscription.AmountUsed)
}

func TestBillingAdjustmentLateSubscriptionRefundPreservesNewPeriodUsage(t *testing.T) {
	tests := []struct {
		name          string
		persistRecord bool
	}{
		{name: "old-period record", persistRecord: true},
		{name: "cleaned record", persistRecord: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &SystemTask{})
			user := User{Username: "late-subscription-refund-" + test.name, Password: "password", Quota: 100, AffCode: "late-subscription-refund-aff-" + test.name}
			require.NoError(t, db.Create(&user).Error)
			token := Token{UserId: user.Id, Key: "late-subscription-refund-token-" + test.name, RemainQuota: 380, UsedQuota: 120}
			require.NoError(t, db.Create(&token).Error)
			subscription := UserSubscription{
				UserId:            user.Id,
				AmountTotal:       1_000,
				AmountUsed:        7,
				LastResetTime:     common.GetTimestamp(),
				QuotaResetVersion: 1,
			}
			require.NoError(t, db.Create(&subscription).Error)
			requestID := "late-subscription-refund-" + test.name
			var record SubscriptionPreConsumeRecord
			if test.persistRecord {
				record = SubscriptionPreConsumeRecord{
					RequestId:          requestID,
					UserId:             user.Id,
					UserSubscriptionId: subscription.Id,
					PreConsumed:        100,
					QuotaResetVersion:  0,
					Status:             "consumed",
				}
				require.NoError(t, db.Create(&record).Error)
			}

			taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
				RequestID:             requestID,
				Kind:                  BillingAdjustmentRefund,
				FundingSource:         BillingAdjustmentSubscription,
				UserID:                user.Id,
				SubscriptionID:        subscription.Id,
				SubscriptionRequestID: requestID,
				TokenID:               token.Id,
				FundingDelta:          -120,
				TokenDelta:            -120,
				ExtraReserved:         20,
			})
			require.NoError(t, err)
			result, err := ProcessBillingAdjustmentWithResult(taskID)
			require.NoError(t, err)
			assert.Equal(t, BillingAdjustmentResult{TokenDelta: -120}, result)

			require.NoError(t, db.First(&subscription, subscription.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, int64(7), subscription.AmountUsed)
			assert.Equal(t, 500, token.RemainQuota)
			assert.Zero(t, token.UsedQuota)
			if test.persistRecord {
				require.NoError(t, db.First(&record, record.Id).Error)
				assert.Equal(t, "refunded", record.Status)
			}
		})
	}
}

func TestBillingAdjustmentNegativeSettlementAfterResetOnlyRestoresToken(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &SystemTask{})
	user := User{Username: "negative-settlement-after-reset", Password: "password", Quota: 100, AffCode: "negative-settlement-after-reset-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "negative-settlement-after-reset-token", RemainQuota: 400, UsedQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	subscription := UserSubscription{
		UserId:            user.Id,
		AmountTotal:       1_000,
		AmountUsed:        7,
		LastResetTime:     common.GetTimestamp(),
		QuotaResetVersion: 1,
	}
	require.NoError(t, db.Create(&subscription).Error)
	const requestID = "negative-settlement-after-reset-request"
	record := SubscriptionPreConsumeRecord{
		RequestId:          requestID,
		UserId:             user.Id,
		UserSubscriptionId: subscription.Id,
		PreConsumed:        100,
		QuotaResetVersion:  0,
		Status:             "consumed",
	}
	require.NoError(t, db.Create(&record).Error)

	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:             requestID,
		Kind:                  BillingAdjustmentSettle,
		FundingSource:         BillingAdjustmentSubscription,
		UserID:                user.Id,
		SubscriptionID:        subscription.Id,
		SubscriptionRequestID: requestID,
		TokenID:               token.Id,
		FundingDelta:          -30,
		TokenDelta:            -30,
	})
	require.NoError(t, err)
	result, err := ProcessBillingAdjustmentWithResult(taskID)
	require.NoError(t, err)
	assert.Equal(t, BillingAdjustmentResult{TokenDelta: -30}, result)

	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.NoError(t, db.First(&record, record.Id).Error)
	assert.Equal(t, int64(7), subscription.AmountUsed)
	assert.Equal(t, 430, token.RemainQuota)
	assert.Equal(t, 70, token.UsedQuota)
	assert.Equal(t, "settled", record.Status)
	assert.Equal(t, int64(70), record.PreConsumed)
}

func TestBillingAdjustmentNegativeSettlementShrinksRefundProof(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &SystemTask{})
	user := User{Username: "negative-settlement-proof", Password: "password", Quota: 100, AffCode: "negative-settlement-proof-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "negative-settlement-proof-token", RemainQuota: 400, UsedQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 1_000, AmountUsed: 100}
	require.NoError(t, db.Create(&subscription).Error)
	const requestID = "negative-settlement-proof-request"
	record := SubscriptionPreConsumeRecord{
		RequestId:          requestID,
		UserId:             user.Id,
		UserSubscriptionId: subscription.Id,
		PreConsumed:        100,
		Status:             "consumed",
	}
	require.NoError(t, db.Create(&record).Error)

	initialTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:             requestID,
		Kind:                  BillingAdjustmentSettle,
		FundingSource:         BillingAdjustmentSubscription,
		UserID:                user.Id,
		SubscriptionID:        subscription.Id,
		SubscriptionRequestID: requestID,
		TokenID:               token.Id,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessBillingAdjustment(initialTaskID))

	settlementTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:             requestID + "-recalculation",
		Kind:                  BillingAdjustmentSettle,
		FundingSource:         BillingAdjustmentSubscription,
		UserID:                user.Id,
		SubscriptionID:        subscription.Id,
		SubscriptionRequestID: requestID,
		TokenID:               token.Id,
		FundingDelta:          -30,
		TokenDelta:            -30,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessBillingAdjustment(settlementTaskID))
	require.NoError(t, db.First(&record, record.Id).Error)
	assert.Equal(t, "settled", record.Status)
	assert.Equal(t, int64(70), record.PreConsumed)

	refundTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:             requestID + "-task-failure",
		Kind:                  BillingAdjustmentRefund,
		FundingSource:         BillingAdjustmentSubscription,
		UserID:                user.Id,
		SubscriptionID:        subscription.Id,
		SubscriptionRequestID: requestID,
		TokenID:               token.Id,
		FundingDelta:          -70,
		TokenDelta:            -70,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessBillingAdjustment(refundTaskID))
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.NoError(t, db.First(&record, record.Id).Error)
	assert.Zero(t, subscription.AmountUsed)
	assert.Equal(t, 500, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, "refunded", record.Status)
}

func TestBillingAdjustmentEnqueueRejectsMismatchedSubscriptionProof(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &SystemTask{})
	user := User{Username: "proof-owner", Password: "password", AffCode: "proof-owner-aff"}
	otherUser := User{Username: "proof-other", Password: "password", AffCode: "proof-other-aff"}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&otherUser).Error)
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 1_000, AmountUsed: 100}
	otherSubscription := UserSubscription{UserId: otherUser.Id, AmountTotal: 1_000, AmountUsed: 100}
	require.NoError(t, db.Create(&subscription).Error)
	require.NoError(t, db.Create(&otherSubscription).Error)
	const requestID = "mismatched-subscription-proof"
	require.NoError(t, db.Create(&SubscriptionPreConsumeRecord{
		RequestId:          requestID,
		UserId:             otherUser.Id,
		UserSubscriptionId: otherSubscription.Id,
		PreConsumed:        100,
		Status:             "settled",
	}).Error)

	_, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:             "mismatched-subscription-proof-refund",
		Kind:                  BillingAdjustmentRefund,
		FundingSource:         BillingAdjustmentSubscription,
		UserID:                user.Id,
		SubscriptionID:        subscription.Id,
		SubscriptionRequestID: requestID,
		FundingDelta:          -100,
	})

	require.ErrorContains(t, err, "proof does not match adjustment")
	var taskCount int64
	require.NoError(t, db.Model(&SystemTask{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount)
}

func TestBillingAdjustmentZeroSettlementMarksReservationSettled(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &SystemTask{})
	user := User{Username: "zero-settlement-user", Password: "password", AffCode: "zero-settlement-aff"}
	require.NoError(t, db.Create(&user).Error)
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 1_000, AmountUsed: 100}
	require.NoError(t, db.Create(&subscription).Error)
	const requestID = "zero-settlement-proof-request"
	record := SubscriptionPreConsumeRecord{
		RequestId:          requestID,
		UserId:             subscription.UserId,
		UserSubscriptionId: subscription.Id,
		PreConsumed:        100,
		Status:             "consumed",
	}
	require.NoError(t, db.Create(&record).Error)

	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:             requestID,
		Kind:                  BillingAdjustmentSettle,
		FundingSource:         BillingAdjustmentSubscription,
		UserID:                subscription.UserId,
		SubscriptionID:        subscription.Id,
		SubscriptionRequestID: requestID,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessBillingAdjustment(taskID))
	require.NoError(t, db.First(&record, record.Id).Error)
	assert.Equal(t, "settled", record.Status)
	assert.Equal(t, int64(100), record.PreConsumed)
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

func TestBillingAdjustmentDoesNotMutateReusedTokenOwnedByAnotherUser(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &SystemTask{})
	originalUser := User{
		Username: "billing-token-owner-original",
		Password: "password",
		Quota:    200,
		AffCode:  "token-owner-original-aff",
	}
	replacementUser := User{
		Username: "billing-token-owner-replacement",
		Password: "password",
		Quota:    200,
		AffCode:  "token-owner-replacement-aff",
	}
	require.NoError(t, db.Create(&originalUser).Error)
	require.NoError(t, db.Create(&replacementUser).Error)
	originalToken := Token{
		UserId:      originalUser.Id,
		Key:         "billing-token-owner-original-key",
		RemainQuota: 100,
	}
	require.NoError(t, db.Create(&originalToken).Error)

	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:     "request-reused-token-owner",
		Kind:          BillingAdjustmentSettle,
		FundingSource: BillingAdjustmentWallet,
		UserID:        originalUser.Id,
		TokenID:       originalToken.Id,
		FundingDelta:  30,
		TokenDelta:    30,
	})
	require.NoError(t, err)

	require.NoError(t, db.Unscoped().Delete(&originalToken).Error)
	replacementToken := Token{
		Id:          originalToken.Id,
		UserId:      replacementUser.Id,
		Key:         "billing-token-owner-replacement-key",
		RemainQuota: 75,
		UsedQuota:   5,
	}
	require.NoError(t, db.Create(&replacementToken).Error)

	result, err := ProcessBillingAdjustmentWithResult(taskID)
	require.NoError(t, err)
	assert.Equal(t, 30, result.WalletDelta)
	assert.Zero(t, result.TokenDelta)

	require.NoError(t, db.First(&originalUser, originalUser.Id).Error)
	require.NoError(t, db.First(&replacementToken, replacementToken.Id).Error)
	assert.Equal(t, 170, originalUser.Quota)
	assert.Equal(t, replacementUser.Id, replacementToken.UserId)
	assert.Equal(t, 75, replacementToken.RemainQuota)
	assert.Equal(t, 5, replacementToken.UsedQuota)
}

func TestBillingAdjustmentDoesNotMutateSameUserReplacementToken(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &SystemTask{})
	user := User{
		Username: "billing-token-identity-user",
		Password: "password",
		Quota:    200,
		AffCode:  "token-identity-user-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	originalToken := Token{
		UserId:      user.Id,
		Key:         "billing-token-identity-original-key",
		RemainQuota: 100,
	}
	require.NoError(t, db.Create(&originalToken).Error)

	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:     "request-reused-token-identity",
		Kind:          BillingAdjustmentSettle,
		FundingSource: BillingAdjustmentWallet,
		UserID:        user.Id,
		TokenID:       originalToken.Id,
		TokenKeyHash:  BillingTokenKeyHash(originalToken.Key),
		FundingDelta:  30,
		TokenDelta:    30,
	})
	require.NoError(t, err)

	require.NoError(t, db.Unscoped().Delete(&originalToken).Error)
	replacementToken := Token{
		Id:          originalToken.Id,
		UserId:      user.Id,
		Key:         "billing-token-identity-replacement-key",
		RemainQuota: 75,
		UsedQuota:   5,
	}
	require.NoError(t, db.Create(&replacementToken).Error)

	result, err := ProcessBillingAdjustmentWithResult(taskID)
	require.NoError(t, err)
	assert.Equal(t, 30, result.WalletDelta)
	assert.Zero(t, result.TokenDelta)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&replacementToken, replacementToken.Id).Error)
	assert.Equal(t, 170, user.Quota)
	assert.Equal(t, 75, replacementToken.RemainQuota)
	assert.Equal(t, 5, replacementToken.UsedQuota)
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

	channel := Channel{Id: 42, Name: "batch-retry", Key: "test", UsedQuota: 0}
	require.NoError(t, db.Create(&channel).Error)
	addNewRecord(BatchUpdateTypeChannelUsedQuota, channel.Id, 17)
	require.NoError(t, db.Migrator().DropTable(&Channel{}))

	err := FlushBatchUpdates()
	require.Error(t, err)
	batchUpdateLocks[BatchUpdateTypeChannelUsedQuota].Lock()
	pending := batchUpdateStores[BatchUpdateTypeChannelUsedQuota][channel.Id]
	batchUpdateLocks[BatchUpdateTypeChannelUsedQuota].Unlock()
	assert.Equal(t, int64(17), pending)

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
