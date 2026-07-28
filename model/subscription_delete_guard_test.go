package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSubscriptionDeleteGuardTest(t *testing.T) *gorm.DB {
	t.Helper()
	return setupBillingAdjustmentTestDB(
		t,
		&User{},
		&Token{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&BillingReservation{},
		&SystemTask{},
		&Midjourney{},
		&Task{},
	)
}

func createSubscriptionDeleteGuardUser(t *testing.T, db *gorm.DB, id int, username string) User {
	t.Helper()
	user := User{
		Id:       id,
		Username: username,
		Password: "password",
		AffCode:  username + "-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	return user
}

func TestAdminDeleteUserSubscriptionRejectsLivePreConsumeRecord(t *testing.T) {
	db := setupSubscriptionDeleteGuardTest(t)
	user := createSubscriptionDeleteGuardUser(t, db, 801, "delete-guard-live-user")
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 100, AmountUsed: 20, Status: "cancelled"}
	require.NoError(t, db.Create(&subscription).Error)
	record := SubscriptionPreConsumeRecord{
		RequestId:          "delete-guard-live-reservation",
		ClaimToken:         "delete-guard-claim",
		UserId:             subscription.UserId,
		UserSubscriptionId: subscription.Id,
		PreConsumed:        20,
		Status:             "consumed",
	}
	require.NoError(t, db.Create(&record).Error)

	_, err := AdminDeleteUserSubscription(subscription.Id)
	require.ErrorContains(t, err, "live billing reservations")
	var subscriptionCount int64
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", subscription.Id).Count(&subscriptionCount).Error)
	assert.Equal(t, int64(1), subscriptionCount)

	require.NoError(t, db.Model(&record).Update("status", "refunded").Error)
	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", subscription.Id).Count(&subscriptionCount).Error)
	assert.Zero(t, subscriptionCount)
}

func TestAdminDeleteUserSubscriptionAllowsSettledPreConsumeProof(t *testing.T) {
	db := setupSubscriptionDeleteGuardTest(t)
	user := createSubscriptionDeleteGuardUser(t, db, 807, "delete-guard-settled-user")
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 100, AmountUsed: 20, Status: "cancelled"}
	require.NoError(t, db.Create(&subscription).Error)
	require.NoError(t, db.Create(&SubscriptionPreConsumeRecord{
		RequestId:          "delete-guard-settled-proof",
		UserId:             subscription.UserId,
		UserSubscriptionId: subscription.Id,
		PreConsumed:        20,
		Status:             "settled",
	}).Error)

	_, err := AdminDeleteUserSubscription(subscription.Id)
	require.NoError(t, err)
	var subscriptionCount int64
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", subscription.Id).Count(&subscriptionCount).Error)
	assert.Zero(t, subscriptionCount)
}

func TestAdminDeleteUserSubscriptionWaitsForPendingBillingAdjustment(t *testing.T) {
	db := setupSubscriptionDeleteGuardTest(t)
	user := User{Id: 802, Username: "delete-guard-pending-user", Password: "password", AffCode: "delete-guard-pending-aff"}
	require.NoError(t, db.Create(&user).Error)
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 100, Status: "cancelled"}
	require.NoError(t, db.Create(&subscription).Error)
	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      "delete-guard-pending-adjustment",
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         subscription.UserId,
		SubscriptionID: subscription.Id,
		FundingDelta:   10,
	})
	require.NoError(t, err)

	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.ErrorContains(t, err, "billing adjustment")
	var subscriptionCount int64
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", subscription.Id).Count(&subscriptionCount).Error)
	assert.Equal(t, int64(1), subscriptionCount)

	require.NoError(t, ProcessBillingAdjustment(taskID))
	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", subscription.Id).Count(&subscriptionCount).Error)
	assert.Zero(t, subscriptionCount)
}

