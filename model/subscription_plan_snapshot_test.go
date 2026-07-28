package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubscriptionOrderPlanSnapshotFreezesFulfillmentAndRenewalTerms(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
	})

	allowBalancePay := true
	allowWalletOverflow := false
	plan := &SubscriptionPlan{
		Title:                   "Original paid terms",
		PriceAmount:             9.99,
		Currency:                "USD",
		DurationUnit:            SubscriptionDurationCustom,
		CustomSeconds:           3600,
		Enabled:                 true,
		AllowBalancePay:         &allowBalancePay,
		AllowWalletOverflow:     &allowWalletOverflow,
		CreemProductId:          "creem-original-product",
		UpgradeGroup:            "vip",
		DowngradeGroup:          "base",
		TotalAmount:             1200,
		QuotaResetPeriod:        SubscriptionResetCustom,
		QuotaResetCustomSeconds: 300,
	}
	require.NoError(t, DB.Create(plan).Error)
	user := &User{
		Username: "snapshot-fulfillment",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	order := &SubscriptionOrder{
		UserId:          user.Id,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         "snapshot-initial",
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.SetPlanSnapshot(plan))
	originalSnapshot := order.PlanSnapshot
	require.NotEmpty(t, originalSnapshot)
	require.NoError(t, order.Insert())
	// Global balance conversion settings are not paid plan terms and may
	// change while an external provider webhook is delayed.
	common.QuotaPerUnit = float64(common.MaxQuota)

	mutatedWalletOverflow := true
	require.NoError(t, DB.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).Updates(map[string]interface{}{
		"title":                      "Mutated catalog terms",
		"duration_unit":              SubscriptionDurationCustom,
		"custom_seconds":             int64(7200),
		"allow_wallet_overflow":      mutatedWalletOverflow,
		"creem_product_id":           "creem-mutated-product",
		"upgrade_group":              "mutated-vip",
		"downgrade_group":            "mutated-base",
		"total_amount":               int64(9900),
		"quota_reset_period":         SubscriptionResetCustom,
		"quota_reset_custom_seconds": int64(900),
	}).Error)
	persistedOrder := GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.NotNil(t, persistedOrder)
	capturedPlan, err := GetSubscriptionOrderPlan(persistedOrder)
	require.NoError(t, err)
	assert.Equal(t, "creem-original-product", capturedPlan.CreemProductId)
	require.NoError(t, SetSubscriptionOrderProviderIdentifiersAt(
		order.TradeNo,
		PaymentProviderStripe,
		"cus_snapshot",
		"sub_snapshot",
		"pi_snapshot_initial",
		"active",
		100,
		"",
	))
	require.NoError(t, CompleteSubscriptionOrder(
		order.TradeNo,
		"",
		PaymentProviderStripe,
		"",
	))

	var subscription UserSubscription
	require.NoError(t, DB.Where("subscription_order_id = ?", order.Id).First(&subscription).Error)
	assert.Equal(t, int64(3600), subscription.EndTime-subscription.StartTime)
	assert.Equal(t, int64(1200), subscription.AmountTotal)
	assert.Equal(t, int64(300), subscription.NextResetTime-subscription.LastResetTime)
	assert.Equal(t, "vip", subscription.UpgradeGroup)
	assert.Equal(t, "base", subscription.DowngradeGroup)
	assert.False(t, subscription.AllowWalletOverflow)

	var persistedUser User
	require.NoError(t, DB.Where("id = ?", user.Id).First(&persistedUser).Error)
	assert.Equal(t, "vip", persistedUser.Group)

	initialEndTime := subscription.EndTime
	require.NoError(t, RenewProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_snapshot",
		"snapshot-renewal",
		"pi_snapshot_renewal",
		9.99,
		200,
		"",
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, int64(3600), subscription.EndTime-initialEndTime)
	assert.Equal(t, int64(1200), subscription.AmountTotal)
	assert.Equal(t, int64(300), subscription.NextResetTime-subscription.LastResetTime)
	assert.Equal(t, "vip", subscription.UpgradeGroup)
	assert.Equal(t, "base", subscription.DowngradeGroup)
	assert.False(t, subscription.AllowWalletOverflow)

	var renewalOrder SubscriptionOrder
	require.NoError(t, DB.Where("trade_no = ?", "stripe_invoice_snapshot-renewal").First(&renewalOrder).Error)
	assert.Equal(t, originalSnapshot, renewalOrder.PlanSnapshot)

	settledEndTime := subscription.EndTime
	require.NoError(t, DB.Model(&SubscriptionOrder{}).
		Where("id = ?", order.Id).
		Update("plan_snapshot", "{").Error)
	require.Error(t, RenewProviderSubscriptionAt(
		PaymentProviderStripe,
		"sub_snapshot",
		"snapshot-corrupt-renewal",
		"pi_snapshot_corrupt",
		9.99,
		300,
		"",
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, settledEndTime, subscription.EndTime)
	var corruptRenewalCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).
		Where("trade_no = ?", "stripe_invoice_snapshot-corrupt-renewal").
		Count(&corruptRenewalCount).Error)
	assert.Zero(t, corruptRenewalCount)
}

