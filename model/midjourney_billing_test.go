package model

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMidjourneyBillingContextPersistsWithoutLeakingThroughJSON(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Midjourney{})
	task := Midjourney{
		UserId:                7,
		MjId:                  "midjourney-billing-context",
		Quota:                 30,
		BillingRequestId:      "request-secret",
		BillingPurpose:        "midjourney-submit:IMAGINE",
		BillingSource:         BillingAdjustmentSubscription,
		BillingSubscriptionId: 11,
		BillingTaskId:         "billing_midjourney_settlement",
		BillingTokenId:        13,
		BillingIsPlayground:   true,
	}
	require.NoError(t, db.Create(&task).Error)

	var persisted Midjourney
	require.NoError(t, db.First(&persisted, task.Id).Error)
	assert.Equal(t, task.BillingRequestId, persisted.BillingRequestId)
	assert.Equal(t, task.BillingPurpose, persisted.BillingPurpose)
	assert.Equal(t, task.BillingSource, persisted.BillingSource)
	assert.Equal(t, task.BillingSubscriptionId, persisted.BillingSubscriptionId)
	assert.Equal(t, task.BillingTaskId, persisted.BillingTaskId)
	assert.Equal(t, task.BillingTokenId, persisted.BillingTokenId)
	assert.True(t, persisted.BillingIsPlayground)

	encoded, err := common.Marshal(persisted)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "request-secret")
	assert.NotContains(t, string(encoded), "billing_midjourney_settlement")
	assert.NotContains(t, string(encoded), "billing_purpose")
}

func TestMidjourneyBillingLogUsesDurableEventIdentity(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Log{})
	user := User{
		Username: "midjourney-log-event",
		Password: "password",
		AffCode:  "midjourney-log-event-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	task := Midjourney{
		UserId:           user.Id,
		ChannelId:        17,
		BillingTokenId:   23,
		BillingModelName: "mj_imagine",
		BillingTokenName: "midjourney-token",
		BillingGroup:     "default",
	}
	const eventID = "midjourney-durable-log-event"

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := ensureMidjourneyBillingLogTx(tx, &task, eventID, LogTypeConsume, 30, "consume", "{}"); err != nil {
			return err
		}
		return ensureMidjourneyBillingLogTx(tx, &task, eventID, LogTypeConsume, 30, "consume", "{}")
	}))

	var logs []Log
	require.NoError(t, db.Where("billing_event_id = ?", eventID).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, eventID, logs[0].RequestId)
	assert.Equal(t, 30, logs[0].Quota)
}

func TestTerminalMidjourneyTasksRemainSchedulableUntilBillingIsReconciled(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Midjourney{})
	task := Midjourney{
		UserId:           8,
		MjId:             "midjourney-terminal-reconciliation",
		Status:           "SUCCESS",
		Progress:         "100%",
		BillingPurpose:   "midjourney-submit:IMAGINE",
		BillingTaskId:    "billing_terminal_reconciliation",
		BillingFinalized: false,
	}
	require.NoError(t, db.Create(&task).Error)
	hasUnfinished, err := HasUnfinishedMidjourneyTasks()
	require.NoError(t, err)
	assert.True(t, hasUnfinished)
	tasks, err := GetAllUnFinishTasks()
	require.NoError(t, err)
	assert.Len(t, tasks, 1)

	require.NoError(t, db.Model(&task).Update("billing_finalized", true).Error)
	hasUnfinished, err = HasUnfinishedMidjourneyTasks()
	require.NoError(t, err)
	assert.False(t, hasUnfinished)

	require.NoError(t, db.Model(&task).Updates(map[string]any{
		"status":           "FAILURE",
		"billing_refunded": false,
	}).Error)
	hasUnfinished, err = HasUnfinishedMidjourneyTasks()
	require.NoError(t, err)
	assert.True(t, hasUnfinished)
	require.NoError(t, db.Model(&task).Update("billing_refunded", true).Error)
	hasUnfinished, err = HasUnfinishedMidjourneyTasks()
	require.NoError(t, err)
	assert.False(t, hasUnfinished)
}

