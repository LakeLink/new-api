package controller

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreemAmountCentsUsesDecimalRounding(t *testing.T) {
	cents, err := creemAmountCents(10.005)
	require.NoError(t, err)
	assert.Equal(t, 1001, cents)

	cents, err = creemAmountCents(0.01)
	require.NoError(t, err)
	assert.Equal(t, 1, cents)
}

func TestCreemAmountCentsRejectsUnsafePrices(t *testing.T) {
	for _, price := range []float64{0, -1, math.NaN(), math.Inf(1), float64(math.MaxInt32)/100 + 1} {
		_, err := creemAmountCents(price)
		require.Error(t, err)
	}
}
