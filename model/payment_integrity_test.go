package model

import (
	"fmt"
	"math"
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

	require.NoError(t, Recharge("stripe-exact-quota", "cus_1", "pi_1", 725, "127.0.0.1"))
	require.NoError(t, Recharge("stripe-exact-quota", "cus_1", "pi_1", 725, "127.0.0.1"))
	assert.Equal(t, 133, getUserQuotaForPaymentGuardTest(t, 501))
	topUp := GetTopUpByTradeNo("stripe-exact-quota")
	require.NotNil(t, topUp)
	assert.Equal(t, 123, topUp.Quota)
	assert.Equal(t, "pi_1", topUp.ProviderPaymentId)
	require.NotNil(t, topUp.ProviderPaidAmountMinor)
	assert.Equal(t, int64(725), *topUp.ProviderPaidAmountMinor)

	require.Error(t, Recharge("stripe-exact-quota", "cus_1", "pi_other", 725, "127.0.0.1"))
	require.Error(t, Recharge("stripe-exact-quota", "cus_1", "pi_1", 724, "127.0.0.1"))
	assert.Equal(t, 133, getUserQuotaForPaymentGuardTest(t, 501))
}

func TestStripePromotionRefundUsesAmountActuallyPaid(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 515, 100)
	require.NoError(t, (&TopUp{
		UserId: 515, Amount: 1, Quota: 1000, Money: 10,
		TradeNo: "stripe-promoted-refund", PaymentMethod: PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusPending,
	}).Insert())

	require.Error(t, Recharge("stripe-promoted-refund", "cus_promo", "pi_promo", 1001, "127.0.0.1"))
	assert.Equal(t, 100, getUserQuotaForPaymentGuardTest(t, 515))
	require.NoError(t, Recharge("stripe-promoted-refund", "cus_promo", "pi_promo", 500, "127.0.0.1"))
	topUp := GetTopUpByTradeNo("stripe-promoted-refund")
	require.NotNil(t, topUp)
	assert.Equal(t, 10.0, topUp.Money)
	require.NotNil(t, topUp.ProviderPaidAmountMinor)
	assert.Equal(t, int64(500), *topUp.ProviderPaidAmountMinor)
	assert.Equal(t, 1100, getUserQuotaForPaymentGuardTest(t, 515))

	require.NoError(t, ReverseTopUpByProviderPayment(PaymentProviderStripe, "pi_promo", 2.5, false))
	assert.Equal(t, 600, getUserQuotaForPaymentGuardTest(t, 515))
}

func TestStripeZeroPaidPromotionHasExactReplayIdentity(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 516, 100)
	require.NoError(t, (&TopUp{
		UserId: 516, Amount: 1, Quota: 1000, Money: 10,
		TradeNo: "stripe-zero-promotion", PaymentMethod: PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusPending,
	}).Insert())

	require.NoError(t, Recharge("stripe-zero-promotion", "cus_zero", "", 0, "127.0.0.1"))
	require.NoError(t, Recharge("stripe-zero-promotion", "cus_zero", "", 0, "127.0.0.1"))
	require.Error(t, Recharge("stripe-zero-promotion", "cus_zero", "", 1, "127.0.0.1"))
	require.Error(t, Recharge("stripe-zero-promotion", "cus_zero", "pi_after_zero", 0, "127.0.0.1"))
	assert.Equal(t, 1100, getUserQuotaForPaymentGuardTest(t, 516))

	topUp := GetTopUpByTradeNo("stripe-zero-promotion")
	require.NotNil(t, topUp)
	require.NotNil(t, topUp.ProviderPaidAmountMinor)
	assert.Zero(t, *topUp.ProviderPaidAmountMinor)
	assert.Equal(t, 10.0, topUp.Money)
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

func TestCreemRechargeUsesActualPaidAmountForReplayAndRefunds(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 517, 100)
	require.NoError(t, (&TopUp{
		UserId: 517, Amount: 1000, Quota: 1000, Money: 10,
		TradeNo: "creem-paid-amount", PaymentMethod: PaymentMethodCreem,
		PaymentProvider: PaymentProviderCreem, Status: common.TopUpStatusPending,
	}).Insert())

	require.NoError(t, RechargeCreem("creem-paid-amount", "tx_creem", 1200, "", "", "127.0.0.1"))
	require.NoError(t, RechargeCreem("creem-paid-amount", "tx_creem", 1200, "", "", "127.0.0.1"))
	require.Error(t, RechargeCreem("creem-paid-amount", "tx_other", 1200, "", "", "127.0.0.1"))
	require.Error(t, RechargeCreem("creem-paid-amount", "tx_creem", 1100, "", "", "127.0.0.1"))

	topUp := GetTopUpByTradeNo("creem-paid-amount")
	require.NotNil(t, topUp)
	require.NotNil(t, topUp.ProviderPaidAmountMinor)
	assert.Equal(t, int64(1200), *topUp.ProviderPaidAmountMinor)
	assert.Equal(t, 10.0, topUp.Money)
	assert.Equal(t, 1100, getUserQuotaForPaymentGuardTest(t, 517))

	require.NoError(t, ReverseTopUpByProviderPayment(PaymentProviderCreem, "tx_creem", 6, false))
	assert.Equal(t, 600, getUserQuotaForPaymentGuardTest(t, 517))
}

func TestWaffoRechargeBindsProviderOrderAndRejectsMismatchedReplay(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 514, 10)
	require.NoError(t, (&TopUp{
		UserId: 514, Amount: 1, Quota: 123, Money: 7.25,
		TradeNo: "waffo-provider-order", PaymentMethod: PaymentMethodWaffo,
		PaymentProvider: PaymentProviderWaffo, Status: common.TopUpStatusPending,
	}).Insert())

	require.NoError(t, RechargeWaffo("waffo-provider-order", "acquiring-1", "127.0.0.1"))
	require.NoError(t, RechargeWaffo("waffo-provider-order", "acquiring-1", "127.0.0.1"))
	require.Error(t, RechargeWaffo("waffo-provider-order", "acquiring-other", "127.0.0.1"))
	assert.Equal(t, 133, getUserQuotaForPaymentGuardTest(t, 514))
	topUp := GetTopUpByTradeNo("waffo-provider-order")
	require.NotNil(t, topUp)
	assert.Equal(t, "acquiring-1", topUp.ProviderPaymentId)
	assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
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