func TestMidjourneyTaskWithNullProgressRemainsSchedulable(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Midjourney{})
	task := Midjourney{
		UserId: 9,
		MjId:   "midjourney-null-progress",
		Status: "IN_PROGRESS",
	}
	require.NoError(t, db.Create(&task).Error)
	require.NoError(t, db.Model(&task).Update("progress", nil).Error)

	hasUnfinished, err := HasUnfinishedMidjourneyTasks()
	require.NoError(t, err)
	assert.True(t, hasUnfinished)
	tasks, err := GetAllUnFinishTasks()
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, task.Id, tasks[0].Id)
}

func TestStaleMidjourneyStatusUpdatesPreserveBillingFinalization(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Midjourney{})
	task := Midjourney{
		MjId:             "midjourney-stale-provider-update",
		Status:           "IN_PROGRESS",
		Progress:         "50%",
		BillingTaskId:    "original-billing-task",
		BillingPurpose:   "midjourney-submit:IMAGINE",
		BillingFinalized: false,
		BillingRefunded:  false,
	}
	require.NoError(t, db.Create(&task).Error)

	var staleNotify Midjourney
	require.NoError(t, db.First(&staleNotify, task.Id).Error)
	require.NoError(t, db.Model(&Midjourney{}).Where("id = ?", task.Id).Updates(map[string]any{
		"billing_task_id":   "finalized-billing-task",
		"billing_finalized": true,
		"billing_refunded":  true,
	}).Error)

	staleNotify.Progress = "75%"
	require.NoError(t, staleNotify.Update())
	staleNotify.Status = "SUCCESS"
	staleNotify.Progress = "100%"
	won, err := staleNotify.UpdateWithStatus("IN_PROGRESS")
	require.NoError(t, err)
	require.True(t, won)

	var persisted Midjourney
	require.NoError(t, db.First(&persisted, task.Id).Error)
	assert.Equal(t, "SUCCESS", persisted.Status)
	assert.Equal(t, "100%", persisted.Progress)
	assert.Equal(t, "finalized-billing-task", persisted.BillingTaskId)
	assert.True(t, persisted.BillingFinalized)
	assert.True(t, persisted.BillingRefunded)
}

func TestMidjourneyStatusCASPersistsAcceptedProviderCode(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &Midjourney{})
	task := Midjourney{
		MjId:     "midjourney-provider-code-transition",
		Code:     22,
		Status:   "IN_PROGRESS",
		Progress: "50%",
	}
	require.NoError(t, db.Create(&task).Error)

	task.Code = 1
	task.Progress = "75%"
	won, err := task.UpdateWithStatus("IN_PROGRESS")
	require.NoError(t, err)
	require.True(t, won)

	var persisted Midjourney
	require.NoError(t, db.First(&persisted, task.Id).Error)
	assert.Equal(t, 1, persisted.Code)
	assert.Equal(t, "75%", persisted.Progress)
}