func TestSubscriptionOrderPlanSnapshotFailsClosed(t *testing.T) {
	truncateTables(t)

	plan := &SubscriptionPlan{
		Title:            "Snapshot validation",
		PriceAmount:      5,
		Currency:         "USD",
		DurationUnit:     SubscriptionDurationHour,
		DurationValue:    1,
		Enabled:          true,
		TotalAmount:      500,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	require.NoError(t, DB.Create(plan).Error)
	otherPlan := *plan
	otherPlan.Id = 0
	otherPlan.Title = "Other snapshot plan"
	require.NoError(t, DB.Create(&otherPlan).Error)
	user := &User{
		Username: "snapshot-fail-closed",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	validOrder := &SubscriptionOrder{PlanId: plan.Id}
	require.NoError(t, validOrder.SetPlanSnapshot(plan))
	var corruptedSnapshot subscriptionPlanSnapshot
	require.NoError(t, common.UnmarshalJsonStr(validOrder.PlanSnapshot, &corruptedSnapshot))
	corruptedSnapshot.TotalAmount = 0
	corruptedData, err := common.Marshal(corruptedSnapshot)
	require.NoError(t, err)

	tests := []struct {
		name         string
		tradeNo      string
		planID       int
		planSnapshot string
	}{
		{
			name:         "malformed JSON",
			tradeNo:      "snapshot-malformed",
			planID:       plan.Id,
			planSnapshot: "{",
		},
		{
			name:         "whitespace is not a legacy snapshot",
			tradeNo:      "snapshot-whitespace",
			planID:       plan.Id,
			planSnapshot: " ",
		},
		{
			name:         "mismatched plan",
			tradeNo:      "snapshot-mismatched",
			planID:       otherPlan.Id,
			planSnapshot: validOrder.PlanSnapshot,
		},
		{
			name:         "well-formed terms with stale checksum",
			tradeNo:      "snapshot-corrupted",
			planID:       plan.Id,
			planSnapshot: string(corruptedData),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			order := &SubscriptionOrder{
				UserId:          user.Id,
				PlanId:          test.planID,
				Money:           5,
				PlanSnapshot:    test.planSnapshot,
				TradeNo:         test.tradeNo,
				PaymentMethod:   PaymentMethodStripe,
				PaymentProvider: PaymentProviderStripe,
				Status:          common.TopUpStatusPending,
			}
			require.NoError(t, order.Insert())
			require.Error(t, CompleteSubscriptionOrder(
				test.tradeNo,
				"",
				PaymentProviderStripe,
				"",
			))
			persisted := GetSubscriptionOrderByTradeNo(test.tradeNo)
			require.NotNil(t, persisted)
			assert.Equal(t, common.TopUpStatusPending, persisted.Status)
		})
	}

	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", user.Id).Count(&subscriptionCount).Error)
	assert.Zero(t, subscriptionCount)
}

func TestSubscriptionOrderPlanSnapshotIsPrivateAndTransactionReadsAreFresh(t *testing.T) {
	truncateTables(t)

	plan := &SubscriptionPlan{
		Title:            "Fresh transaction plan",
		PriceAmount:      1,
		Currency:         "USD",
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		Enabled:          true,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() {
		InvalidateSubscriptionPlanCache(plan.Id)
	})
	cached, err := GetSubscriptionPlanById(plan.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(100), cached.TotalAmount)

	require.NoError(t, DB.Model(&SubscriptionPlan{}).
		Where("id = ?", plan.Id).
		Update("total_amount", int64(200)).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		fresh, txErr := getSubscriptionPlanByIdTx(tx, plan.Id)
		if txErr != nil {
			return txErr
		}
		assert.Equal(t, int64(200), fresh.TotalAmount)
		return nil
	}))

	order := &SubscriptionOrder{PlanId: plan.Id}
	require.NoError(t, order.SetPlanSnapshot(cached))
	data, err := common.Marshal(order)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "plan_snapshot")
	assert.NotContains(t, string(data), "Fresh transaction plan")
}

