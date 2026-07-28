package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBillingReservationTest(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupDurableBillingSessionTest(t)
	require.NoError(t, db.AutoMigrate(
		&model.AffiliateReward{},
		&model.BillingReservation{},
		&model.TaskBillingFinalization{},
		&model.SubscriptionPlan{},
		&model.SubscriptionPreConsumeRecord{},
	))
	return db
}

func walletBillingInfo(requestID string, user model.User, token model.Token) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RequestId:       requestID,
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		OriginModelName: "billing-reservation-model",
		ForcePreConsume: true,
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_only",
		},
	}
}

func TestBillingSessionDuplicateRequestRejectsChangedInitialReservation(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, token := createDurableBillingBalances(t, db, "reservation-duplicate")
	ctx, _ := gin.CreateTestContext(nil)

	first, apiErr := NewBillingSession(ctx, walletBillingInfo("reservation-duplicate", user, token), 30)
	require.Nil(t, apiErr)
	require.Equal(t, 30, first.GetPreConsumedQuota())

	changedInfo := walletBillingInfo("reservation-duplicate", user, token)
	changed, apiErr := NewBillingSession(ctx, changedInfo, 90)
	require.Nil(t, changed)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
	assert.ErrorIs(t, apiErr, model.ErrBillingReservationContextMismatch)

	retryInfo := walletBillingInfo("reservation-duplicate", user, token)
	retry, apiErr := NewBillingSession(ctx, retryInfo, 30)
	require.Nil(t, apiErr)
	require.Equal(t, 30, retry.GetPreConsumedQuota())
	assert.Equal(t, 30, retryInfo.FinalPreConsumedQuota)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 870, user.Quota)
	assert.Equal(t, 370, token.RemainQuota)
	assert.Equal(t, 130, token.UsedQuota)

	var reservation model.BillingReservation
	require.NoError(t, db.Where("request_id = ?", "reservation-duplicate").First(&reservation).Error)
	assert.Equal(t, 30, reservation.InitialQuota)
	assert.Equal(t, 30, reservation.ReservedQuota)
	assert.Equal(t, 30, reservation.TokenReserved)
}

func TestBillingSessionRejectsSameUserReplacementTokenOnRetry(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, originalToken := createDurableBillingBalances(t, db, "reservation-token-identity")
	ctx, _ := gin.CreateTestContext(nil)
	info := walletBillingInfo("reservation-token-identity", user, originalToken)

	session, apiErr := NewBillingSession(ctx, info, 30)
	require.Nil(t, apiErr)
	require.NotNil(t, session)

	require.NoError(t, db.Unscoped().Delete(&originalToken).Error)
	replacementToken := model.Token{
		Id:          originalToken.Id,
		UserId:      user.Id,
		Key:         "reservation-token-identity-replacement",
		RemainQuota: 75,
		UsedQuota:   5,
	}
	require.NoError(t, db.Create(&replacementToken).Error)

	retryInfo := walletBillingInfo(info.RequestId, user, replacementToken)
	retry, apiErr := NewBillingSession(ctx, retryInfo, 30)
	require.Nil(t, retry)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
	assert.ErrorIs(t, apiErr, model.ErrBillingReservationContextMismatch)

	session.Refund(ctx)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&replacementToken, replacementToken.Id).Error)
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 75, replacementToken.RemainQuota)
	assert.Equal(t, 5, replacementToken.UsedQuota)
}

func TestBillingSessionZeroQuotaRefundClosesReservationWithoutChangingBalances(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, token := createDurableBillingBalances(t, db, "reservation-zero-refund")
	ctx, _ := gin.CreateTestContext(nil)
	info := walletBillingInfo("reservation-zero-refund", user, token)

	session, apiErr := NewBillingSession(ctx, info, 0)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assert.Zero(t, session.GetPreConsumedQuota())
	assert.True(t, session.NeedsRefund(), "the pending durable reservation still needs a terminal marker")

	var reservation model.BillingReservation
	require.NoError(t, db.Where("request_id = ?", info.RequestId).First(&reservation).Error)
	assert.Equal(t, "pending", reservation.Status)

	session.Refund(ctx)
	session.Refund(ctx)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 400, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
	assert.False(t, session.NeedsRefund())

	require.NoError(t, db.Where("request_id = ?", info.RequestId).First(&reservation).Error)
	assert.Equal(t, "refunded", reservation.Status)
	var adjustment model.SystemTask
	require.NoError(t, db.Where("type = ?", model.SystemTaskTypeBillingAdjustment).First(&adjustment).Error)
	assert.Equal(t, model.SystemTaskStatusSucceeded, adjustment.Status)
}