func TestInsertMidjourneyPinsAcceptedTaskToSubmittingChannel(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Channel{}, &Midjourney{})
	user := User{
		Username: "midjourney-channel-snapshot-user",
		Password: "password",
		AffCode:  "midjourney-channel-snapshot-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	channel := Channel{
		Name:        "midjourney-channel-snapshot",
		Key:         "channel-key",
		CreatedTime: 1_700_000_001,
	}
	require.NoError(t, db.Create(&channel).Error)

	accepted := &Midjourney{
		UserId:             user.Id,
		MjId:               "midjourney-channel-snapshot-accepted",
		Status:             "SUBMITTED",
		Progress:           "0%",
		ChannelId:          channel.Id,
		ChannelCreatedTime: channel.CreatedTime,
	}
	require.NoError(t, accepted.Insert())

	replacement := &Midjourney{
		UserId:             user.Id,
		MjId:               "midjourney-channel-snapshot-replaced",
		Status:             "SUBMITTED",
		Progress:           "0%",
		ChannelId:          channel.Id,
		ChannelCreatedTime: channel.CreatedTime - 1,
	}
	require.ErrorContains(t, replacement.Insert(), "was replaced")

	missing := &Midjourney{
		UserId:             user.Id,
		MjId:               "midjourney-channel-snapshot-missing",
		Status:             "SUBMITTED",
		Progress:           "0%",
		ChannelId:          channel.Id + 1,
		ChannelCreatedTime: channel.CreatedTime,
	}
	require.ErrorContains(t, missing.Insert(), "no longer exists")

	var count int64
	require.NoError(t, db.Model(&Midjourney{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestInsertMidjourneyWithBillingReservationAdoptsWalletChargeExactlyOnce(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &SystemTask{}, &Midjourney{})
	user := User{Username: "midjourney-reserved-wallet", Password: "password", Quota: 1_000, AffCode: "midjourney-reserved-wallet-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "midjourney-reserved-wallet-token", RemainQuota: 500}
	require.NoError(t, db.Create(&token).Error)

	const (
		requestID = "midjourney-reserved-wallet-request"
		purpose   = "midjourney-submit:IMAGINE"
		quota     = 30
	)
	_, err := CreateBillingReservation(BillingReservationRequest{
		RequestID:      requestID,
		UserID:         user.Id,
		TokenID:        token.Id,
		TokenKey:       token.Key,
		FundingSource:  BillingAdjustmentWallet,
		ModelName:      "mj_imagine",
		RequestedQuota: quota,
		InitialQuota:   quota,
	})
	require.NoError(t, err)

	settlementRequestID := requestID + "\x00purpose:" + purpose
	settlementTaskID := BillingAdjustmentTaskID(settlementRequestID, BillingAdjustmentSettle)
	task := &Midjourney{
		UserId:           user.Id,
		Code:             1,
		Action:           "IMAGINE",
		MjId:             "midjourney-reserved-wallet-upstream",
		Status:           "SUBMITTED",
		Progress:         "0%",
		ChannelId:        7,
		Quota:            quota,
		BillingRequestId: requestID,
		BillingPurpose:   purpose,
		BillingSource:    BillingAdjustmentWallet,
		BillingTaskId:    settlementTaskID,
		BillingTokenId:   token.Id,
	}
	require.NoError(t, task.InsertWithBillingReservation())
	firstID := task.Id
	require.Positive(t, firstID)

	retry := *task
	retry.Id = 0
	require.NoError(t, retry.InsertWithBillingReservation())
	assert.Equal(t, firstID, retry.Id)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 970, user.Quota)
	assert.Equal(t, 470, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)

	var reservation BillingReservation
	require.NoError(t, db.Where("request_id = ?", requestID).First(&reservation).Error)
	assert.Equal(t, billingReservationStatusSettled, reservation.Status)

	var settlement SystemTask
	require.NoError(t, db.Where("task_id = ?", settlementTaskID).First(&settlement).Error)
	assert.Equal(t, SystemTaskStatusSucceeded, settlement.Status)
	var adjustment BillingAdjustment
	require.NoError(t, common.UnmarshalJsonStr(settlement.Payload, &adjustment))
	assert.Equal(t, quota, adjustment.FundingDelta)
	assert.Equal(t, quota, adjustment.TokenDelta)
	assert.Equal(t, BillingTokenKeyHash(token.Key), adjustment.TokenKeyHash)
	var result BillingAdjustmentResult
	require.NoError(t, common.UnmarshalJsonStr(settlement.Result, &result))
	assert.Equal(t, quota, result.WalletDelta)
	assert.Equal(t, quota, result.TokenDelta)

	var taskCount int64
	require.NoError(t, db.Model(&Midjourney{}).Count(&taskCount).Error)
	assert.Equal(t, int64(1), taskCount)

	reversalID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:        "reversal-of:" + settlementTaskID,
		Kind:             BillingAdjustmentRefund,
		FundingSource:    BillingAdjustmentWallet,
		UserID:           user.Id,
		TokenID:          token.Id,
		ReversalOfTaskID: settlementTaskID,
	})
	require.NoError(t, err)
	_, err = ProcessBillingAdjustmentWithResult(reversalID)
	require.NoError(t, err)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 1_000, user.Quota)
	assert.Equal(t, 500, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
}