func TestPurchasePlanLookupIgnoresStaleDisplayCache(t *testing.T) {
	truncateTables(t)

	plan := &SubscriptionPlan{
		Title:                 "Initially enabled plan",
		PriceAmount:           9,
		Currency:              "USD",
		DurationUnit:          SubscriptionDurationMonth,
		DurationValue:         1,
		Enabled:               true,
		StripePriceId:         "price_old",
		CreemProductId:        "creem_old",
		WaffoPancakeProductId: "waffo_old",
		QuotaResetPeriod:      SubscriptionResetNever,
		AllowWalletOverflow:   common.GetPointer(true),
		AllowBalancePay:       common.GetPointer(true),
	}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() {
		InvalidateSubscriptionPlanCache(plan.Id)
	})

	// Populate the display cache with the original terms, then mutate the row
	// without invalidating it to model a failed invalidation or another node's
	// process-local cache.
	cached, err := getSubscriptionPlanByIdTx(nil, plan.Id)
	require.NoError(t, err)
	require.True(t, cached.Enabled)
	require.NoError(t, DB.Model(&SubscriptionPlan{}).
		Where("id = ?", plan.Id).
		Updates(map[string]any{
			"enabled":                  false,
			"price_amount":             19,
			"stripe_price_id":          "price_new",
			"creem_product_id":         "creem_new",
			"waffo_pancake_product_id": "waffo_new",
		}).Error)

	authoritative, err := GetSubscriptionPlanById(plan.Id)
	require.NoError(t, err)
	assert.False(t, authoritative.Enabled)
	assert.Equal(t, 19.0, authoritative.PriceAmount)
	assert.Equal(t, "price_new", authoritative.StripePriceId)
	assert.Equal(t, "creem_new", authoritative.CreemProductId)
	assert.Equal(t, "waffo_new", authoritative.WaffoPancakeProductId)

	require.NoError(t, DB.Delete(&SubscriptionPlan{}, plan.Id).Error)
	_, err = GetSubscriptionPlanById(plan.Id)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestBalanceSubscriptionOrderPersistsPlanSnapshotForAudit(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
	})
	common.QuotaPerUnit = 100

	allowBalancePay := true
	allowWalletOverflow := false
	plan := &SubscriptionPlan{
		Title:               "Balance snapshot",
		PriceAmount:         2,
		Currency:            "USD",
		DurationUnit:        SubscriptionDurationMonth,
		DurationValue:       1,
		Enabled:             true,
		AllowBalancePay:     &allowBalancePay,
		AllowWalletOverflow: &allowWalletOverflow,
		TotalAmount:         700,
		QuotaResetPeriod:    SubscriptionResetNever,
	}
	require.NoError(t, DB.Create(plan).Error)
	user := &User{
		Username: "balance-snapshot",
		Group:    "default",
		Status:   common.UserStatusEnabled,
		Quota:    1000,
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, PurchaseSubscriptionWithBalance(user.Id, plan.Id))
	var order SubscriptionOrder
	require.NoError(t, DB.Where(
		"user_id = ? AND payment_provider = ?",
		user.Id,
		PaymentProviderBalance,
	).First(&order).Error)
	require.NotEmpty(t, order.PlanSnapshot)
	snapshotPlan, err := subscriptionPlanFromOrderTx(DB, &order)
	require.NoError(t, err)
	assert.Equal(t, plan.Id, snapshotPlan.Id)
	assert.Equal(t, int64(700), snapshotPlan.TotalAmount)
	require.NotNil(t, snapshotPlan.AllowWalletOverflow)
	assert.False(t, *snapshotPlan.AllowWalletOverflow)
}

