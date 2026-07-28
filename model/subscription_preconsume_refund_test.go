package model

import (
	"fmt"
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRefundSubscriptionPreConsumeIsAtomicAndIdempotent(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&SubscriptionPreConsumeRecord{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SubscriptionPreConsumeRecord{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SubscriptionPreConsumeRecord{}).Error)
		DB.Exec("DELETE FROM user_subscriptions")
	})

	subscription := &UserSubscription{Id: 9601, UserId: 560, AmountTotal: 100, AmountUsed: 80}
	require.NoError(t, DB.Create(subscription).Error)
	record := &SubscriptionPreConsumeRecord{
		RequestId: "atomic-refund", UserId: 560, UserSubscriptionId: subscription.Id,
		PreConsumed: 30, Status: "consumed",
	}
	require.NoError(t, DB.Create(record).Error)

	require.NoError(t, RefundSubscriptionPreConsume(record.RequestId))
	require.NoError(t, RefundSubscriptionPreConsume(record.RequestId))
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	require.NoError(t, DB.First(record, record.Id).Error)
	assert.Equal(t, int64(50), subscription.AmountUsed)
	assert.Equal(t, "refunded", record.Status)
}

func TestPostConsumeUserSubscriptionDeltaRejectsOverflow(t *testing.T) {
	subscription := &UserSubscription{Id: 9602, UserId: 561, AmountUsed: math.MaxInt64}
	require.NoError(t, DB.Create(subscription).Error)
	t.Cleanup(func() { DB.Delete(subscription) })

	require.Error(t, PostConsumeUserSubscriptionDelta(subscription.Id, 1))
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.Equal(t, int64(math.MaxInt64), subscription.AmountUsed)
}

func TestPreConsumeUserSubscriptionRejectsOversizedAmount(t *testing.T) {
	_, err := PreConsumeUserSubscription("oversized-preconsume", 1, "test-model", 0, int64(common.MaxQuota)+1)
	require.ErrorContains(t, err, "exceeds supported quota range")
}