func TestProviderRefundEventsAreCumulativeAndIdempotent(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 511, 1100)
	require.NoError(t, (&TopUp{
		UserId: 511, Amount: 1, Quota: 1000, Money: 10,
		TradeNo:       "pancake-refund-events",
		PaymentMethod: PaymentMethodWaffoPancake, PaymentProvider: PaymentProviderWaffoPancake,
		Status: common.TopUpStatusSuccess,
	}).Insert())

	require.NoError(t, ReverseTopUpByTradeNoEvent(
		PaymentProviderWaffoPancake, "pancake-refund-events", "refund-1", 250,
	))
	require.NoError(t, ReverseTopUpByTradeNoEvent(
		PaymentProviderWaffoPancake, "pancake-refund-events", "refund-1", 250,
	))
	require.Error(t, ReverseTopUpByTradeNoEvent(
		PaymentProviderWaffoPancake, "pancake-refund-events", "refund-1", 251,
	))
	assert.Equal(t, 850, getUserQuotaForPaymentGuardTest(t, 511))
	topUp := GetTopUpByTradeNo("pancake-refund-events")
	require.NotNil(t, topUp)
	assert.Equal(t, TopUpStatusPartiallyRefunded, topUp.Status)
	assert.Equal(t, 250, topUp.RefundedQuota)

	require.NoError(t, ReverseTopUpByTradeNoEvent(
		PaymentProviderWaffoPancake, "pancake-refund-events", "refund-2", 750,
	))
	require.NoError(t, ReverseTopUpByTradeNoEvent(
		PaymentProviderWaffoPancake, "pancake-refund-events", "refund-2", 750,
	))
	assert.Equal(t, 100, getUserQuotaForPaymentGuardTest(t, 511))
	topUp = GetTopUpByTradeNo("pancake-refund-events")
	require.NotNil(t, topUp)
	assert.Equal(t, TopUpStatusRefunded, topUp.Status)
	assert.Equal(t, 1000, topUp.RefundedQuota)
	assert.NotEmpty(t, topUp.ProviderRefundEvents)
}

func TestTopUpReversalRejectsBalanceUnderflowAndUnsafeRefundAmount(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 512, common.MinQuota)
	require.NoError(t, (&TopUp{
		UserId: 512, Amount: 1, Quota: 100, Money: 10,
		TradeNo:       "refund-balance-underflow",
		PaymentMethod: PaymentMethodStripe, PaymentProvider: PaymentProviderStripe,
		Status: common.TopUpStatusSuccess,
	}).Insert())

	require.Error(t, ReverseTopUpByTradeNo(
		PaymentProviderStripe, "refund-balance-underflow", 0, true,
	))
	require.Error(t, ReverseTopUpByTradeNoEvent(
		PaymentProviderStripe, "refund-balance-underflow", "refund-underflow", 1000,
	))
	assert.Equal(t, common.MinQuota, getUserQuotaForPaymentGuardTest(t, 512))
	topUp := GetTopUpByTradeNo("refund-balance-underflow")
	require.NotNil(t, topUp)
	assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
	assert.Zero(t, topUp.RefundedQuota)

	require.Error(t, ReverseTopUpByTradeNo(
		PaymentProviderStripe, "refund-balance-underflow", math.MaxFloat64, false,
	))
	require.Error(t, ReverseTopUpByTradeNo(
		PaymentProviderStripe, "refund-balance-underflow", math.NaN(), false,
	))
	require.Error(t, ReverseTopUpByTradeNo(
		PaymentProviderStripe, "refund-balance-underflow", 10.01, false,
	))
	assert.Equal(t, common.MinQuota, getUserQuotaForPaymentGuardTest(t, 512))
	topUp = GetTopUpByTradeNo("refund-balance-underflow")
	require.NotNil(t, topUp)
	assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
	assert.Zero(t, topUp.RefundedQuota)
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

func TestOverlappingDifferentUpgradeGroupsRestoreSurvivingThenOriginalGroup(t *testing.T) {
	truncateTables(t)
	user := &User{Id: 507, Username: "tier-overlap", Group: "base", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(user).Error)

	vipPlan := insertSubscriptionPlanForPaymentGuardTest(t, 507)
	vipPlan.UpgradeGroup = "vip"
	require.NoError(t, DB.Save(vipPlan).Error)
	InvalidateSubscriptionPlanCache(vipPlan.Id)
	proPlan := insertSubscriptionPlanForPaymentGuardTest(t, 508)
	proPlan.UpgradeGroup = "pro"
	require.NoError(t, DB.Save(proPlan).Error)
	InvalidateSubscriptionPlanCache(proPlan.Id)

	insertSubscriptionOrderForPaymentGuardTest(t, "tier-overlap-vip", user.Id, vipPlan.Id, PaymentProviderStripe)
	require.NoError(t, CompleteSubscriptionOrder("tier-overlap-vip", "", PaymentProviderStripe, ""))
	insertSubscriptionOrderForPaymentGuardTest(t, "tier-overlap-pro", user.Id, proPlan.Id, PaymentProviderStripe)
	require.NoError(t, CompleteSubscriptionOrder("tier-overlap-pro", "", PaymentProviderStripe, ""))

	var subs []UserSubscription
	require.NoError(t, DB.Where("user_id = ?", user.Id).Order("id asc").Find(&subs).Error)
	require.Len(t, subs, 2)
	assert.Equal(t, "base", subs[0].PrevUserGroup)
	assert.Equal(t, "base", subs[1].PrevUserGroup)

	_, err := AdminInvalidateUserSubscription(subs[1].Id)
	require.NoError(t, err)
	group, err := GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "vip", group)

	_, err = AdminInvalidateUserSubscription(subs[0].Id)
	require.NoError(t, err)
	group, err = GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "base", group)
}