func TestBillingSessionAlwaysCreatesFundedReservation(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, token := createDurableBillingBalances(t, db, "reservation-trusted")
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	ctx, _ := gin.CreateTestContext(nil)

	info := walletBillingInfo("reservation-trusted", user, token)
	info.ForcePreConsume = false
	info.TokenUnlimited = true
	first, apiErr := NewBillingSession(ctx, info, 30)
	require.Nil(t, apiErr)
	assert.Equal(t, 30, first.GetPreConsumedQuota())
	assert.True(t, first.NeedsRefund())

	retryInfo := walletBillingInfo("reservation-trusted", user, token)
	retryInfo.ForcePreConsume = false
	retryInfo.TokenUnlimited = true
	retry, apiErr := NewBillingSession(ctx, retryInfo, 30)
	require.Nil(t, apiErr)
	assert.Equal(t, 30, retry.GetPreConsumedQuota())

	changedInfo := walletBillingInfo("reservation-trusted", user, token)
	changedInfo.ForcePreConsume = false
	changedInfo.TokenUnlimited = true
	changed, apiErr := NewBillingSession(ctx, changedInfo, 31)
	require.Nil(t, changed)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
	assert.ErrorIs(t, apiErr, model.ErrBillingReservationContextMismatch)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 870, user.Quota)
	assert.Equal(t, 370, token.RemainQuota)
	assert.Equal(t, 130, token.UsedQuota)

	var reservation model.BillingReservation
	require.NoError(t, db.Where("request_id = ?", info.RequestId).First(&reservation).Error)
	assert.Equal(t, 30, reservation.RequestedQuota)
	assert.Equal(t, 30, reservation.InitialQuota)
	assert.Equal(t, 30, reservation.ReservedQuota)
	assert.False(t, reservation.TrustBypass)
	require.NoError(t, first.Reserve(30))
	require.NoError(t, first.Reserve(31))
}

func TestBillingSessionFundsLegacyTrustReservationOnRetry(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, token := createDurableBillingBalances(t, db, "reservation-legacy-trust")
	requestID := "reservation-legacy-trust"
	require.NoError(t, db.Create(&model.BillingReservation{
		RequestID:      requestID,
		ClaimToken:     "legacy-claim",
		UserID:         user.Id,
		TokenID:        token.Id,
		FundingSource:  model.BillingAdjustmentWallet,
		ModelName:      "billing-reservation-model",
		RequestedQuota: 30,
		InitialQuota:   0,
		TrustBypass:    true,
	}).Error)

	ctx, _ := gin.CreateTestContext(nil)
	info := walletBillingInfo(requestID, user, token)
	info.ForcePreConsume = false
	session, apiErr := NewBillingSession(ctx, info, 30)
	require.Nil(t, apiErr)
	require.Equal(t, 30, session.GetPreConsumedQuota())

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 870, user.Quota)
	assert.Equal(t, 370, token.RemainQuota)
	assert.Equal(t, 130, token.UsedQuota)

	var reservation model.BillingReservation
	require.NoError(t, db.Where("request_id = ?", requestID).First(&reservation).Error)
	assert.Equal(t, 30, reservation.InitialQuota)
	assert.Equal(t, 30, reservation.ReservedQuota)
	assert.Equal(t, 30, reservation.TokenReserved)
	assert.False(t, reservation.TrustBypass)
}

func TestBillingSessionTrustBypassRequiresTargetAndBufferCoverage(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, token := createDurableBillingBalances(t, db, "reservation-trust-target")
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 10 // trust buffer = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	require.NoError(t, db.Model(&user).Update("quota", 150).Error)
	require.NoError(t, db.Model(&token).Updates(map[string]interface{}{"remain_quota": 150, "used_quota": 0}).Error)
	user.Quota = 150
	token.RemainQuota = 150
	token.UsedQuota = 0

	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set("token_quota", 150)
	info := walletBillingInfo("reservation-trust-target", user, token)
	info.ForcePreConsume = false
	session, apiErr := NewBillingSession(ctx, info, 100)
	require.Nil(t, apiErr)
	require.Equal(t, 100, session.GetPreConsumedQuota(), "balance above the buffer alone must not authorize an oversized target")

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 50, user.Quota)
	assert.Equal(t, 50, token.RemainQuota)
	var reservation model.BillingReservation
	require.NoError(t, db.Where("request_id = ?", info.RequestId).First(&reservation).Error)
	assert.False(t, reservation.TrustBypass)
	assert.Equal(t, 100, reservation.ReservedQuota)
}