func TestPreConsumeDuplicateConflictReturnsPersistedReservation(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&SubscriptionPreConsumeRecord{}))
	const (
		requestId               = "preconsume-on-conflict"
		userId                  = 562
		planId                  = 9703
		persistedSubscriptionId = 9704
		candidateSubscriptionId = 9705
		triggerName             = "inject_preconsume_duplicate"
	)
	require.NoError(t, DB.Exec("DROP TRIGGER IF EXISTS "+triggerName).Error)
	require.NoError(t, DB.Where("request_id = ?", requestId).Delete(&SubscriptionPreConsumeRecord{}).Error)
	require.NoError(t, DB.Delete(&UserSubscription{}, []int{persistedSubscriptionId, candidateSubscriptionId}).Error)
	require.NoError(t, DB.Delete(&SubscriptionPlan{}, planId).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DROP TRIGGER IF EXISTS "+triggerName).Error)
		require.NoError(t, DB.Where("request_id = ?", requestId).Delete(&SubscriptionPreConsumeRecord{}).Error)
		require.NoError(t, DB.Delete(&UserSubscription{}, []int{persistedSubscriptionId, candidateSubscriptionId}).Error)
		require.NoError(t, DB.Delete(&SubscriptionPlan{}, planId).Error)
		InvalidateSubscriptionPlanCache(planId)
	})

	plan := SubscriptionPlan{Id: planId, Title: "duplicate conflict plan", QuotaResetPeriod: SubscriptionResetNever}
	require.NoError(t, DB.Create(&plan).Error)
	persistedSubscription := UserSubscription{
		Id: persistedSubscriptionId, UserId: userId, PlanId: planId,
		AmountTotal: 500, AmountUsed: 123, Status: "cancelled", EndTime: GetDBTimestamp() + 3600,
	}
	candidateSubscription := UserSubscription{
		Id: candidateSubscriptionId, UserId: userId, PlanId: planId,
		AmountTotal: 100, Status: "active", EndTime: GetDBTimestamp() + 3600,
	}
	require.NoError(t, DB.Create(&persistedSubscription).Error)
	require.NoError(t, DB.Create(&candidateSubscription).Error)

	// Simulate a concurrent winner after the initial idempotency lookup but
	// before this transaction inserts its reservation. The nested insert uses a
	// different subscription ID, so the trigger does not recurse.
	triggerSQL := fmt.Sprintf(`
		CREATE TRIGGER %s
		BEFORE INSERT ON subscription_pre_consume_records
		WHEN NEW.request_id = '%s' AND NEW.user_subscription_id = %d
		BEGIN
			INSERT INTO subscription_pre_consume_records
				(request_id, claim_token, user_id, user_subscription_id, pre_consumed, status, created_at, updated_at)
			VALUES ('%s', 'persisted-winner-token', %d, %d, 7, 'consumed', 1, 1);
		END`, triggerName, requestId, candidateSubscriptionId, requestId, userId, persistedSubscriptionId)
	require.NoError(t, DB.Exec(triggerSQL).Error)

	result, err := PreConsumeUserSubscription(requestId, userId, "test-model", 0, 30)
	require.NoError(t, err)
	assert.Equal(t, persistedSubscriptionId, result.UserSubscriptionId)
	assert.Equal(t, int64(7), result.PreConsumed)
	assert.Equal(t, int64(500), result.AmountTotal)
	assert.Equal(t, int64(123), result.AmountUsedBefore)
	assert.Equal(t, int64(123), result.AmountUsedAfter)
	repeated, err := PreConsumeUserSubscription(requestId, userId, "test-model", 0, 99)
	require.NoError(t, err)
	assert.Equal(t, result, repeated)

	require.NoError(t, DB.First(&candidateSubscription, candidateSubscriptionId).Error)
	assert.Zero(t, candidateSubscription.AmountUsed)
	var records []SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", requestId).Find(&records).Error)
	require.Len(t, records, 1)
	assert.Equal(t, persistedSubscriptionId, records[0].UserSubscriptionId)
	assert.Equal(t, "persisted-winner-token", records[0].ClaimToken)
}

func TestPreConsumePersistsClaimAndRetryDoesNotChargeAgain(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&SubscriptionPreConsumeRecord{}))
	const (
		requestId      = "preconsume-claim-owner"
		userId         = 563
		planId         = 9706
		subscriptionId = 9707
	)
	require.NoError(t, DB.Where("request_id = ?", requestId).Delete(&SubscriptionPreConsumeRecord{}).Error)
	require.NoError(t, DB.Delete(&UserSubscription{}, subscriptionId).Error)
	require.NoError(t, DB.Delete(&SubscriptionPlan{}, planId).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("request_id = ?", requestId).Delete(&SubscriptionPreConsumeRecord{}).Error)
		require.NoError(t, DB.Delete(&UserSubscription{}, subscriptionId).Error)
		require.NoError(t, DB.Delete(&SubscriptionPlan{}, planId).Error)
		InvalidateSubscriptionPlanCache(planId)
	})

	plan := SubscriptionPlan{Id: planId, Title: "claim owner plan", QuotaResetPeriod: SubscriptionResetNever}
	require.NoError(t, DB.Create(&plan).Error)
	subscription := UserSubscription{
		Id: subscriptionId, UserId: userId, PlanId: planId,
		AmountTotal: 100, Status: "active", EndTime: GetDBTimestamp() + 3600,
	}
	require.NoError(t, DB.Create(&subscription).Error)

	first, err := PreConsumeUserSubscription(requestId, userId, "test-model", 0, 30)
	require.NoError(t, err)
	assert.Equal(t, int64(30), first.PreConsumed)
	assert.Equal(t, int64(0), first.AmountUsedBefore)
	assert.Equal(t, int64(30), first.AmountUsedAfter)

	repeated, err := PreConsumeUserSubscription(requestId, userId, "test-model", 0, 90)
	require.NoError(t, err)
	assert.Equal(t, int64(30), repeated.PreConsumed)

	require.NoError(t, DB.First(&subscription, subscriptionId).Error)
	assert.Equal(t, int64(30), subscription.AmountUsed)
	var record SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", requestId).First(&record).Error)
	assert.Len(t, record.ClaimToken, 32)
}

