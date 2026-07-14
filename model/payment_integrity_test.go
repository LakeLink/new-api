package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopUpQuotaForAmountPreservesTokensAndRejectsOverflow(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })
	common.QuotaPerUnit = 500000

	quota, err := TopUpQuotaForAmount(12345, true)
	require.NoError(t, err)
	assert.Equal(t, 12345, quota)

	quota, err = TopUpQuotaForAmount(2, false)
	require.NoError(t, err)
	assert.Equal(t, 1000000, quota)

	_, err = TopUpQuotaForAmount(int64(common.MaxQuota)+1, true)
	require.Error(t, err)
	_, err = TopUpQuotaForAmount(5000, false)
	require.Error(t, err)
}

func TestStripeRechargeUsesPersistedQuotaAndIsIdempotent(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 501, 10)
	require.NoError(t, (&TopUp{
		UserId: 501, Amount: 999999, Quota: 123, Money: 7.25,
		TradeNo: "stripe-exact-quota", PaymentMethod: PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusPending,
	}).Insert())

	require.NoError(t, Recharge("stripe-exact-quota", "cus_1", "pi_1", "127.0.0.1"))
	require.NoError(t, Recharge("stripe-exact-quota", "cus_1", "pi_1", "127.0.0.1"))
	assert.Equal(t, 133, getUserQuotaForPaymentGuardTest(t, 501))
	topUp := GetTopUpByTradeNo("stripe-exact-quota")
	require.NotNil(t, topUp)
	assert.Equal(t, 123, topUp.Quota)
	assert.Equal(t, "pi_1", topUp.ProviderPaymentId)
}

func TestEpayRechargeUsesPersistedQuotaAndProtectsBalanceRange(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 505, 10)
	require.NoError(t, (&TopUp{
		UserId: 505, Amount: 999999, Quota: 123, Money: 7.25,
		TradeNo: "epay-exact-quota", PaymentMethod: "alipay",
		PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending,
	}).Insert())

	require.NoError(t, RechargeEpay("epay-exact-quota", "alipay", "epay-payment-1", "127.0.0.1"))
	require.NoError(t, RechargeEpay("epay-exact-quota", "alipay", "epay-payment-1", "127.0.0.1"))
	require.Error(t, RechargeEpay("epay-exact-quota", "alipay", "epay-payment-other", "127.0.0.1"))
	assert.Equal(t, 133, getUserQuotaForPaymentGuardTest(t, 505))
	topUp := GetTopUpByTradeNo("epay-exact-quota")
	require.NotNil(t, topUp)
	assert.Equal(t, "epay-payment-1", topUp.ProviderPaymentId)

	require.NoError(t, DB.Create(&User{
		Id: 506, Username: "payment_guard_overflow", AffCode: "epay-overflow-user",
		Status: common.UserStatusEnabled, Quota: common.MaxQuota - 10,
	}).Error)
	require.NoError(t, (&TopUp{
		UserId: 506, Amount: 1, Quota: 20, Money: 1,
		TradeNo: "epay-overflow", PaymentMethod: "alipay",
		PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending,
	}).Insert())
	require.Error(t, RechargeEpay("epay-overflow", "alipay", "epay-payment-2", "127.0.0.1"))
	assert.Equal(t, common.MaxQuota-10, getUserQuotaForPaymentGuardTest(t, 506))
	topUp = GetTopUpByTradeNo("epay-overflow")
	require.NotNil(t, topUp)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
}

func TestTopUpReversalUsesCumulativeRefundAndIsIdempotent(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 502, 1100)
	require.NoError(t, (&TopUp{
		UserId: 502, Amount: 1, Quota: 1000, Money: 10,
		TradeNo: "stripe-refund", ProviderPaymentId: "pi_refund",
		PaymentMethod: PaymentMethodStripe, PaymentProvider: PaymentProviderStripe,
		Status: common.TopUpStatusSuccess,
	}).Insert())

	require.NoError(t, ReverseTopUpByProviderPayment(PaymentProviderStripe, "pi_refund", 2.5, false))
	require.NoError(t, ReverseTopUpByProviderPayment(PaymentProviderStripe, "pi_refund", 2.5, false))
	assert.Equal(t, 850, getUserQuotaForPaymentGuardTest(t, 502))

	require.NoError(t, ReverseTopUpByProviderPayment(PaymentProviderStripe, "pi_refund", 0, true))
	require.NoError(t, ReverseTopUpByProviderPayment(PaymentProviderStripe, "pi_refund", 0, true))
	assert.Equal(t, 100, getUserQuotaForPaymentGuardTest(t, 502))
	topUp := GetTopUpByTradeNo("stripe-refund")
	require.NotNil(t, topUp)
	assert.Equal(t, TopUpStatusRefunded, topUp.Status)
	assert.Equal(t, 1000, topUp.RefundedQuota)
}