func TestBillingSessionParamOverrideDisablesTrustBypass(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, token := createDurableBillingBalances(t, db, "reservation-trust-override")
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	ctx, _ := gin.CreateTestContext(nil)

	info := walletBillingInfo("reservation-trust-override", user, token)
	info.ForcePreConsume = false
	info.TokenUnlimited = true
	info.ChannelMeta = &relaycommon.ChannelMeta{ParamOverride: map[string]interface{}{"max_tokens": 100_000}}
	session, apiErr := NewBillingSession(ctx, info, 30)
	require.Nil(t, apiErr)
	assert.Equal(t, 30, session.GetPreConsumedQuota())

	var reservation model.BillingReservation
	require.NoError(t, db.Where("request_id = ?", info.RequestId).First(&reservation).Error)
	assert.False(t, reservation.TrustBypass)
	assert.Equal(t, 30, reservation.ReservedQuota)
}

func TestBillingSessionMeteredServerToolsDisableTrustBypass(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, token := createDurableBillingBalances(t, db, "reservation-trust-tools")
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	ctx, _ := gin.CreateTestContext(nil)

	requests := []struct {
		name    string
		request dto.Request
	}{
		{
			name: "Responses web search",
			request: &dto.OpenAIResponsesRequest{
				Model: "gpt-5", Input: json.RawMessage(`"search"`),
				Tools: json.RawMessage(`[{"type":"web_search"}]`),
			},
		},
		{
			name: "Claude web search",
			request: &dto.ClaudeRequest{
				Model: "claude-sonnet", Tools: []map[string]interface{}{{"type": "web_search_20250305", "name": "web_search"}},
			},
		},
		{
			name: "Gemini grounding",
			request: &dto.GeminiChatRequest{
				Tools: json.RawMessage(`[{"googleSearch":{}}]`),
			},
		},
		{
			name: "Chat search preview",
			request: &dto.GeneralOpenAIRequest{
				Model: "gpt-4o-search-preview",
			},
		},
	}

	for index, test := range requests {
		t.Run(test.name, func(t *testing.T) {
			info := walletBillingInfo(fmt.Sprintf("reservation-trust-tool-%d", index), user, token)
			info.ForcePreConsume = false
			info.TokenUnlimited = true
			info.Request = test.request

			session, apiErr := NewBillingSession(ctx, info, 30)
			require.Nil(t, apiErr)
			require.Equal(t, 30, session.GetPreConsumedQuota())

			var reservation model.BillingReservation
			require.NoError(t, db.Where("request_id = ?", info.RequestId).First(&reservation).Error)
			assert.False(t, reservation.TrustBypass)
			assert.Equal(t, 30, reservation.ReservedQuota)
		})
	}
}

func TestBillingSessionInitialReservationRollsBackFundingWhenTokenFails(t *testing.T) {
	db := setupBillingReservationTest(t)
	user := model.User{Username: "reservation-token-failure", Password: "password", Quota: 900, AffCode: "aff-reservation-token-failure"}
	require.NoError(t, db.Create(&user).Error)
	missingToken := model.Token{Id: 987654, UserId: user.Id, Key: "missing-token"}
	ctx, _ := gin.CreateTestContext(nil)

	session, apiErr := NewBillingSession(ctx, walletBillingInfo("reservation-token-failure", user, missingToken), 30)
	require.Nil(t, session)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodePreConsumeTokenQuotaFailed, apiErr.GetErrorCode())
	assert.True(t, errors.Is(apiErr, model.ErrBillingReservationInsufficientToken))

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 900, user.Quota)
	var count int64
	require.NoError(t, db.Model(&model.BillingReservation{}).Where("request_id = ?", "reservation-token-failure").Count(&count).Error)
	assert.Zero(t, count)
}