func TestCleanupSubscriptionPreConsumeRecordsRetainsConsumedBillingProof(t *testing.T) {
	db := setupBillingAdjustmentTestDB(
		t,
		&SubscriptionPreConsumeRecord{},
		&Task{},
		&Midjourney{},
		&SystemTask{},
		&TaskBillingFinalization{},
		&BillingReservation{},
	)
	now := GetDBTimestamp()
	records := []SubscriptionPreConsumeRecord{
		{RequestId: "cleanup-consumed-proof", Status: "consumed"},
		{RequestId: "cleanup-completed-refund", Status: "refunded"},
		{RequestId: "cleanup-completed-settlement", Status: "settled"},
	}
	require.NoError(t, db.Create(&records).Error)
	require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).
		Where("request_id IN ?", []string{records[0].RequestId, records[1].RequestId, records[2].RequestId}).
		Update("updated_at", now-100).Error)

	deleted, err := CleanupSubscriptionPreConsumeRecords(10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	var consumed SubscriptionPreConsumeRecord
	require.NoError(t, db.Where("request_id = ?", records[0].RequestId).First(&consumed).Error)
	var terminalCount int64
	require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).
		Where("request_id IN ?", []string{records[1].RequestId, records[2].RequestId}).
		Count(&terminalCount).Error)
	assert.Zero(t, terminalCount)
}