func TestInsertMidjourneyWithBillingReservationPreservesSubscriptionReversal(t *testing.T) {
	db := setupBillingAdjustmentTestDB(
		t,
		&User{},
		&Token{},
		&SubscriptionPlan{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&SystemTask{},
		&Midjourney{},
	)
	user := User{Username: "midjourney-reserved-subscription", Password: "password", Quota: 1_000, AffCode: "midjourney-reserved-subscription-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "midjourney-reserved-subscription-token", RemainQuota: 500}
	require.NoError(t, db.Create(&token).Error)
	plan := SubscriptionPlan{Title: "Midjourney reservation plan", QuotaResetPeriod: SubscriptionResetNever}
	require.NoError(t, db.Create(&plan).Error)
	subscription := UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 100,
		AmountUsed:  20,
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
		Status:      "active",
	}
	require.NoError(t, db.Create(&subscription).Error)

	const (
		requestID = "midjourney-reserved-subscription-request"
		purpose   = "midjourney-submit:IMAGINE"
		quota     = 30
	)
	reservation, err := CreateBillingReservation(BillingReservationRequest{
		RequestID:      requestID,
		UserID:         user.Id,
		TokenID:        token.Id,
		TokenKey:       token.Key,
		FundingSource:  BillingAdjustmentSubscription,
		ModelName:      "mj_imagine",
		RequestedQuota: quota,
		InitialQuota:   quota,
	})
	require.NoError(t, err)
	require.Equal(t, subscription.Id, reservation.SubscriptionID)

	settlementTaskID := BillingAdjustmentTaskID(
		requestID+"\x00purpose:"+purpose,
		BillingAdjustmentSettle,
	)
	task := &Midjourney{
		UserId:                user.Id,
		Code:                  1,
		Action:                "IMAGINE",
		MjId:                  "midjourney-reserved-subscription-upstream",
		Status:                "SUBMITTED",
		Progress:              "0%",
		ChannelId:             8,
		Quota:                 quota,
		BillingRequestId:      requestID,
		BillingPurpose:        purpose,
		BillingSource:         BillingAdjustmentSubscription,
		BillingSubscriptionId: subscription.Id,
		BillingTaskId:         settlementTaskID,
		BillingTokenId:        token.Id,
	}
	require.NoError(t, task.InsertWithBillingReservation())

	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, int64(50), subscription.AmountUsed)
	assert.Equal(t, 1_000, user.Quota)
	assert.Equal(t, 470, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)

	var proof SubscriptionPreConsumeRecord
	require.NoError(t, db.Where("request_id = ?", requestID).First(&proof).Error)
	assert.Equal(t, "settled", proof.Status)

	reversalID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:        "reversal-of:" + settlementTaskID,
		Kind:             BillingAdjustmentRefund,
		FundingSource:    BillingAdjustmentSubscription,
		UserID:           user.Id,
		SubscriptionID:   subscription.Id,
		TokenID:          token.Id,
		ReversalOfTaskID: settlementTaskID,
	})
	require.NoError(t, err)
	reversal, err := ProcessBillingAdjustmentWithResult(reversalID)
	require.NoError(t, err)
	assert.Equal(t, -quota, reversal.SubscriptionDelta)
	assert.Equal(t, -quota, reversal.TokenDelta)

	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, int64(20), subscription.AmountUsed)
	assert.Equal(t, 1_000, user.Quota)
	assert.Equal(t, 500, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
}

func TestConcurrentMidjourneyReservationsCannotOverspendWallet(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &SystemTask{}, &Midjourney{})
	user := User{Username: "midjourney-concurrent-reservation", Password: "password", Quota: 30, AffCode: "midjourney-concurrent-reservation-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "midjourney-concurrent-reservation-token", RemainQuota: 30}
	require.NoError(t, db.Create(&token).Error)

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, requestID := range []string{
		"midjourney-concurrent-reservation-first",
		"midjourney-concurrent-reservation-second",
	} {
		workers.Add(1)
		go func(requestID string) {
			defer workers.Done()
			<-start
			_, err := CreateBillingReservation(BillingReservationRequest{
				RequestID:      requestID,
				UserID:         user.Id,
				TokenID:        token.Id,
				TokenKey:       token.Key,
				FundingSource:  BillingAdjustmentWallet,
				ModelName:      "mj_imagine",
				RequestedQuota: 30,
				InitialQuota:   30,
			})
			results <- err
		}(requestID)
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	insufficient := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrBillingReservationInsufficientWallet):
			insufficient++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, insufficient)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Zero(t, user.Quota)
	assert.Zero(t, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)
}