func TestOverlappingUpgradePreservesManualGroupProvenance(t *testing.T) {
	truncateTables(t)
	user := &User{Id: 518, Username: "manual-tier-overlap", Group: "base", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(user).Error)

	vipPlan := insertSubscriptionPlanForPaymentGuardTest(t, 518)
	vipPlan.UpgradeGroup = "vip"
	require.NoError(t, DB.Save(vipPlan).Error)
	InvalidateSubscriptionPlanCache(vipPlan.Id)
	proPlan := insertSubscriptionPlanForPaymentGuardTest(t, 519)
	proPlan.UpgradeGroup = "pro"
	require.NoError(t, DB.Save(proPlan).Error)
	InvalidateSubscriptionPlanCache(proPlan.Id)

	insertSubscriptionOrderForPaymentGuardTest(t, "manual-tier-vip", user.Id, vipPlan.Id, PaymentProviderStripe)
	require.NoError(t, CompleteSubscriptionOrder("manual-tier-vip", "", PaymentProviderStripe, ""))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("group", "staff").Error)
	insertSubscriptionOrderForPaymentGuardTest(t, "manual-tier-pro", user.Id, proPlan.Id, PaymentProviderStripe)
	require.NoError(t, CompleteSubscriptionOrder("manual-tier-pro", "", PaymentProviderStripe, ""))

	var subscriptions []UserSubscription
	require.NoError(t, DB.Where("user_id = ?", user.Id).Order("id asc").Find(&subscriptions).Error)
	require.Len(t, subscriptions, 2)
	assert.Equal(t, "base", subscriptions[0].PrevUserGroup)
	assert.Equal(t, "staff", subscriptions[1].PrevUserGroup)

	_, err := AdminInvalidateUserSubscription(subscriptions[1].Id)
	require.NoError(t, err)
	group, err := GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "staff", group)

	_, err = AdminInvalidateUserSubscription(subscriptions[0].Id)
	require.NoError(t, err)
	group, err = GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "staff", group)

}

func TestScheduledExpiryRestoresSurvivingUpgradeGroup(t *testing.T) {
	truncateTables(t)
	user := &User{Id: 509, Username: "scheduled-tier-overlap", Group: "base", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(user).Error)

	vipPlan := insertSubscriptionPlanForPaymentGuardTest(t, 509)
	vipPlan.UpgradeGroup = "vip"
	require.NoError(t, DB.Save(vipPlan).Error)
	InvalidateSubscriptionPlanCache(vipPlan.Id)
	proPlan := insertSubscriptionPlanForPaymentGuardTest(t, 510)
	proPlan.UpgradeGroup = "pro"
	require.NoError(t, DB.Save(proPlan).Error)
	InvalidateSubscriptionPlanCache(proPlan.Id)

	insertSubscriptionOrderForPaymentGuardTest(t, "scheduled-tier-vip", user.Id, vipPlan.Id, PaymentProviderStripe)
	require.NoError(t, CompleteSubscriptionOrder("scheduled-tier-vip", "", PaymentProviderStripe, ""))
	insertSubscriptionOrderForPaymentGuardTest(t, "scheduled-tier-pro", user.Id, proPlan.Id, PaymentProviderStripe)
	require.NoError(t, CompleteSubscriptionOrder("scheduled-tier-pro", "", PaymentProviderStripe, ""))

	var subs []UserSubscription
	require.NoError(t, DB.Where("user_id = ?", user.Id).Order("id asc").Find(&subs).Error)
	require.Len(t, subs, 2)
	now := GetDBTimestamp()
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", subs[0].Id).Update("end_time", now+3600).Error)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", subs[1].Id).Update("end_time", now-1).Error)

	expired, err := ExpireDueSubscriptions(10)
	require.NoError(t, err)
	assert.Equal(t, 1, expired)
	group, err := GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "vip", group)

	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", subs[0].Id).Update("end_time", now-1).Error)
	expired, err = ExpireDueSubscriptions(10)
	require.NoError(t, err)
	assert.Equal(t, 1, expired)
	group, err = GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "base", group)
}

func TestScheduledExpiryUsesCurrentGroupOwnerWhenMultiplePlansAreOverdue(t *testing.T) {
	truncateTables(t)
	user := &User{
		Id:       515,
		Username: "simultaneous-tier-expiry",
		Group:    "base",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)
	vipPlan := insertSubscriptionPlanForPaymentGuardTest(t, 515)
	vipPlan.UpgradeGroup = "vip"
	require.NoError(t, DB.Save(vipPlan).Error)
	InvalidateSubscriptionPlanCache(vipPlan.Id)
	proPlan := insertSubscriptionPlanForPaymentGuardTest(t, 516)
	proPlan.UpgradeGroup = "pro"
	require.NoError(t, DB.Save(proPlan).Error)
	InvalidateSubscriptionPlanCache(proPlan.Id)

	insertSubscriptionOrderForPaymentGuardTest(t, "simultaneous-tier-vip", user.Id, vipPlan.Id, PaymentProviderStripe)
	require.NoError(t, CompleteSubscriptionOrder("simultaneous-tier-vip", "", PaymentProviderStripe, ""))
	insertSubscriptionOrderForPaymentGuardTest(t, "simultaneous-tier-pro", user.Id, proPlan.Id, PaymentProviderStripe)
	require.NoError(t, CompleteSubscriptionOrder("simultaneous-tier-pro", "", PaymentProviderStripe, ""))

	var subscriptions []UserSubscription
	require.NoError(t, DB.Where("user_id = ?", user.Id).Order("id asc").Find(&subscriptions).Error)
	require.Len(t, subscriptions, 2)
	now := GetDBTimestamp()
	// The older VIP plan has the later end time, while the newer PRO plan owns
	// the current group. Both become overdue in the same scheduler batch.
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("id = ?", subscriptions[0].Id).
		Update("end_time", now-1).Error)
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("id = ?", subscriptions[1].Id).
		Update("end_time", now-2).Error)

	expired, err := ExpireDueSubscriptions(10)
	require.NoError(t, err)
	assert.Equal(t, 2, expired)
	group, err := GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "base", group)
}

