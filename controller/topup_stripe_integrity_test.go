package controller

import (
	"math"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStripeTopUpQuoteMatchesChargeAndCredit(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	originalUnitPrice := setting.StripeUnitPrice
	originalDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	originalDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	originalRatios := common.TopupGroupRatio2JSONString()
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		setting.StripeUnitPrice = originalUnitPrice
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalDisplayType
		operation_setting.GetPaymentSetting().AmountDiscount = originalDiscounts
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(originalRatios))
	})

	common.QuotaPerUnit = 500000
	setting.StripeUnitPrice = 2
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"vip":1.2}`))

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{10: 0.75}
	quote, err := buildStripeTopUpQuote(10, "vip")
	require.NoError(t, err)
	assert.Equal(t, 5000000, quote.Quota)
	assert.Equal(t, int64(1800), quote.PayCents)
	assert.Equal(t, 18.0, quote.PayMoney)

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{1500000: 0.5}
	quote, err = buildStripeTopUpQuote(1500000, "vip")
	require.NoError(t, err)
	assert.Equal(t, 1500000, quote.Quota)
	assert.Equal(t, int64(360), quote.PayCents)
	assert.Equal(t, 3.6, quote.PayMoney)
}

func TestBuildStripeTopUpQuoteRejectsUnrepresentableCredit(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	originalUnitPrice := setting.StripeUnitPrice
	originalDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		setting.StripeUnitPrice = originalUnitPrice
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalDisplayType
	})
	common.QuotaPerUnit = 500000
	setting.StripeUnitPrice = 1
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD

	_, err := buildStripeTopUpQuote(5000, "default")
	require.Error(t, err)
}

func TestBuildStripeTopUpQuoteRejectsInvalidQuotaUnit(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	optionMapWasNil := common.OptionMap == nil
	if optionMapWasNil {
		common.OptionMap = make(map[string]string)
	}
	originalValue, hadOriginalValue := common.OptionMap["QuotaPerUnit"]
	common.OptionMapRWMutex.Unlock()
	originalUnitPrice := setting.StripeUnitPrice
	originalDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if optionMapWasNil {
			common.OptionMap = nil
		} else if hadOriginalValue {
			common.OptionMap["QuotaPerUnit"] = originalValue
		} else {
			delete(common.OptionMap, "QuotaPerUnit")
		}
		common.OptionMapRWMutex.Unlock()
		setting.StripeUnitPrice = originalUnitPrice
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalDisplayType
	})

	setting.StripeUnitPrice = 1
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	for _, quotaPerUnit := range []float64{
		0,
		-1,
		math.NaN(),
		math.Inf(1),
		float64(common.MaxQuota) + 1,
	} {
		common.OptionMapRWMutex.Lock()
		common.OptionMap["QuotaPerUnit"] = strconv.FormatFloat(quotaPerUnit, 'g', -1, 64)
		common.OptionMapRWMutex.Unlock()

		_, err := buildStripeTopUpQuote(10, "default")
		require.Error(t, err)
	}
}