func TestAdminDeleteUserSubscriptionRejectsFailedBillingAdjustment(t *testing.T) {
	db := setupSubscriptionDeleteGuardTest(t)
	user := createSubscriptionDeleteGuardUser(t, db, 808, "delete-guard-failed-user")
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 100, Status: "cancelled"}
	require.NoError(t, db.Create(&subscription).Error)
	payload, err := common.Marshal(BillingAdjustment{
		RequestID:      "delete-guard-failed-adjustment",
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         subscription.UserId,
		SubscriptionID: subscription.Id,
		FundingDelta:   10,
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&SystemTask{
		TaskID:  "delete-guard-failed-adjustment",
		Type:    SystemTaskTypeBillingAdjustment,
		Status:  SystemTaskStatusFailed,
		Payload: string(payload),
	}).Error)

	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.ErrorContains(t, err, "billing adjustment")
	var subscriptionCount int64
	require.NoError(t, db.Model(&UserSubscription{}).
		Where("id = ?", subscription.Id).
		Count(&subscriptionCount).Error)
	assert.Equal(t, int64(1), subscriptionCount)
}

func TestAdminDeleteUserSubscriptionRejectsGenericAsyncTaskReference(t *testing.T) {
	db := setupSubscriptionDeleteGuardTest(t)
	user := createSubscriptionDeleteGuardUser(t, db, 806, "delete-guard-task-user")
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 100, Status: "cancelled"}
	require.NoError(t, db.Create(&subscription).Error)
	require.NoError(t, db.Create(&Task{
		TaskID: "delete-guard-generic-task",
		UserId: subscription.UserId,
		Status: TaskStatusInProgress,
		PrivateData: TaskPrivateData{
			BillingSource:  BillingAdjustmentSubscription,
			SubscriptionId: subscription.Id,
		},
	}).Error)

	_, err := AdminDeleteUserSubscription(subscription.Id)
	require.ErrorContains(t, err, "referenced by async task")
	var subscriptionCount int64
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", subscription.Id).Count(&subscriptionCount).Error)
	assert.Equal(t, int64(1), subscriptionCount)
}

func TestSubscriptionBillingAdjustmentCannotEnqueueAfterHardDelete(t *testing.T) {
	db := setupSubscriptionDeleteGuardTest(t)
	user := createSubscriptionDeleteGuardUser(t, db, 803, "delete-guard-hard-delete-user")
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 100, Status: "cancelled"}
	require.NoError(t, db.Create(&subscription).Error)

	_, err := AdminDeleteUserSubscription(subscription.Id)
	require.NoError(t, err)
	_, err = EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      "delete-guard-delete-first",
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         subscription.UserId,
		SubscriptionID: subscription.Id,
		FundingDelta:   10,
	})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var taskCount int64
	require.NoError(t, db.Model(&SystemTask{}).
		Where("type = ?", SystemTaskTypeBillingAdjustment).
		Count(&taskCount).Error)
	assert.Zero(t, taskCount)
}