func TestOneTimeSubscriptionRefundCancelsEntitlementExactlyOnce(t *testing.T) {
	truncateTables(t)
	user := &User{
		Id:       513,
		Username: "one-time-subscription-refund",
		Group:    "base",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 513)
	plan.UpgradeGroup = "vip"
	require.NoError(t, DB.Save(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	insertSubscriptionOrderForPaymentGuardTest(
		t, "pancake-subscription-refund", user.Id, plan.Id, PaymentProviderWaffoPancake,
	)
	require.NoError(t, CompleteSubscriptionOrder(
		"pancake-subscription-refund", "", PaymentProviderWaffoPancake, "",
	))
	group, err := GetUserGroup(user.Id, true)
	require.NoError(t, err)
	require.Equal(t, "vip", group)

	for range 2 {
		require.NoError(t, CancelSubscriptionOrderByTradeNo(
			PaymentProviderWaffoPancake,
			"pancake-subscription-refund",
			"refunded",
			`{"id":"refund-1"}`,
		))
	}

	group, err = GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "base", group)
	order := GetSubscriptionOrderByTradeNo("pancake-subscription-refund")
	require.NotNil(t, order)
	assert.Equal(t, TopUpStatusRefunded, order.Status)
	assert.Equal(t, "refunded", order.ProviderStatus)
	topUp := GetTopUpByTradeNo("pancake-subscription-refund")
	require.NotNil(t, topUp)
	assert.Equal(t, TopUpStatusRefunded, topUp.Status)
	var subscription UserSubscription
	require.NoError(t, DB.Where("subscription_order_id = ?", order.Id).First(&subscription).Error)
	assert.Equal(t, "cancelled", subscription.Status)
	assert.LessOrEqual(t, subscription.EndTime, GetDBTimestamp())
}

func TestProviderSubscriptionRefundsPreservePartialAndRevokeOnCumulativeFullAmount(t *testing.T) {
	testCases := []struct {
		name       string
		provider   string
		mode       ProviderRefundAmountMode
		recurring  bool
		firstMinor int64
		finalMinor int64
	}{
		{
			name:       "Waffo Pancake incremental events",
			provider:   PaymentProviderWaffoPancake,
			mode:       ProviderRefundAmountIncremental,
			firstMinor: 250,
			finalMinor: 749,
		},
		{
			name:       "Stripe cumulative amount",
			provider:   PaymentProviderStripe,
			mode:       ProviderRefundAmountCumulative,
			recurring:  true,
			firstMinor: 250,
			finalMinor: 999,
		},
		{
			name:       "Creem incremental fallback events",
			provider:   PaymentProviderCreem,
			mode:       ProviderRefundAmountIncremental,
			recurring:  true,
			firstMinor: 400,
			finalMinor: 599,
		},
	}

	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			truncateTables(t)
			userID := 520 + index
			user := &User{
				Id:       userID,
				Username: "subscription-refund",
				Group:    "base",
				Status:   common.UserStatusEnabled,
			}
			require.NoError(t, DB.Create(user).Error)
			plan := insertSubscriptionPlanForPaymentGuardTest(t, 520+index)
			plan.UpgradeGroup = "vip"
			require.NoError(t, DB.Save(plan).Error)
			InvalidateSubscriptionPlanCache(plan.Id)
			tradeNo := fmt.Sprintf("subscription-refund-%d", index)
			paymentID := fmt.Sprintf("payment-refund-%d", index)
			subscriptionID := fmt.Sprintf("provider-subscription-%d", index)
			insertSubscriptionOrderForPaymentGuardTest(t, tradeNo, userID, plan.Id, testCase.provider)
			if testCase.recurring {
				require.NoError(t, SetSubscriptionOrderProviderIdentifiersAt(
					tradeNo,
					testCase.provider,
					"",
					subscriptionID,
					paymentID,
					"active",
					100,
					"",
				))
			}
			require.NoError(t, CompleteSubscriptionOrder(tradeNo, "", testCase.provider, ""))

			applyRefund := func(refund SubscriptionRefundInput) error {
				if testCase.recurring {
					return ApplySubscriptionRefundByProviderPayment(testCase.provider, paymentID, refund)
				}
				return ApplySubscriptionRefundByTradeNo(testCase.provider, tradeNo, refund)
			}
			firstRefund := SubscriptionRefundInput{
				EventID:           "refund-1",
				AmountMinor:       testCase.firstMinor,
				AmountMode:        testCase.mode,
				ProviderEventTime: 200,
				ProviderPayload:   `{"event":"refund-1"}`,
			}
			require.NoError(t, applyRefund(firstRefund))
			require.NoError(t, applyRefund(firstRefund))
			changedReplay := firstRefund
			changedReplay.AmountMinor++
			require.Error(t, applyRefund(changedReplay))

			var subscription UserSubscription
			require.NoError(t, DB.Where("user_id = ?", userID).First(&subscription).Error)
			assert.Equal(t, "active", subscription.Status)
			order := GetSubscriptionOrderByTradeNo(tradeNo)
			require.NotNil(t, order)
			assert.Equal(t, testCase.firstMinor, order.ProviderRefundedAmountMinor)
			assert.Equal(t, TopUpStatusPartiallyRefunded, order.ProviderStatus)

			require.NoError(t, applyRefund(SubscriptionRefundInput{
				EventID:           "refund-2",
				AmountMinor:       testCase.finalMinor,
				AmountMode:        testCase.mode,
				ProviderEventTime: 300,
				ProviderPayload:   `{"event":"refund-2"}`,
			}))
			require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
			assert.Equal(t, "cancelled", subscription.Status)
			order = GetSubscriptionOrderByTradeNo(tradeNo)
			require.NotNil(t, order)
			assert.Equal(t, int64(999), order.ProviderRefundedAmountMinor)
			assert.NotEmpty(t, order.ProviderRefundEvents)
			group, err := GetUserGroup(userID, true)
			require.NoError(t, err)
			assert.Equal(t, "base", group)
		})
	}
}

