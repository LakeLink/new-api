package model

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalcSubscriptionBalanceQuotaRejectsNonFiniteAndNegativePrices(t *testing.T) {
	tests := []struct {
		name  string
		price float64
	}{
		{name: "negative", price: -0.01},
		{name: "not a number", price: math.NaN()},
		{name: "positive infinity", price: math.Inf(1)},
		{name: "negative infinity", price: math.Inf(-1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := calcSubscriptionBalanceQuota(test.price)
				require.Error(t, err)
			})
		})
	}

	quota, err := calcSubscriptionBalanceQuota(0)
	require.NoError(t, err)
	assert.Zero(t, quota)
}
