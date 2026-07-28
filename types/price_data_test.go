package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPriceDataBalancedRatiosHaveStableFloatResults(t *testing.T) {
	priceData := PriceData{}
	priceData.AddOtherRatio("huge", 1e308)
	priceData.AddOtherRatio("tiny", 1e-308)
	priceData.AddOtherRatio("scale", 2)

	for range 100 {
		assert.Equal(t, 2.0, priceData.OtherRatioMultiplier())
		assert.Equal(t, 14.0, priceData.ApplyOtherRatiosToFloat(7))
		assert.Equal(t, 7.0, priceData.RemoveOtherRatiosFromFloat(14))
	}
}
