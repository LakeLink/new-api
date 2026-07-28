package model

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopUpQuotaConversionRejectsCorruptNumericStateWithoutPanicking(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = map[string]string{"QuotaPerUnit": "NaN"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	require.NotPanics(t, func() {
		_, err := TopUpQuotaForAmount(1, false)
		require.Error(t, err)
	})
	require.NotPanics(t, func() {
		_, err := topUpCreditQuota(&TopUp{
			Amount:          1,
			Money:           math.Inf(1),
			PaymentProvider: PaymentProviderStripe,
		})
		require.Error(t, err)
	})

	quota, err := topUpCreditQuota(&TopUp{
		Amount:          123,
		PaymentProvider: PaymentProviderCreem,
	})
	require.NoError(t, err)
	assert.Equal(t, 123, quota)

	_, err = topUpCreditQuota(&TopUp{Quota: common.MaxQuota + 1})
	require.Error(t, err)
}

func TestStripeRechargeRejectsNonFinitePersistedQuoteWithoutPanicking(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 525, 100)
	require.NoError(t, (&TopUp{
		UserId:          525,
		Amount:          1,
		Quota:           1000,
		Money:           math.NaN(),
		TradeNo:         "stripe-nonfinite-quote",
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		Status:          common.TopUpStatusPending,
	}).Insert())

	require.NotPanics(t, func() {
		require.Error(t, Recharge(
			"stripe-nonfinite-quote",
			"cus_nonfinite",
			"pi_nonfinite",
			100,
			"127.0.0.1",
		))
	})

	assert.Equal(t, 100, getUserQuotaForPaymentGuardTest(t, 525))
	topUp := GetTopUpByTradeNo("stripe-nonfinite-quote")
	require.NotNil(t, topUp)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
}
