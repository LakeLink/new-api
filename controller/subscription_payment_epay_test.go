package controller

import (
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompleteSubscriptionEpayCallbackValidatesAmountAndIsIdempotent(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.SubscriptionPlan{},
		&model.SubscriptionOrder{},
		&model.SubscriptionProviderPayment{},
		&model.UserSubscription{},
		&model.TopUp{},
	))
	user := &model.User{Id: 701, Username: "epay-sub", Group: "default", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(user).Error)
	allowBalance := false
	plan := &model.SubscriptionPlan{
		Title: "Epay plan", PriceAmount: 9.99, Enabled: true,
		DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1,
		AllowBalancePay: &allowBalance,
	}
	require.NoError(t, db.Create(plan).Error)
	order := &model.SubscriptionOrder{
		UserId: 701, PlanId: plan.Id, Money: 9.99, TradeNo: "epay-sub-order",
		PaymentMethod: "alipay", PaymentProvider: model.PaymentProviderEpay,
		Status: common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())

	callback := &epay.VerifyRes{
		Type: "alipay", TradeNo: "epay-provider-order", ServiceTradeNo: order.TradeNo,
		Money: "9.98", TradeStatus: epay.StatusTradeSuccess, VerifyStatus: true,
	}
	require.Error(t, completeSubscriptionEpayCallback(callback))
	assert.Equal(t, common.TopUpStatusPending, model.GetSubscriptionOrderByTradeNo(order.TradeNo).Status)

	callback.Money = "9.99"
	require.NoError(t, completeSubscriptionEpayCallback(callback))
	require.NoError(t, completeSubscriptionEpayCallback(callback))
	callback.TradeNo = "epay-provider-other"
	require.Error(t, completeSubscriptionEpayCallback(callback))
	callback.TradeNo = "epay-provider-order"
	completed := model.GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.NotNil(t, completed)
	assert.Equal(t, common.TopUpStatusSuccess, completed.Status)
	assert.Equal(t, callback.TradeNo, completed.ProviderPaymentId)
	var count int64
	require.NoError(t, db.Model(&model.UserSubscription{}).Where("subscription_order_id = ?", order.Id).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}