func TestSubscriptionRefundUsesProviderPaidAmountForFullThreshold(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 525, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 525)
	insertSubscriptionOrderForPaymentGuardTest(t, "provider-paid-refund", 525, plan.Id, PaymentProviderStripe)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiers(
		"provider-paid-refund",
		PaymentProviderStripe,
		"",
		"sub_provider_paid",
		"pi_provider_paid",
		"active",
		"",
	))
	require.NoError(t, CompleteSubscriptionOrder("provider-paid-refund", "", PaymentProviderStripe, ""))

	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"pi_provider_paid",
		SubscriptionRefundInput{
			EventID:         "provider-paid-partial",
			AmountMinor:     999,
			PaidAmountMinor: 1200,
			AmountMode:      ProviderRefundAmountCumulative,
		},
	))
	var subscription UserSubscription
	require.NoError(t, DB.Where("user_id = ?", 525).First(&subscription).Error)
	assert.Equal(t, "active", subscription.Status)
	order := GetSubscriptionOrderByTradeNo("provider-paid-refund")
	require.NotNil(t, order)
	assert.Equal(t, int64(1200), order.ProviderPaidAmountMinor)
	assert.Equal(t, int64(999), order.ProviderRefundedAmountMinor)

	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"pi_provider_paid",
		SubscriptionRefundInput{
			EventID:         "provider-paid-full",
			AmountMinor:     1200,
			PaidAmountMinor: 1200,
			AmountMode:      ProviderRefundAmountCumulative,
		},
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, "cancelled", subscription.Status)
}

func TestSubscriptionRefundLedgerAllowsTerminalRefundAfterPartialEventLimit(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 526, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 526)
	insertSubscriptionOrderForPaymentGuardTest(t, "refund-ledger-limit", 526, plan.Id, PaymentProviderStripe)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiersAt(
		"refund-ledger-limit",
		PaymentProviderStripe,
		"",
		"sub_refund_ledger_limit",
		"pi_refund_ledger_limit",
		"active",
		100,
		"",
	))
	require.NoError(t, CompleteSubscriptionOrder("refund-ledger-limit", "", PaymentProviderStripe, ""))

	for i := range 64 {
		require.NoError(t, ApplySubscriptionRefundByProviderPayment(
			PaymentProviderStripe,
			"pi_refund_ledger_limit",
			SubscriptionRefundInput{
				EventID:           fmt.Sprintf("partial-refund-%02d", i),
				AmountMinor:       10,
				AmountMode:        ProviderRefundAmountIncremental,
				ProviderEventTime: int64(200 + i),
			},
		))
	}
	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"pi_refund_ledger_limit",
		SubscriptionRefundInput{
			EventID:           "terminal-refund",
			AmountMinor:       999,
			AmountMode:        ProviderRefundAmountCumulative,
			ProviderEventTime: 300,
		},
	))
	// Replays and delayed partial snapshots after terminal settlement are safe
	// no-ops and must not make a provider retry forever.
	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"pi_refund_ledger_limit",
		SubscriptionRefundInput{
			EventID:           "terminal-refund",
			AmountMinor:       999,
			AmountMode:        ProviderRefundAmountCumulative,
			ProviderEventTime: 300,
		},
	))
	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"pi_refund_ledger_limit",
		SubscriptionRefundInput{
			EventID:           "delayed-partial-refund",
			AmountMinor:       10,
			AmountMode:        ProviderRefundAmountIncremental,
			ProviderEventTime: 250,
		},
	))

	order := GetSubscriptionOrderByTradeNo("refund-ledger-limit")
	require.NotNil(t, order)
	assert.Equal(t, int64(999), order.ProviderRefundedAmountMinor)
	var events map[string]subscriptionRefundEvent
	require.NoError(t, common.UnmarshalJsonStr(order.ProviderRefundEvents, &events))
	require.Len(t, events, 1)
	assert.Contains(t, events, "terminal-refund")
	var subscription UserSubscription
	require.NoError(t, DB.Where("user_id = ?", 526).First(&subscription).Error)
	assert.Equal(t, "cancelled", subscription.Status)
}

func TestRefundingOlderRecurringPaymentPreservesNewerPaidPeriod(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 527, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 527)
	insertSubscriptionOrderForPaymentGuardTest(t, "older-refund-initial", 527, plan.Id, PaymentProviderStripe)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiersAt(
		"older-refund-initial",
		PaymentProviderStripe,
		"",
		"sub_older_refund",
		"pi_older_initial",
		"active",
		100,
		"",
	))
	require.NoError(t, CompleteSubscriptionOrder("older-refund-initial", "", PaymentProviderStripe, ""))
	require.NoError(t, RenewProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_older_refund",
		"older_refund_renewal_1",
		"pi_older_renewal_1",
		9.99,
		200,
		"",
	))
	require.NoError(t, RenewProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_older_refund",
		"older_refund_renewal_2",
		"pi_older_renewal_2",
		9.99,
		300,
		"",
	))

	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"pi_older_initial",
		SubscriptionRefundInput{
			EventID:           "refund-old-period",
			AmountMinor:       999,
			AmountMode:        ProviderRefundAmountCumulative,
			Full:              true,
			ProviderEventTime: 400,
		},
	))
	var subscription UserSubscription
	require.NoError(t, DB.Where("user_id = ?", 527).First(&subscription).Error)
	assert.Equal(t, "active", subscription.Status)
	initialOrder := GetSubscriptionOrderByTradeNo("older-refund-initial")
	require.NotNil(t, initialOrder)
	assert.Equal(t, TopUpStatusRefunded, initialOrder.ProviderStatus)

	require.NoError(t, ApplySubscriptionRefundByProviderInvoice(
		PaymentProviderStripe,
		"older_refund_renewal_2",
		SubscriptionRefundInput{
			EventID:           "refund-current-period",
			AmountMinor:       999,
			AmountMode:        ProviderRefundAmountCumulative,
			Full:              true,
			ProviderEventTime: 500,
		},
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, "cancelled", subscription.Status)
}