func TestLegacySubscriptionOrderFallbackBecomesStableAfterFirstRenewal(t *testing.T) {
	truncateTables(t)

	plan := &SubscriptionPlan{
		Title:            "Legacy fallback",
		PriceAmount:      3,
		Currency:         "USD",
		DurationUnit:     SubscriptionDurationHour,
		DurationValue:    1,
		Enabled:          true,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	require.NoError(t, DB.Create(plan).Error)
	user := &User{
		Username: "legacy-snapshot-fallback",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)
	order := &SubscriptionOrder{
		UserId:          user.Id,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         "legacy-snapshot-initial",
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())
	require.NoError(t, SetSubscriptionOrderProviderIdentifiers(
		order.TradeNo,
		PaymentProviderStripe,
		"",
		"sub_legacy_snapshot",
		"pi_legacy_initial",
		"active",
		"",
	))
	require.NoError(t, CompleteSubscriptionOrder(
		order.TradeNo,
		"",
		PaymentProviderStripe,
		"",
	))

	require.NoError(t, DB.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).Updates(map[string]interface{}{
		"duration_value": 2,
		"total_amount":   int64(200),
	}).Error)
	var subscription UserSubscription
	require.NoError(t, DB.Where("subscription_order_id = ?", order.Id).First(&subscription).Error)
	initialEndTime := subscription.EndTime
	require.NoError(t, RenewProviderSubscription(
		PaymentProviderStripe,
		"sub_legacy_snapshot",
		"legacy-renewal-one",
		"pi_legacy_one",
		3,
		"",
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, int64(2*time.Hour/time.Second), subscription.EndTime-initialEndTime)
	assert.Equal(t, int64(200), subscription.AmountTotal)

	var firstRenewal SubscriptionOrder
	require.NoError(t, DB.Where(
		"trade_no = ?",
		"stripe_invoice_legacy-renewal-one",
	).First(&firstRenewal).Error)
	require.NotEmpty(t, firstRenewal.PlanSnapshot)
	require.NoError(t, DB.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).Updates(map[string]interface{}{
		"duration_value": 3,
		"total_amount":   int64(300),
	}).Error)

	firstRenewalEndTime := subscription.EndTime
	require.NoError(t, RenewProviderSubscription(
		PaymentProviderStripe,
		"sub_legacy_snapshot",
		"legacy-renewal-two",
		"pi_legacy_two",
		3,
		"",
	))
	require.NoError(t, DB.Where("id = ?", subscription.Id).First(&subscription).Error)
	assert.Equal(t, int64(2*time.Hour/time.Second), subscription.EndTime-firstRenewalEndTime)
	assert.Equal(t, int64(200), subscription.AmountTotal)
}
