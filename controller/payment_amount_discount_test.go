package controller

import (
	"math"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func TestLookupPaymentAmountDiscountRejectsPlatformIntOverflow(t *testing.T) {
	discounts := map[int]float64{10: 0.75}
	discount, ok := lookupPaymentAmountDiscount(discounts, 10)
	assert.True(t, ok)
	assert.Equal(t, 0.75, discount)

	_, ok = lookupPaymentAmountDiscount(discounts, -1)
	assert.False(t, ok)

	if strconv.IntSize == 32 {
		_, ok = lookupPaymentAmountDiscount(discounts, int64(math.MaxInt32)+1)
		assert.False(t, ok)
	}
}

func TestTokenDisplayMinimumFailsClosedForInvalidQuotaUnit(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	optionMapWasNil := common.OptionMap == nil
	if optionMapWasNil {
		common.OptionMap = make(map[string]string)
	}
	originalValue, hadOriginalValue := common.OptionMap["QuotaPerUnit"]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if optionMapWasNil {
			common.OptionMap = nil
			return
		}
		if hadOriginalValue {
			common.OptionMap["QuotaPerUnit"] = originalValue
			return
		}
		delete(common.OptionMap, "QuotaPerUnit")
	})

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

		assert.Equal(t, int64(common.MaxQuota), tokenDisplayMinimum(1))
	}

	common.OptionMapRWMutex.Lock()
	common.OptionMap["QuotaPerUnit"] = "500000"
	common.OptionMapRWMutex.Unlock()
	assert.Equal(t, int64(1_000_000), tokenDisplayMinimum(2))
	assert.Equal(t, int64(common.MaxQuota), tokenDisplayMinimum(0))
}