func TestSubscriptionRefundAggregatesScopedCumulativeProviderPayments(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 528, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 528)
	insertSubscriptionOrderForPaymentGuardTest(t, "multi-payment-refund", 528, plan.Id, PaymentProviderStripe)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiersAt(
		"multi-payment-refund",
		PaymentProviderStripe,
		"",
		"sub_multi_payment",
		"pi_default_payment",
		"active",
		100,
		"",
	))
	require.NoError(t, CompleteSubscriptionOrder("multi-payment-refund", "", PaymentProviderStripe, ""))
	require.NoError(t, BindSubscriptionProviderPayments(
		PaymentProviderStripe,
		"sub_multi_payment",
		"",
		[]string{"ch_partial_a", "ch_partial_b"},
	))

	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"ch_partial_a",
		SubscriptionRefundInput{
			EventID:     "refund-a-1",
			AmountMinor: 400,
			AmountMode:  ProviderRefundAmountCumulative,
			AmountScope: "ch_partial_a",
		},
	))
	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"ch_partial_a",
		SubscriptionRefundInput{
			EventID:     "refund-a-replay-snapshot",
			AmountMinor: 400,
			AmountMode:  ProviderRefundAmountCumulative,
			AmountScope: "ch_partial_a",
		},
	))
	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"ch_partial_a",
		SubscriptionRefundInput{
			EventID:     "refund-a-2",
			AmountMinor: 500,
			AmountMode:  ProviderRefundAmountCumulative,
			AmountScope: "ch_partial_a",
		},
	))
	order := GetSubscriptionOrderByTradeNo("multi-payment-refund")
	require.NotNil(t, order)
	assert.Equal(t, int64(500), order.ProviderRefundedAmountMinor)

	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"ch_partial_b",
		SubscriptionRefundInput{
			EventID:     "refund-b",
			AmountMinor: 499,
			AmountMode:  ProviderRefundAmountCumulative,
			AmountScope: "ch_partial_b",
		},
	))
	order = GetSubscriptionOrderByTradeNo("multi-payment-refund")
	require.NotNil(t, order)
	assert.Equal(t, int64(999), order.ProviderRefundedAmountMinor)
	var subscription UserSubscription
	require.NoError(t, DB.Where("user_id = ?", 528).First(&subscription).Error)
	assert.Equal(t, "cancelled", subscription.Status)
}

func TestRenewalAndProviderPaymentReferencesCommitAtomically(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 530, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 530)
	insertSubscriptionOrderForPaymentGuardTest(t, "atomic-renewal-initial", 530, plan.Id, PaymentProviderStripe)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiersAt(
		"atomic-renewal-initial",
		PaymentProviderStripe,
		"",
		"sub_atomic_renewal",
		"pi_atomic_initial",
		"active",
		100,
		"",
	))
	require.NoError(t, CompleteSubscriptionOrder("atomic-renewal-initial", "", PaymentProviderStripe, ""))
	require.NoError(t, BindSubscriptionProviderPayments(
		PaymentProviderStripe,
		"sub_atomic_renewal",
		"",
		[]string{"pi_atomic_initial"},
	))
	var subscription UserSubscription
	require.NoError(t, DB.Where("user_id = ?", 530).First(&subscription).Error)
	initialEndTime := subscription.EndTime

	err := RenewProviderSubscriptionWithPaymentsAt(
		PaymentProviderStripe,
		"sub_atomic_renewal",
		"atomic_collision",
		"pi_atomic_renewal",
		[]string{"pi_atomic_renewal", "pi_atomic_initial"},
		9.99,
		200,
		"",
	)
	require.Error(t, err)
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, initialEndTime, subscription.EndTime)
	var renewalCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).
		Where("trade_no = ?", "stripe_invoice_atomic_collision").
		Count(&renewalCount).Error)
	assert.Zero(t, renewalCount)
	var rolledBackReferenceCount int64
	require.NoError(t, DB.Model(&SubscriptionProviderPayment{}).
		Where("payment_provider = ? AND provider_payment_id = ?", PaymentProviderStripe, "pi_atomic_renewal").
		Count(&rolledBackReferenceCount).Error)
	assert.Zero(t, rolledBackReferenceCount)

	require.NoError(t, RenewProviderSubscriptionWithPaymentsAt(
		PaymentProviderStripe,
		"sub_atomic_renewal",
		"atomic_success",
		"pi_atomic_renewal",
		[]string{"pi_atomic_renewal", "ch_atomic_renewal"},
		9.99,
		300,
		"",
	))
	var renewal SubscriptionOrder
	require.NoError(t, DB.Where("trade_no = ?", "stripe_invoice_atomic_success").First(&renewal).Error)
	var references []SubscriptionProviderPayment
	require.NoError(t, DB.Where(
		"payment_provider = ? AND provider_payment_id IN ?",
		PaymentProviderStripe,
		[]string{"pi_atomic_renewal", "ch_atomic_renewal"},
	).Order("provider_payment_id asc").Find(&references).Error)
	require.Len(t, references, 2)
	assert.Equal(t, renewal.Id, references[0].SubscriptionOrderId)
	assert.Equal(t, renewal.Id, references[1].SubscriptionOrderId)

	// A retry binds the same references to the already-created invoice without
	// extending the entitlement again.
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	renewedEndTime := subscription.EndTime
	require.NoError(t, RenewProviderSubscriptionWithPaymentsAt(
		PaymentProviderStripe,
		"sub_atomic_renewal",
		"atomic_success",
		"pi_atomic_renewal",
		[]string{"pi_atomic_renewal", "ch_atomic_renewal"},
		9.99,
		300,
		"",
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, renewedEndTime, subscription.EndTime)
}

func TestSubscriptionDisputeRevokesIndivisibleEntitlementForPartialAmount(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 529, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 529)
	insertSubscriptionOrderForPaymentGuardTest(t, "partial-dispute", 529, plan.Id, PaymentProviderStripe)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiersAt(
		"partial-dispute",
		PaymentProviderStripe,
		"",
		"sub_partial_dispute",
		"pi_partial_dispute",
		"active",
		100,
		"",
	))
	require.NoError(t, CompleteSubscriptionOrder("partial-dispute", "", PaymentProviderStripe, ""))

	require.NoError(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"pi_partial_dispute",
		SubscriptionRefundInput{
			EventID:     "partial-dispute-event",
			AmountMinor: 100,
			AmountMode:  ProviderRefundAmountCumulative,
			AmountScope: "pi_partial_dispute",
			Full:        true,
		},
	))
	var subscription UserSubscription
	require.NoError(t, DB.Where("user_id = ?", 529).First(&subscription).Error)
	assert.Equal(t, "cancelled", subscription.Status)
}