func TestBillingSessionPlaygroundReservationSkipsTokenAndRefundsWallet(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, token := createDurableBillingBalances(t, db, "reservation-playground")
	info := walletBillingInfo("reservation-playground", user, token)
	info.IsPlayground = true
	ctx, _ := gin.CreateTestContext(nil)

	session, apiErr := NewBillingSession(ctx, info, 30)
	require.Nil(t, apiErr)
	assert.True(t, session.NeedsRefund())
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 870, user.Quota)
	assert.Equal(t, 400, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)

	session.Refund(ctx)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 400, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
}

func TestBillingSessionReserveIsAtomicAndCumulative(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, token := createDurableBillingBalances(t, db, "reservation-extend")
	require.NoError(t, db.Model(&token).Updates(map[string]interface{}{"remain_quota": 40, "used_quota": 0}).Error)
	token.RemainQuota = 40
	token.UsedQuota = 0
	info := walletBillingInfo("reservation-extend", user, token)
	ctx, _ := gin.CreateTestContext(nil)

	session, apiErr := NewBillingSession(ctx, info, 30)
	require.Nil(t, apiErr)
	require.Equal(t, 30, session.GetPreConsumedQuota())

	err := session.Reserve(60)
	require.Error(t, err)
	var reserveAPIError *types.NewAPIError
	require.ErrorAs(t, err, &reserveAPIError)
	assert.Equal(t, types.ErrorCodePreConsumeTokenQuotaFailed, reserveAPIError.GetErrorCode())
	assert.Equal(t, 30, session.GetPreConsumedQuota())

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 870, user.Quota, "wallet extension must roll back with the failed token extension")
	assert.Equal(t, 10, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)

	require.NoError(t, db.Model(&token).Update("remain_quota", 100).Error)
	require.NoError(t, session.Reserve(60))
	require.NoError(t, session.Reserve(60))
	assert.Equal(t, 60, session.GetPreConsumedQuota())
	assert.Equal(t, 60, info.FinalPreConsumedQuota)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 840, user.Quota)
	assert.Equal(t, 70, token.RemainQuota)
	assert.Equal(t, 60, token.UsedQuota)

	var reservation model.BillingReservation
	require.NoError(t, db.Where("request_id = ?", "reservation-extend").First(&reservation).Error)
	assert.Equal(t, 60, reservation.ReservedQuota)
	assert.Equal(t, 60, reservation.TokenReserved)
}

func TestBillingSessionSubscriptionReserveKeepsRefundCanonical(t *testing.T) {
	db := setupBillingReservationTest(t)
	user, token := createDurableBillingBalances(t, db, "reservation-subscription")
	plan := model.SubscriptionPlan{Title: "Reservation plan", Enabled: true, TotalAmount: 200}
	require.NoError(t, db.Create(&plan).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })
	subscription := model.UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 200,
		Status:      "active",
		StartTime:   common.GetTimestamp() - 60,
		EndTime:     common.GetTimestamp() + 3600,
	}
	require.NoError(t, db.Create(&subscription).Error)
	info := walletBillingInfo("reservation-subscription", user, token)
	info.UserSetting.BillingPreference = "subscription_only"
	ctx, _ := gin.CreateTestContext(nil)

	session, apiErr := NewBillingSession(ctx, info, 30)
	require.Nil(t, apiErr)
	require.NoError(t, session.Reserve(80))
	assert.Equal(t, 80, session.GetPreConsumedQuota())
	assert.Equal(t, int64(80), info.SubscriptionPreConsumed)
	assert.Equal(t, subscription.Id, info.SubscriptionId)

	var preConsume model.SubscriptionPreConsumeRecord
	require.NoError(t, db.Where("request_id = ?", info.RequestId).First(&preConsume).Error)
	assert.Equal(t, int64(80), preConsume.PreConsumed)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Equal(t, int64(80), subscription.AmountUsed)

	duplicateInfo := walletBillingInfo(info.RequestId, user, token)
	duplicateInfo.UserSetting.BillingPreference = "subscription_only"
	duplicateSession, duplicateErr := NewBillingSession(ctx, duplicateInfo, 30)
	require.Nil(t, duplicateErr)
	assert.Equal(t, 80, duplicateSession.GetPreConsumedQuota())
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Equal(t, int64(80), subscription.AmountUsed)

	session.Refund(ctx)
	duplicateSession.Refund(ctx)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, int64(0), subscription.AmountUsed)
	assert.Equal(t, 400, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
	require.NoError(t, db.Where("request_id = ?", info.RequestId).First(&preConsume).Error)
	assert.Equal(t, "refunded", preConsume.Status)
}
