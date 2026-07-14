package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildEpayTopUpQuotePersistsExactCreditAndRoundedCharge(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	originalPrice := operation_setting.Price
	originalDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	originalDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	originalRatios := common.TopupGroupRatio2JSONString()
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		operation_setting.Price = originalPrice
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalDisplayType
		operation_setting.GetPaymentSetting().AmountDiscount = originalDiscounts
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(originalRatios))
	})

	common.QuotaPerUnit = 500000
	operation_setting.Price = 7.3
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{10: 0.75}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"vip":1.2}`))

	quote, err := buildEpayTopUpQuote(10, "vip")
	require.NoError(t, err)
	assert.Equal(t, 5000000, quote.Quota)
	assert.Equal(t, "65.70", quote.PayText)
	assert.Equal(t, 65.7, quote.PayMoney)

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	operation_setting.GetPaymentSetting().AmountDiscount = nil
	quote, err = buildEpayTopUpQuote(1500000, "default")
	require.NoError(t, err)
	assert.Equal(t, 1500000, quote.Quota)
	assert.Equal(t, "21.90", quote.PayText)
}

func TestBuildEpayTopUpQuoteRejectsUnrepresentableCredit(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	originalPrice := operation_setting.Price
	originalDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		operation_setting.Price = originalPrice
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalDisplayType
	})
	common.QuotaPerUnit = 500000
	operation_setting.Price = 7.3
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD

	_, err := buildEpayTopUpQuote(5000, "default")
	require.Error(t, err)
}