func TestProviderSubscriptionRejectsInvalidRenewalAmountsAndEventTimes(t *testing.T) {
	for name, money := range map[string]float64{
		"zero":              0,
		"negative":          -1,
		"not a number":      math.NaN(),
		"positive infinity": math.Inf(1),
		"overflow":          math.MaxFloat64,
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, RenewProviderSubscriptionAt(
				PaymentProviderStripe,
				"sub_invalid_amount",
				"invoice_invalid_amount",
				"pi_invalid_amount",
				money,
				1,
				"",
			))
		})
	}

	require.Error(t, RenewProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_negative_event_time",
		"invoice_negative_event_time",
		"pi_negative_event_time",
		9.99,
		-1,
		"",
	))
	require.Error(t, SetSubscriptionOrderProviderIdentifiersAt(
		"negative-event-time",
		PaymentProviderStripe,
		"",
		"sub_negative_event_time",
		"",
		"active",
		-1,
		"",
	))
	require.Error(t, UpdateProviderSubscriptionStatusAt(
		PaymentProviderStripe, "sub_negative_event_time", "active", -1, "",
	))
	require.Error(t, SetProviderSubscriptionPaymentAt(
		PaymentProviderStripe, "sub_negative_event_time", "", "active", -1, "",
	))
	require.Error(t, CancelProviderSubscriptionAt(
		PaymentProviderStripe, "sub_negative_event_time", "canceled", -1, "",
	))
	require.Error(t, CancelProviderSubscriptionByPaymentAt(
		PaymentProviderStripe, "pi_negative_event_time", "canceled", -1, "",
	))
	require.Error(t, ApplySubscriptionRefundByProviderPayment(
		PaymentProviderStripe,
		"pi_negative_event_time",
		SubscriptionRefundInput{
			EventID:           "negative-event-time",
			AmountMinor:       1,
			AmountMode:        ProviderRefundAmountCumulative,
			ProviderEventTime: -1,
		},
	))
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

func TestProviderSubscriptionLifecycleRejectsStaleRenewalAndCancellation(t *testing.T) {
	truncateTables(t)
	user := &User{
		Id:       523,
		Username: "provider-lifecycle-ordering",
		Group:    "base",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 523)
	plan.UpgradeGroup = "vip"
	require.NoError(t, DB.Save(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	insertSubscriptionOrderForPaymentGuardTest(t, "lifecycle-initial", user.Id, plan.Id, PaymentProviderStripe)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiersAt(
		"lifecycle-initial",
		PaymentProviderStripe,
		"",
		"sub_ordered",
		"pi_initial_ordered",
		"active",
		100,
		"",
	))
	require.NoError(t, CompleteSubscriptionOrder("lifecycle-initial", "", PaymentProviderStripe, ""))
	require.NoError(t, CancelProviderSubscriptionAt(
		PaymentProviderStripe, "sub_ordered", "canceled", 300, `{"event":"cancel"}`,
	))

	var subscription UserSubscription
	require.NoError(t, DB.Where("user_id = ?", user.Id).First(&subscription).Error)
	require.Equal(t, "cancelled", subscription.Status)
	cancelledEnd := subscription.EndTime
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("group", "staff").Error)

	require.NoError(t, RenewProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_ordered",
		"invoice_old",
		"pi_old",
		9.99,
		200,
		`{"event":"old-renewal"}`,
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, "cancelled", subscription.Status)
	assert.Equal(t, cancelledEnd, subscription.EndTime)
	var oldRenewalCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).
		Where("trade_no = ?", "stripe_invoice_invoice_old").
		Count(&oldRenewalCount).Error)
	assert.Zero(t, oldRenewalCount)

	require.NoError(t, RenewProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_ordered",
		"invoice_new",
		"pi_new",
		9.99,
		400,
		`{"event":"new-renewal"}`,
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	require.Equal(t, "active", subscription.Status)
	assert.Equal(t, int64(400), subscription.ProviderEventTime)
	assert.Equal(t, "staff", subscription.PrevUserGroup)

	require.NoError(t, CancelProviderSubscriptionAt(
		PaymentProviderStripe, "sub_ordered", "canceled", 350, `{"event":"stale-cancel"}`,
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, "active", subscription.Status)
	assert.Equal(t, int64(400), subscription.ProviderEventTime)
	group, err := GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "vip", group)

	require.NoError(t, CancelProviderSubscriptionAt(
		PaymentProviderStripe, "sub_ordered", "canceled", 500, `{"event":"new-cancel"}`,
	))
	group, err = GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "staff", group)

	_, err = AdminInvalidateUserSubscription(subscription.Id)
	require.NoError(t, err)
	require.NoError(t, RenewProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_ordered",
		"invoice_after_admin_cancel",
		"pi_after_admin_cancel",
		9.99,
		600,
		`{"event":"renew-after-admin-cancel"}`,
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, "cancelled", subscription.Status)
	assert.Zero(t, subscription.ProviderEventTime)
}

