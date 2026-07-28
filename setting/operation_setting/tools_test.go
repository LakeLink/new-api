package operation_setting

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiInputAudioPricingUsesCurrentModelRates(t *testing.T) {
	tests := []struct {
		model string
		want  float64
	}{
		{model: "gemini-2.5-flash", want: 1},
		{model: "gemini-2.5-flash-lite", want: 0.3},
		{model: "gemini-2.5-flash-native-audio-preview-12-2025", want: 3},
		{model: "gemini-3.1-flash-lite", want: 0.5},
		{model: "gemini-flash-lite-latest", want: 0.5},
		{model: "gemini-3-flash-preview", want: 1},
		{model: "gemini-robotics-er-1.6-preview", want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.want, GetGeminiInputAudioPricePerMillionTokens(tt.model))
		})
	}
}

func TestUpdateToolPricesRejectsUnsafeBillingValues(t *testing.T) {
	original := make(map[string]float64, len(toolPriceSetting.Prices))
	for key, price := range toolPriceSetting.Prices {
		original[key] = price
	}
	t.Cleanup(func() {
		require.NoError(t, UpdateToolPrices(original))
	})

	for name, prices := range map[string]map[string]float64{
		"negative": {"web_search": -1},
		"nan":      {"web_search": math.NaN()},
		"infinite": {"web_search": math.Inf(1)},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, UpdateToolPrices(prices))
		})
	}

	require.NoError(t, UpdateToolPrices(map[string]float64{"custom_tool": 3.5, "disabled_tool": 0}))
	assert.Equal(t, 3.5, GetToolPrice("custom_tool"))
	assert.Zero(t, GetToolPrice("disabled_tool"))
}

func TestGeminiInputAudioPricingUsesActualServiceTier(t *testing.T) {
	assert.Equal(t, 0.15, GetGeminiInputAudioPriceForServiceTier("gemini-2.5-flash-lite", "flex"))
	assert.Equal(t, 0.9, GetGeminiInputAudioPriceForServiceTier("gemini-3.1-flash-lite", "priority"))
	assert.Equal(t, 1.0, GetGeminiInputAudioPriceForServiceTier("gemini-3-flash-preview", "standard"))
	assert.Equal(t, 3.0, GetGeminiInputAudioPriceForServiceTier("gemini-2.5-flash-native-audio-preview-12-2025", "flex"))
	assert.Equal(t, 2.0, GetGeminiInputAudioPriceForServiceTier("gemini-robotics-er-1.6-preview", "priority"))
}

func TestGeminiGroundingSearchUsesGenerationSpecificRates(t *testing.T) {
	assert.Equal(t, 14.0, GetToolPriceForModel("google_search", "gemini-3.5-flash"))
	assert.Equal(t, 35.0, GetToolPriceForModel("google_search", "gemini-2.5-flash"))
	assert.Equal(t, 35.0, GetToolPriceForModel("google_search", "gemini-2.5-pro"))
	assert.Equal(t, 14.0, GetToolPriceForModel("google_maps", "gemini-3.5-flash"))
	assert.Equal(t, 25.0, GetToolPriceForModel("google_maps", "gemini-2.5-pro"))
}
