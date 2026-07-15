package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyDurableQuotaAdjustmentIsAtomicIdempotentAndPurposeScoped(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user, token := createDurableBillingBalances(t, db, "legacy-purpose")
	info := &relaycommon.RelayInfo{
		RequestId: "legacy-purpose-request",
		UserId:    user.Id,
		UserQuota: user.Quota,
		TokenId:   token.Id,
		TokenKey:  token.Key,
	}

	_, applied, err := ApplyDurableQuotaAdjustment(info, "violation-fee", 30, 0, false)
	require.NoError(t, err)
	assert.True(t, applied)
	_, applied, err = ApplyDurableQuotaAdjustment(info, "violation-fee", 30, 0, false)
	require.NoError(t, err)
	assert.False(t, applied)
	_, applied, err = ApplyDurableQuotaAdjustment(info, "midjourney-submit", 30, 0, false)
	require.NoError(t, err)
	assert.True(t, applied)
	_, applied, err = ApplyDurableQuotaAdjustment(info, "midjourney-free-submit", 0, 0, false)
	require.NoError(t, err)
	assert.True(t, applied)
	_, applied, err = ApplyDurableQuotaAdjustment(info, "midjourney-free-submit", 0, 0, false)
	require.NoError(t, err)
	assert.False(t, applied)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 840, user.Quota)
	assert.Equal(t, 340, token.RemainQuota)
	assert.Equal(t, 160, token.UsedQuota)
	var taskCount int64
	require.NoError(t, db.Model(&model.SystemTask{}).Count(&taskCount).Error)
	assert.Equal(t, int64(3), taskCount)
}

func TestApplyDurableQuotaAdjustmentPreservesPlaygroundTokenBalance(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user, token := createDurableBillingBalances(t, db, "legacy-playground")
	info := &relaycommon.RelayInfo{
		RequestId:    "legacy-playground-request",
		UserId:       user.Id,
		TokenId:      token.Id,
		TokenKey:     token.Key,
		IsPlayground: true,
	}

	_, applied, err := ApplyDurableQuotaAdjustment(info, "settlement", 25, 0, false)
	require.NoError(t, err)
	assert.True(t, applied)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 875, user.Quota)
	assert.Equal(t, 400, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
}

func TestApplyDurableQuotaAdjustmentPreservesSubscriptionFunding(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user, token := createDurableBillingBalances(t, db, "legacy-subscription")
	subscription := model.UserSubscription{
		UserId:      user.Id,
		AmountTotal: 1_000,
		AmountUsed:  10,
	}
	require.NoError(t, db.Create(&subscription).Error)
	info := &relaycommon.RelayInfo{
		RequestId:      "legacy-subscription-request",
		UserId:         user.Id,
		TokenId:        token.Id,
		TokenKey:       token.Key,
		BillingSource:  BillingSourceSubscription,
		SubscriptionId: subscription.Id,
	}

	result, applied, err := ApplyDurableQuotaAdjustment(info, "settlement", 20, 0, false)
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, 20, result.SubscriptionDelta)
	_, applied, err = ApplyDurableQuotaAdjustment(info, "settlement", 20, 0, false)
	require.NoError(t, err)
	assert.False(t, applied)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 380, token.RemainQuota)
	assert.Equal(t, 120, token.UsedQuota)
	assert.Equal(t, int64(30), subscription.AmountUsed)
	assert.Equal(t, int64(20), info.SubscriptionPostDelta)
}

func TestApplyDurableQuotaAdjustmentRejectsPurposeReuseWithDifferentDelta(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user, token := createDurableBillingBalances(t, db, "legacy-mismatch")
	info := &relaycommon.RelayInfo{
		RequestId: "legacy-mismatch-request",
		UserId:    user.Id,
		TokenId:   token.Id,
		TokenKey:  token.Key,
	}

	_, _, err := ApplyDurableQuotaAdjustment(info, "settlement", 10, 0, false)
	require.NoError(t, err)
	_, _, err = ApplyDurableQuotaAdjustment(info, "settlement", 11, 0, false)
	assert.ErrorContains(t, err, "idempotency key reused with different payload")
}

func TestSettleBillingLegacyFallbackUsesOneDurableDelta(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user, token := createDurableBillingBalances(t, db, "legacy-settlement")
	info := &relaycommon.RelayInfo{
		RequestId:             "legacy-settlement-request",
		UserId:                user.Id,
		UserQuota:             user.Quota + 20,
		TokenId:               token.Id,
		TokenKey:              token.Key,
		FinalPreConsumedQuota: 20,
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.NoError(t, SettleBilling(ctx, info, 0))
	require.NoError(t, SettleBilling(ctx, info, 0))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 920, user.Quota)
	assert.Equal(t, 420, token.RemainQuota)
	assert.Equal(t, 80, token.UsedQuota)
	var tasks int64
	require.NoError(t, db.Model(&model.SystemTask{}).Count(&tasks).Error)
	assert.Equal(t, int64(1), tasks)
}