func TestCleanupSubscriptionPreConsumeRecordsRetainsRefundedProofForPendingReservation(t *testing.T) {
	db := setupBillingAdjustmentTestDB(
		t,
		&SubscriptionPreConsumeRecord{},
		&Task{},
		&Midjourney{},
		&SystemTask{},
		&TaskBillingFinalization{},
		&BillingReservation{},
	)
	record := SubscriptionPreConsumeRecord{
		RequestId:          "cleanup-refunded-pending-reservation",
		UserId:             702,
		UserSubscriptionId: 78,
		Status:             "refunded",
	}
	require.NoError(t, db.Create(&record).Error)
	require.NoError(t, db.Model(&record).Update("updated_at", GetDBTimestamp()-100).Error)
	require.NoError(t, db.Create(&BillingReservation{
		RequestID:      record.RequestId,
		ClaimToken:     "cleanup-refunded-pending",
		UserID:         record.UserId,
		FundingSource:  BillingAdjustmentSubscription,
		RequestedQuota: 10,
		InitialQuota:   10,
		ReservedQuota:  10,
		SubscriptionID: record.UserSubscriptionId,
		Status:         billingReservationStatusPending,
	}).Error)

	deleted, err := CleanupSubscriptionPreConsumeRecords(10)

	require.NoError(t, err)
	assert.Zero(t, deleted)
	var count int64
	require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).
		Where("id = ?", record.Id).
		Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestCleanupSubscriptionPreConsumeRecordsRetainsProofNeededByAsyncBilling(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, db *gorm.DB, requestID string)
	}{
		{
			name: "nonterminal async task",
			seed: func(t *testing.T, db *gorm.DB, requestID string) {
				t.Helper()
				require.NoError(t, db.Create(&Task{
					TaskID: "cleanup-proof-task",
					UserId: 701,
					Status: TaskStatusInProgress,
					PrivateData: TaskPrivateData{
						BillingRequestId: requestID,
					},
				}).Error)
			},
		},
		{
			name: "legacy nonterminal async task",
			seed: func(t *testing.T, db *gorm.DB, _ string) {
				t.Helper()
				require.NoError(t, db.Create(&Task{
					TaskID: "cleanup-proof-legacy-task",
					UserId: 701,
					Status: TaskStatusInProgress,
					PrivateData: TaskPrivateData{
						BillingSource:  BillingAdjustmentSubscription,
						SubscriptionId: 77,
					},
				}).Error)
			},
		},
		{
			name: "legacy nonterminal Midjourney task",
			seed: func(t *testing.T, db *gorm.DB, _ string) {
				t.Helper()
				require.NoError(t, db.Create(&Midjourney{
					UserId:                701,
					MjId:                  "cleanup-proof-legacy-midjourney",
					Status:                "IN_PROGRESS",
					Progress:              "50%",
					BillingSource:         BillingAdjustmentSubscription,
					BillingSubscriptionId: 77,
				}).Error)
			},
		},
		{
			name: "legacy null Midjourney billing markers",
			seed: func(t *testing.T, db *gorm.DB, requestID string) {
				t.Helper()
				task := Midjourney{
					UserId:                701,
					MjId:                  "cleanup-proof-null-midjourney-markers",
					Status:                "FAILURE",
					Progress:              "100%",
					BillingRequestId:      requestID,
					BillingPurpose:        "midjourney-submit:IMAGINE",
					BillingSource:         BillingAdjustmentSubscription,
					BillingSubscriptionId: 77,
				}
				require.NoError(t, db.Create(&task).Error)
				require.NoError(t, db.Model(&task).Updates(map[string]interface{}{
					"billing_finalized": nil,
					"billing_refunded":  nil,
				}).Error)
			},
		},
		{
			name: "unresolved billing adjustment",
			seed: func(t *testing.T, db *gorm.DB, requestID string) {
				t.Helper()
				payload, err := common.Marshal(BillingAdjustment{
					RequestID:             "cleanup-proof-adjustment",
					Kind:                  BillingAdjustmentRefund,
					FundingSource:         BillingAdjustmentSubscription,
					UserID:                701,
					SubscriptionID:        1,
					SubscriptionRequestID: requestID,
				})
				require.NoError(t, err)
				require.NoError(t, db.Create(&SystemTask{
					TaskID:  "cleanup-proof-adjustment",
					Type:    SystemTaskTypeBillingAdjustment,
					Status:  SystemTaskStatusPending,
					Payload: string(payload),
				}).Error)
			},
		},
		{
			name: "unresolved billing finalization",
			seed: func(t *testing.T, db *gorm.DB, requestID string) {
				t.Helper()
				payload, err := common.Marshal(TaskBillingFinalizationPayload{
					Adjustment: BillingAdjustment{
						SubscriptionRequestID: requestID,
					},
				})
				require.NoError(t, err)
				require.NoError(t, db.Create(&TaskBillingFinalization{
					FinalizationID: "cleanup-proof-finalization",
					Payload:        string(payload),
					MainStatus:     taskBillingFinalizationPending,
					LogStatus:      taskBillingFinalizationSucceeded,
				}).Error)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupBillingAdjustmentTestDB(
				t,
				&SubscriptionPreConsumeRecord{},
				&Task{},
				&Midjourney{},
				&SystemTask{},
				&TaskBillingFinalization{},
				&BillingReservation{},
			)
			requestID := "cleanup-protected-" + test.name
			record := SubscriptionPreConsumeRecord{
				RequestId:          requestID,
				UserId:             701,
				UserSubscriptionId: 77,
				Status:             "settled",
			}
			require.NoError(t, db.Create(&record).Error)
			require.NoError(t, db.Model(&record).
				Update("updated_at", GetDBTimestamp()-100).Error)
			test.seed(t, db, requestID)

			deleted, err := CleanupSubscriptionPreConsumeRecords(10)
			require.NoError(t, err)
			assert.Zero(t, deleted)

			var count int64
			require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).
				Where("id = ?", record.Id).
				Count(&count).Error)
			assert.Equal(t, int64(1), count)
		})
	}
}
