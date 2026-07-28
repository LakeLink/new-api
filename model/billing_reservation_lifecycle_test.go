package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCleanupBillingReservationsPreservesTerminalIdempotencyTombstone(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &BillingReservation{}, &SystemTask{})
	user := User{Username: "reservation-cleanup", Password: "password", Quota: 1_000, AffCode: "reservation-cleanup-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "reservation-cleanup-token", RemainQuota: 1_000}
	require.NoError(t, db.Create(&token).Error)

	request := BillingReservationRequest{
		RequestID:      "reservation-cleanup-request",
		UserID:         user.Id,
		TokenID:        token.Id,
		TokenKey:       token.Key,
		FundingSource:  BillingAdjustmentWallet,
		ModelName:      "reservation-cleanup-model",
		RequestedQuota: 100,
		InitialQuota:   100,
	}
	_, err := CreateBillingReservation(request)
	require.NoError(t, err)
	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:     request.RequestID,
		Kind:          BillingAdjustmentSettle,
		FundingSource: BillingAdjustmentWallet,
		UserID:        user.Id,
		TokenID:       token.Id,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessBillingAdjustment(taskID))
	require.NoError(t, db.Model(&BillingReservation{}).
		Where("request_id = ?", request.RequestID).
		Update("updated_at", common.GetTimestamp()-1_000).Error)

	deleted, err := CleanupBillingReservations(100)
	require.NoError(t, err)
	assert.Zero(t, deleted)

	_, err = CreateBillingReservation(request)
	require.ErrorIs(t, err, ErrBillingReservationTerminal)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 900, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
}

func TestBillingAdjustmentClosesMatchingReservationAtomically(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &Token{}, &BillingReservation{}, &SystemTask{})
	user := User{Username: "reservation-terminal", Password: "password", Quota: 1_000, AffCode: "reservation-terminal-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "reservation-terminal-token", RemainQuota: 1_000}
	require.NoError(t, db.Create(&token).Error)

	request := BillingReservationRequest{
		RequestID:      "reservation-terminal-request",
		UserID:         user.Id,
		TokenID:        token.Id,
		TokenKey:       token.Key,
		FundingSource:  BillingAdjustmentWallet,
		ModelName:      "reservation-terminal-model",
		RequestedQuota: 100,
		InitialQuota:   100,
	}
	_, err := CreateBillingReservation(request)
	require.NoError(t, err)
	taskID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:     request.RequestID,
		Kind:          BillingAdjustmentSettle,
		FundingSource: BillingAdjustmentWallet,
		UserID:        user.Id,
		TokenID:       token.Id,
	})
	require.NoError(t, err)
	require.NoError(t, ProcessBillingAdjustment(taskID))

	var reservation BillingReservation
	require.NoError(t, db.Where("request_id = ?", request.RequestID).First(&reservation).Error)
	assert.Equal(t, billingReservationStatusSettled, reservation.Status)

	_, err = CreateBillingReservation(request)
	require.ErrorIs(t, err, ErrBillingReservationTerminal)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 900, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
}

func TestBillingReservationRejectsConflictingTerminalTransition(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &BillingReservation{})
	reservation := BillingReservation{
		RequestID:     "reservation-conflicting-terminal",
		ClaimToken:    "claim",
		UserID:        1,
		TokenID:       1,
		FundingSource: BillingAdjustmentWallet,
		Status:        billingReservationStatusPending,
	}
	require.NoError(t, db.Create(&reservation).Error)
	require.NoError(t, MarkBillingReservationTerminal(reservation.RequestID, BillingAdjustmentSettle))
	require.ErrorIs(t, MarkBillingReservationTerminal(reservation.RequestID, BillingAdjustmentRefund), ErrBillingReservationTerminal)
}

func TestZeroDeltaSubscriptionSettlementClosesPreConsumeProof(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &BillingReservation{}, &SubscriptionPreConsumeRecord{})
	reservation := BillingReservation{
		RequestID:      "subscription-zero-settlement",
		ClaimToken:     "claim",
		UserID:         7,
		TokenID:        8,
		FundingSource:  BillingAdjustmentSubscription,
		ReservedQuota:  90,
		TokenReserved:  90,
		SubscriptionID: 9,
		Status:         billingReservationStatusPending,
	}
	require.NoError(t, db.Create(&reservation).Error)
	proof := SubscriptionPreConsumeRecord{
		RequestId:          reservation.RequestID,
		UserId:             reservation.UserID,
		UserSubscriptionId: reservation.SubscriptionID,
		PreConsumed:        int64(reservation.ReservedQuota),
		Status:             "consumed",
	}
	require.NoError(t, db.Create(&proof).Error)

	require.NoError(t, MarkBillingReservationTerminal(reservation.RequestID, BillingAdjustmentSettle))
	require.NoError(t, db.First(&reservation, reservation.ID).Error)
	require.NoError(t, db.First(&proof, proof.Id).Error)
	assert.Equal(t, billingReservationStatusSettled, reservation.Status)
	assert.Equal(t, "settled", proof.Status)
}

func TestSubscriptionBillingReservationRequiresLiveUser(t *testing.T) {
	db := setupBillingAdjustmentTestDB(
		t,
		&User{},
		&Token{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&SystemTask{},
	)
	const missingUserID = 406
	subscription := UserSubscription{
		UserId:      missingUserID,
		AmountTotal: 100,
		Status:      "active",
		EndTime:     GetDBTimestamp() + 3600,
	}
	require.NoError(t, db.Create(&subscription).Error)
	token := Token{UserId: missingUserID, Key: "missing-user-token", RemainQuota: 100}
	require.NoError(t, db.Create(&token).Error)

	_, err := CreateBillingReservation(BillingReservationRequest{
		RequestID:      "missing-subscription-reservation-user",
		UserID:         missingUserID,
		TokenID:        token.Id,
		TokenKey:       token.Key,
		FundingSource:  BillingAdjustmentSubscription,
		ModelName:      "missing-user-model",
		RequestedQuota: 10,
		InitialQuota:   10,
	})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var reservationCount int64
	require.NoError(t, db.Model(&BillingReservation{}).Count(&reservationCount).Error)
	assert.Zero(t, reservationCount)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Zero(t, subscription.AmountUsed)
}
