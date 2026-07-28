package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPreConsumeUserSubscriptionPropagatesActiveSubscriptionQueryFailure(t *testing.T) {
	db := setupBillingAdjustmentTestDB(
		t,
		&SubscriptionPreConsumeRecord{},
		&UserSubscription{},
	)
	injectedErr := errors.New("injected active subscription query failure")
	const callbackName = "test:fail_preconsume_subscription_query"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "user_subscriptions" {
				_ = tx.AddError(injectedErr)
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(callbackName))
	})

	result, err := PreConsumeUserSubscription(
		"preconsume-query-failure",
		901,
		"test-model",
		0,
		10,
	)
	require.ErrorIs(t, err, injectedErr)
	assert.Nil(t, result)
	assert.NotContains(t, err.Error(), "no active subscription")
}

func TestResetDueSubscriptionsPropagatesPlanQueryFailure(t *testing.T) {
	db := setupBillingAdjustmentTestDB(
		t,
		&SubscriptionPlan{},
		&UserSubscription{},
	)
	const (
		planID         = 9851
		subscriptionID = 9852
	)
	now := GetDBTimestamp()
	require.NoError(t, db.Create(&SubscriptionPlan{
		Id:               planID,
		Title:            "maintenance query failure",
		QuotaResetPeriod: SubscriptionResetDaily,
	}).Error)
	require.NoError(t, db.Create(&UserSubscription{
		Id:            subscriptionID,
		UserId:        9853,
		PlanId:        planID,
		AmountTotal:   100,
		AmountUsed:    80,
		EndTime:       now + 86_400,
		Status:        "active",
		NextResetTime: now - 1,
	}).Error)
	InvalidateSubscriptionPlanCache(planID)

	injectedErr := errors.New("injected reset plan query failure")
	const callbackName = "test:fail_reset_plan_query"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "subscription_plans" {
				_ = tx.AddError(injectedErr)
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(callbackName))
		InvalidateSubscriptionPlanCache(planID)
	})

	resetCount, err := ResetDueSubscriptions(10)
	require.ErrorIs(t, err, injectedErr)
	assert.Zero(t, resetCount)

	var subscription UserSubscription
	require.NoError(t, db.First(&subscription, subscriptionID).Error)
	assert.Equal(t, int64(80), subscription.AmountUsed)
}

func TestResetDueSubscriptionsPropagatesLockedSubscriptionQueryFailure(t *testing.T) {
	db := setupBillingAdjustmentTestDB(
		t,
		&SubscriptionPlan{},
		&UserSubscription{},
	)
	const (
		planID         = 9861
		subscriptionID = 9862
	)
	now := GetDBTimestamp()
	require.NoError(t, db.Create(&SubscriptionPlan{
		Id:               planID,
		Title:            "maintenance lock failure",
		QuotaResetPeriod: SubscriptionResetDaily,
	}).Error)
	require.NoError(t, db.Create(&UserSubscription{
		Id:            subscriptionID,
		UserId:        9863,
		PlanId:        planID,
		AmountTotal:   100,
		AmountUsed:    70,
		EndTime:       now + 86_400,
		Status:        "active",
		NextResetTime: now - 1,
	}).Error)
	InvalidateSubscriptionPlanCache(planID)

	injectedErr := errors.New("injected reset lock query failure")
	subscriptionQueryCount := 0
	const callbackName = "test:fail_reset_lock_query"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "user_subscriptions" {
				return
			}
			subscriptionQueryCount++
			if subscriptionQueryCount == 2 {
				_ = tx.AddError(injectedErr)
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(callbackName))
		InvalidateSubscriptionPlanCache(planID)
	})

	resetCount, err := ResetDueSubscriptions(10)
	require.ErrorIs(t, err, injectedErr)
	assert.Zero(t, resetCount)

	var subscription UserSubscription
	require.NoError(t, db.First(&subscription, subscriptionID).Error)
	assert.Equal(t, int64(70), subscription.AmountUsed)
}