func TestAdminDeleteUserSubscriptionWaitsForExactMidjourneyReversal(t *testing.T) {
	db := setupSubscriptionDeleteGuardTest(t)
	user := User{Id: 804, Username: "delete-guard-reversal-user", Password: "password", AffCode: "delete-guard-reversal-aff"}
	require.NoError(t, db.Create(&user).Error)
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 100, Status: "cancelled"}
	require.NoError(t, db.Create(&subscription).Error)
	const billingRequestID = "delete-guard-midjourney-settlement"
	const billingPurpose = "midjourney-submit:IMAGINE"
	settlementTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      billingRequestID + "\x00purpose:" + billingPurpose,
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         subscription.UserId,
		SubscriptionID: subscription.Id,
		FundingDelta:   10,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessBillingAdjustment(settlementTaskID))

	midjourneyTask := Midjourney{
		UserId:                subscription.UserId,
		MjId:                  "delete-guard-midjourney",
		Status:                "SUBMITTED",
		Progress:              "0%",
		Quota:                 10,
		BillingRequestId:      billingRequestID,
		BillingPurpose:        billingPurpose,
		BillingSource:         BillingAdjustmentSubscription,
		BillingSubscriptionId: subscription.Id,
		BillingTaskId:         settlementTaskID,
		BillingFinalized:      true,
	}
	require.NoError(t, db.Create(&midjourneyTask).Error)

	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.ErrorContains(t, err, "unresolved Midjourney billing obligation")
	require.NoError(t, db.Model(&midjourneyTask).Updates(map[string]any{
		"status":   "FAILURE",
		"progress": "100%",
	}).Error)
	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.ErrorContains(t, err, "unresolved Midjourney billing obligation")

	invalidReversalPayload, err := common.Marshal(BillingAdjustment{
		RequestID:        "invalid-midjourney-reversal",
		Kind:             BillingAdjustmentRefund,
		FundingSource:    BillingAdjustmentSubscription,
		UserID:           subscription.UserId,
		SubscriptionID:   subscription.Id,
		ReversalOfTaskID: "another-settlement",
	})
	require.NoError(t, err)
	invalidReversal := SystemTask{
		TaskID:  BillingAdjustmentReversalTaskID(settlementTaskID),
		Type:    SystemTaskTypeBillingAdjustment,
		Status:  SystemTaskStatusSucceeded,
		Payload: string(invalidReversalPayload),
	}
	require.NoError(t, db.Create(&invalidReversal).Error)
	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.ErrorContains(t, err, "invalid Midjourney billing reversal")
	require.NoError(t, db.Delete(&invalidReversal).Error)

	reversalTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:        "reversal-of:" + settlementTaskID,
		Kind:             BillingAdjustmentRefund,
		FundingSource:    BillingAdjustmentSubscription,
		UserID:           subscription.UserId,
		SubscriptionID:   subscription.Id,
		ReversalOfTaskID: settlementTaskID,
	})
	require.NoError(t, err)
	assert.Equal(t, BillingAdjustmentReversalTaskID(settlementTaskID), reversalTaskID)
	require.NoError(t, ProcessBillingAdjustment(reversalTaskID))

	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.NoError(t, err)
	var subscriptionCount int64
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", subscription.Id).Count(&subscriptionCount).Error)
	assert.Zero(t, subscriptionCount)
}

func TestAdminDeleteUserSubscriptionAllowsSuccessfulMidjourneyTask(t *testing.T) {
	db := setupSubscriptionDeleteGuardTest(t)
	user := User{Id: 805, Username: "delete-guard-success-user", Password: "password", AffCode: "delete-guard-success-aff"}
	require.NoError(t, db.Create(&user).Error)
	subscription := UserSubscription{UserId: user.Id, AmountTotal: 100, Status: "cancelled"}
	require.NoError(t, db.Create(&subscription).Error)
	const billingRequestID = "delete-guard-successful-midjourney"
	const billingPurpose = "midjourney-submit:IMAGINE"
	settlementTaskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:      billingRequestID + "\x00purpose:" + billingPurpose,
		Kind:           BillingAdjustmentSettle,
		FundingSource:  BillingAdjustmentSubscription,
		UserID:         subscription.UserId,
		SubscriptionID: subscription.Id,
		FundingDelta:   10,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessBillingAdjustment(settlementTaskID))
	require.NoError(t, db.Create(&Midjourney{
		UserId:                subscription.UserId,
		MjId:                  "delete-guard-successful-midjourney",
		Status:                "SUCCESS",
		Progress:              "100%",
		Quota:                 10,
		BillingRequestId:      billingRequestID,
		BillingPurpose:        billingPurpose,
		BillingSource:         BillingAdjustmentSubscription,
		BillingSubscriptionId: subscription.Id,
		BillingTaskId:         settlementTaskID,
	}).Error)

	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.ErrorContains(t, err, "unresolved Midjourney billing obligation")
	require.NoError(t, db.Model(&Midjourney{}).
		Where("billing_task_id = ?", settlementTaskID).
		Update("billing_finalized", true).Error)
	require.NoError(t, db.Model(&Midjourney{}).
		Where("billing_task_id = ?", settlementTaskID).
		Update("billing_task_id", "missing-midjourney-settlement").Error)
	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.ErrorContains(t, err, "invalid Midjourney billing settlement")
	require.NoError(t, db.Model(&Midjourney{}).
		Where("billing_subscription_id = ?", subscription.Id).
		Update("billing_task_id", settlementTaskID).Error)
	_, err = AdminDeleteUserSubscription(subscription.Id)
	require.NoError(t, err)
}