func TestProviderSubscriptionStatusUpdatesEveryRenewalOrder(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 524, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 524)
	insertSubscriptionOrderForPaymentGuardTest(t, "status-all-initial", 524, plan.Id, PaymentProviderStripe)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiersAt(
		"status-all-initial",
		PaymentProviderStripe,
		"",
		"sub_status_all",
		"pi_status_initial",
		"active",
		100,
		"",
	))
	require.NoError(t, CompleteSubscriptionOrder("status-all-initial", "", PaymentProviderStripe, ""))
	require.NoError(t, RenewProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_status_all",
		"status_renewal",
		"pi_status_renewal",
		9.99,
		200,
		"",
	))

	require.NoError(t, UpdateProviderSubscriptionStatusAt(
		PaymentProviderStripe,
		"sub_status_all",
		"past_due",
		300,
		`{"event":"past-due"}`,
	))
	require.NoError(t, UpdateProviderSubscriptionStatusAt(
		PaymentProviderStripe,
		"sub_status_all",
		"active",
		250,
		`{"event":"stale-active"}`,
	))
	require.NoError(t, SetSubscriptionOrderProviderIdentifiersAt(
		"status-all-initial",
		PaymentProviderStripe,
		"",
		"sub_status_all",
		"pi_status_initial",
		"active",
		150,
		`{"event":"stale-checkout"}`,
	))
	var orders []SubscriptionOrder
	require.NoError(t, DB.Where(
		"payment_provider = ? AND provider_subscription_id = ?",
		PaymentProviderStripe,
		"sub_status_all",
	).Order("id asc").Find(&orders).Error)
	require.Len(t, orders, 2)
	for _, order := range orders {
		assert.Equal(t, "past_due", order.ProviderStatus)
		assert.Equal(t, int64(300), order.ProviderEventTime)
		assert.Equal(t, `{"event":"past-due"}`, order.ProviderPayload)
	}

	require.NoError(t, CancelProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_status_all",
		"canceled",
		400,
		`{"event":"cancel"}`,
	))
	require.NoError(t, UpdateProviderSubscriptionStatusAt(
		PaymentProviderStripe,
		"sub_status_all",
		"active",
		400,
		`{"event":"same-second-active"}`,
	))
	orders = nil
	require.NoError(t, DB.Where(
		"payment_provider = ? AND provider_subscription_id = ?",
		PaymentProviderStripe,
		"sub_status_all",
	).Order("id asc").Find(&orders).Error)
	for _, order := range orders {
		assert.Equal(t, "canceled", order.ProviderStatus)
		assert.Equal(t, int64(400), order.ProviderEventTime)
		assert.Equal(t, `{"event":"cancel"}`, order.ProviderPayload)
	}

	require.NoError(t, UpdateProviderSubscriptionStatusAt(
		PaymentProviderStripe,
		"sub_status_all",
		"refunded",
		500,
		`{"event":"refund"}`,
	))
	require.NoError(t, CancelProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_status_all",
		"canceled",
		500,
		`{"event":"same-second-cancel"}`,
	))
	orders = nil
	require.NoError(t, DB.Where(
		"payment_provider = ? AND provider_subscription_id = ?",
		PaymentProviderStripe,
		"sub_status_all",
	).Order("id asc").Find(&orders).Error)
	for _, order := range orders {
		assert.Equal(t, "refunded", order.ProviderStatus)
		assert.Equal(t, int64(500), order.ProviderEventTime)
		assert.Equal(t, `{"event":"refund"}`, order.ProviderPayload)
	}
}

func TestProviderSubscriptionLifecycleUpdatesAreIdempotent(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 517, 0)
	plan := insertSubscriptionPlanForPaymentGuardTest(t, 517)
	insertSubscriptionOrderForPaymentGuardTest(
		t, "provider-lifecycle-replay", 517, plan.Id, PaymentProviderStripe,
	)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiers(
		"provider-lifecycle-replay",
		PaymentProviderStripe,
		"cus_lifecycle",
		"sub_lifecycle",
		"pi_initial",
		"active",
		`{"status":"active"}`,
	))

	for range 2 {
		require.NoError(t, UpdateProviderSubscriptionStatus(
			PaymentProviderStripe,
			"sub_lifecycle",
			"past_due",
			`{"status":"past_due"}`,
		))
		require.NoError(t, SetProviderSubscriptionPayment(
			PaymentProviderStripe,
			"sub_lifecycle",
			"pi_retry",
			"active",
			`{"status":"active"}`,
		))
	}
	order := GetSubscriptionOrderByTradeNo("provider-lifecycle-replay")
	require.NotNil(t, order)
	assert.Equal(t, "active", order.ProviderStatus)
	assert.Equal(t, "pi_retry", order.ProviderPaymentId)
	require.ErrorIs(t, UpdateProviderSubscriptionStatus(
		PaymentProviderStripe, "missing-subscription", "active", "",
	), ErrSubscriptionOrderNotFound)
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

	plan = &SubscriptionPlan{
		PriceAmount:             1,
		DurationUnit:            SubscriptionDurationMonth,
		DurationValue:           1,
		AllowBalancePay:         &allowBalance,
		QuotaResetPeriod:        SubscriptionResetCustom,
		QuotaResetCustomSeconds: maxSubscriptionResetSecond + 1,
	}
	require.Error(t, ValidateSubscriptionPlanForPurchase(plan))
	assert.Zero(t, calcNextResetTime(time.Unix(1, 0), plan, math.MaxInt64))
}

func TestValidateOptionValueRejectsUnsafeQuotaPerUnit(t *testing.T) {
	require.Error(t, validateOptionValue("QuotaPerUnit", "NaN"))
	require.Error(t, validateOptionValue("QuotaPerUnit", "0"))
	require.Error(t, validateOptionValue("QuotaPerUnit", "2147483648"))
	require.NoError(t, validateOptionValue("QuotaPerUnit", "500000"))
}

func TestValidateOptionValueRejectsUnsafeClaudeThinkingBudgetPercentage(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "0.09", "1.01"} {
		require.Error(t, validateOptionValue("claude.thinking_adapter_budget_tokens_percentage", value))
	}
	require.NoError(t, validateOptionValue("claude.thinking_adapter_budget_tokens_percentage", "0.1"))
	require.NoError(t, validateOptionValue("claude.thinking_adapter_budget_tokens_percentage", "1"))
}

func TestValidateOptionValueRejectsUnsafeGeminiThinkingBudgetPercentage(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "0.001", "1.01"} {
		require.Error(t, validateOptionValue("gemini.thinking_adapter_budget_tokens_percentage", value))
	}
	require.NoError(t, validateOptionValue("gemini.thinking_adapter_budget_tokens_percentage", "0.002"))
	require.NoError(t, validateOptionValue("gemini.thinking_adapter_budget_tokens_percentage", "1"))
}

func TestValidateOptionValueRejectsUnsafeClaudeDefaultMaxTokens(t *testing.T) {
	require.NoError(t, validateOptionValue("claude.default_max_tokens", `{"default":8192,"claude-test":4096}`))
	require.Error(t, validateOptionValue("claude.default_max_tokens", `{"default":-1}`))
	require.Error(t, validateOptionValue("claude.default_max_tokens", `{"default":1073741824}`))
	require.Error(t, validateOptionValue("claude.default_max_tokens", `{"default":1.5}`))
	require.Error(t, validateOptionValue("claude.default_max_tokens", `[]`))
}
