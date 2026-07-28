package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentXAIAndPerplexityDefaultPricing(t *testing.T) {
	tests := []struct {
		model           string
		modelRatio      float64
		completionRatio float64
		cacheRatio      float64
	}{
		{model: "grok-4.5", modelRatio: 1, completionRatio: 3, cacheRatio: 0.15},
		{model: "grok-4.3-latest", modelRatio: 0.625, completionRatio: 2, cacheRatio: 0.16},
		{model: "grok-4.20-0309-reasoning", modelRatio: 0.625, completionRatio: 2, cacheRatio: 0.16},
		{model: "grok-build-0.1", modelRatio: 0.5, completionRatio: 2, cacheRatio: 0.2},
		{model: "grok-code-fast-1", modelRatio: 0.5, completionRatio: 2, cacheRatio: 0.2},
		{model: "sonar", modelRatio: 0.5, completionRatio: 1, cacheRatio: 1},
		{model: "sonar-pro", modelRatio: 1.5, completionRatio: 5, cacheRatio: 1},
		{model: "sonar-reasoning-pro", modelRatio: 1, completionRatio: 4, cacheRatio: 1},
		{model: "sonar-deep-research", modelRatio: 1, completionRatio: 4, cacheRatio: 1},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			modelRatio, ok := GetDefaultModelRatioMap()[tt.model]
			require.True(t, ok)
			assert.Equal(t, tt.modelRatio, modelRatio)
			assert.Equal(t, tt.completionRatio, GetDefaultCompletionRatio(tt.model))
			assert.Equal(t, tt.cacheRatio, GetDefaultCacheRatio(tt.model))
		})
	}

	assert.Equal(t, 0.05, GetDefaultModelPriceMap()["grok-imagine-image-quality"])
	assert.Equal(t, 0.02, GetDefaultModelPriceMap()["grok-imagine-image"])
}

func TestRetiredProviderDefaultsAreNotAdvertisedAsCurrentPrices(t *testing.T) {
	for _, model := range []string{
		"llama-3-sonar-small-32k-chat",
		"llama-3-sonar-large-32k-online",
		"grok-3-beta",
		"grok-2",
		"grok-beta",
	} {
		_, ok := GetDefaultModelRatioMap()[model]
		assert.False(t, ok, model)
	}
}
