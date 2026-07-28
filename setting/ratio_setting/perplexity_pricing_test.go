package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerplexityRequestPrice(t *testing.T) {
	price, ok := GetPerplexityRequestPrice("sonar", "", "")
	require.True(t, ok)
	assert.Equal(t, 0.005, price)

	price, ok = GetPerplexityRequestPrice("sonar-pro", "high", "fast")
	require.True(t, ok)
	assert.Equal(t, 0.014, price)

	price, ok = GetPerplexityRequestPrice("sonar-pro", "medium", "auto")
	require.True(t, ok)
	assert.Equal(t, 0.018, price)

	_, ok = GetPerplexityRequestPrice("sonar-deep-research", "low", "")
	assert.False(t, ok)
	_, ok = GetPerplexityRequestPrice("sonar", "invalid", "")
	assert.False(t, ok)
}