func TestOverlappingUpgradeSubscriptionsRestoreOriginalGroup(t *testing.T) {
	truncateTables(t)
	user := &User{Id: 503, Username: "overlap", Group: "base", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(user).Error)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 503)
	plan.UpgradeGroup = "vip"
	require.NoError(t, DB.Save(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	insertSubscriptionOrderForPaymentGuardTest(t, "overlap-one", user.Id, plan.Id, PaymentProviderStripe)
	require.NoError(t, CompleteSubscriptionOrder("overlap-one", "", PaymentProviderStripe, ""))
	insertSubscriptionOrderForPaymentGuardTest(t, "overlap-two", user.Id, plan.Id, PaymentProviderStripe)
	require.NoError(t, CompleteSubscriptionOrder("overlap-two", "", PaymentProviderStripe, ""))

	var subs []UserSubscription
	require.NoError(t, DB.Where("user_id = ?", user.Id).Order("id asc").Find(&subs).Error)
	require.Len(t, subs, 2)
	assert.Equal(t, "base", subs[0].PrevUserGroup)
	assert.Equal(t, "base", subs[1].PrevUserGroup)

	_, err := AdminInvalidateUserSubscription(subs[0].Id)
	require.NoError(t, err)
	group, err := GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "vip", group)

	_, err = AdminInvalidateUserSubscription(subs[1].Id)
	require.NoError(t, err)
	group, err = GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "base", group)
}

func TestProviderSubscriptionRenewalIsIdempotent(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 504, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 504)
	insertSubscriptionOrderForPaymentGuardTest(t, "stripe-sub-initial", 504, plan.Id, PaymentProviderStripe)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiers(
		"stripe-sub-initial", PaymentProviderStripe, "cus_sub", "sub_1", "pi_initial", "active", "",
	))
	require.NoError(t, CompleteSubscriptionOrder("stripe-sub-initial", "", PaymentProviderStripe, ""))

	var initial UserSubscription
	require.NoError(t, DB.Where("user_id = ?", 504).First(&initial).Error)
	require.NoError(t, RenewProviderSubscription(PaymentProviderStripe, "sub_1", "in_1", "pi_renew", 9.99, "{}"))
	var renewed UserSubscription
	require.NoError(t, DB.Where("id = ?", initial.Id).First(&renewed).Error)
	assert.Greater(t, renewed.EndTime, initial.EndTime)

	require.NoError(t, RenewProviderSubscription(PaymentProviderStripe, "sub_1", "in_1", "pi_renew", 9.99, "{}"))
	var repeated UserSubscription
	require.NoError(t, DB.Where("id = ?", initial.Id).First(&repeated).Error)
	assert.Equal(t, renewed.EndTime, repeated.EndTime)
	var renewalCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).Where("trade_no = ?", "stripe_invoice_in_1").Count(&renewalCount).Error)
	assert.Equal(t, int64(1), renewalCount)
}

func TestSubscriptionPlanValidationRejectsInvalidDurationAndUnsafeBalancePrice(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })
	common.QuotaPerUnit = 500000

	plan := &SubscriptionPlan{PriceAmount: 1, DurationUnit: SubscriptionDurationCustom, CustomSeconds: 0}
	require.Error(t, ValidateSubscriptionPlanForPurchase(plan))

	plan = &SubscriptionPlan{PriceAmount: 9999, DurationUnit: SubscriptionDurationMonth, DurationValue: 1}
	require.Error(t, ValidateSubscriptionPlanForPurchase(plan))

	allowBalance := false
	plan.AllowBalancePay = &allowBalance
	require.NoError(t, ValidateSubscriptionPlanForPurchase(plan))

	plan.CustomSeconds = int64((100*366*24*time.Hour)/time.Second) + 1
	plan.DurationUnit = SubscriptionDurationCustom
	require.Error(t, ValidateSubscriptionPlanForPurchase(plan))
}

func TestValidateOptionValueRejectsUnsafeQuotaPerUnit(t *testing.T) {
	require.Error(t, validateOptionValue("QuotaPerUnit", "NaN"))
	require.Error(t, validateOptionValue("QuotaPerUnit", "0"))
	require.Error(t, validateOptionValue("QuotaPerUnit", "2147483648"))
	require.NoError(t, validateOptionValue("QuotaPerUnit", "500000"))
}
