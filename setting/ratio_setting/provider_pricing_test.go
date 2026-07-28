package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiAndClaudeCurrentDefaultPricing(t *testing.T) {
	claudeCacheWriteRatio := 1.25
	tests := []struct {
		model           string
		modelRatio      float64
		completionRatio float64
		cacheRatio      float64
		cacheWriteRatio *float64
	}{
		{model: "claude-fable-5", modelRatio: 5, completionRatio: 5, cacheRatio: 0.1, cacheWriteRatio: &claudeCacheWriteRatio},
		{model: "claude-mythos-5", modelRatio: 5, completionRatio: 5, cacheRatio: 0.1, cacheWriteRatio: &claudeCacheWriteRatio},
		{model: "claude-sonnet-5", modelRatio: 1, completionRatio: 5, cacheRatio: 0.1, cacheWriteRatio: &claudeCacheWriteRatio},
		{model: "claude-opus-5", modelRatio: 2.5, completionRatio: 5, cacheRatio: 0.1, cacheWriteRatio: &claudeCacheWriteRatio},
		{model: "claude-opus-5-xhigh", modelRatio: 2.5, completionRatio: 5, cacheRatio: 0.1, cacheWriteRatio: &claudeCacheWriteRatio},
		{model: "claude-opus-5-thinking", modelRatio: 2.5, completionRatio: 5, cacheRatio: 0.1, cacheWriteRatio: &claudeCacheWriteRatio},
		{model: "claude-opus-4-8-thinking", modelRatio: 2.5, completionRatio: 5, cacheRatio: 0.1, cacheWriteRatio: &claudeCacheWriteRatio},
		{model: "claude-opus-4-7-thinking", modelRatio: 2.5, completionRatio: 5, cacheRatio: 0.1, cacheWriteRatio: &claudeCacheWriteRatio},
		{model: "gemini-3.6-flash", modelRatio: 0.75, completionRatio: 5, cacheRatio: 0.1},
		{model: "gemini-3.5-flash", modelRatio: 0.75, completionRatio: 6, cacheRatio: 0.1},
		{model: "gemini-3.5-flash-lite", modelRatio: 0.15, completionRatio: 2.5 / 0.3, cacheRatio: 0.1},
		{model: "gemini-3.1-flash-lite", modelRatio: 0.125, completionRatio: 6, cacheRatio: 0.1},
		{model: "gemini-3-flash-preview", modelRatio: 0.25, completionRatio: 6, cacheRatio: 0.1},
		{model: "gemini-flash-latest", modelRatio: 0.75, completionRatio: 5, cacheRatio: 0.1},
		{model: "gemini-flash-lite-latest", modelRatio: 0.15, completionRatio: 2.5 / 0.3, cacheRatio: 0.1},
		{model: "gemini-2.5-pro", modelRatio: 0.625, completionRatio: 8, cacheRatio: 0.1},
		{model: "gemini-2.5-flash", modelRatio: 0.15, completionRatio: 2.5 / 0.3, cacheRatio: 0.1},
		{model: "gemini-2.5-flash-lite", modelRatio: 0.05, completionRatio: 4, cacheRatio: 0.1},
		{model: "gemini-2.5-flash-image", modelRatio: 0.15, completionRatio: 2.5 / 0.3, cacheRatio: 1},
		{model: "gemini-3.1-flash-image", modelRatio: 0.25, completionRatio: 6, cacheRatio: 1},
		{model: "gemini-3.1-flash-lite-image", modelRatio: 0.125, completionRatio: 6, cacheRatio: 1},
		{model: "gemini-3-pro-image", modelRatio: 1, completionRatio: 6, cacheRatio: 1},
		{model: "gemini-2.5-flash-preview-tts", modelRatio: 0.25, completionRatio: 20, cacheRatio: 1},
		{model: "gemini-2.5-pro-preview-tts", modelRatio: 0.5, completionRatio: 20, cacheRatio: 1},
		{model: "gemini-3.1-flash-tts-preview", modelRatio: 0.5, completionRatio: 20, cacheRatio: 1},
		{model: "gemini-2.5-computer-use-preview-10-2025", modelRatio: 0.625, completionRatio: 8, cacheRatio: 1},
		{model: "gemini-3.1-pro-preview", modelRatio: 1, completionRatio: 6, cacheRatio: 0.1},
		{model: "gemini-3.1-pro-preview-customtools", modelRatio: 1, completionRatio: 6, cacheRatio: 0.1},
		{model: "gemini-robotics-er-1.6-preview", modelRatio: 0.5, completionRatio: 5, cacheRatio: 1},
		{model: "gemini-embedding-2", modelRatio: 0.1, completionRatio: 4, cacheRatio: 1},
		{model: "claude-sonnet-4-6", modelRatio: 1.5, completionRatio: 5, cacheRatio: 0.1, cacheWriteRatio: &claudeCacheWriteRatio},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			modelRatio, ok := GetDefaultModelRatioMap()[tt.model]
			require.True(t, ok)
			assert.Equal(t, tt.modelRatio, modelRatio)
			assert.Equal(t, tt.completionRatio, GetDefaultCompletionRatio(tt.model))
			assert.Equal(t, tt.cacheRatio, GetDefaultCacheRatio(tt.model))
			if tt.cacheWriteRatio != nil {
				assert.Equal(t, *tt.cacheWriteRatio, GetDefaultCreateCacheRatio(tt.model))
			}
		})
	}
}

func TestImagenCurrentDefaultPerImagePricing(t *testing.T) {
	prices := GetDefaultModelPriceMap()
	assert.Equal(t, 0.02, prices["imagen-4.0-fast-generate-001"])
	assert.Equal(t, 0.04, prices["imagen-4.0-generate-001"])
	assert.Equal(t, 0.06, prices["imagen-4.0-ultra-generate-001"])
}

func TestClaudeFastPriceRatios(t *testing.T) {
	tests := []struct {
		model     string
		wantInput float64
		wantOK    bool
	}{
		{model: "claude-opus-5", wantInput: 5, wantOK: true},
		{model: "claude-opus-5-max", wantInput: 5, wantOK: true},
		{model: "claude-opus-4-8", wantInput: 5, wantOK: true},
		{model: "claude-opus-4-8-high", wantInput: 5, wantOK: true},
		{model: "claude-opus-4-7", wantOK: false},
		{model: "claude-opus-4-6", wantOK: false},
		{model: "claude-sonnet-4-6", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			ratios, ok := GetClaudeFastPriceRatios(tt.model)
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				return
			}
			assert.Equal(t, tt.wantInput, ratios.ModelRatio)
			assert.Equal(t, 5.0, ratios.CompletionRatio)
		})
	}
}

func TestGeminiLongContextModels(t *testing.T) {
	assert.True(t, IsGeminiLongContextModel("gemini-2.5-pro"))
	assert.True(t, IsGeminiLongContextModel("gemini-2.5-computer-use-preview-10-2025"))
	assert.True(t, IsGeminiLongContextModel("gemini-3.1-pro-preview"))
	assert.True(t, IsGeminiLongContextModel("gemini-3.1-pro-preview-customtools"))
	assert.True(t, IsGeminiLongContextModel("gemini-pro-latest"))
	assert.False(t, IsGeminiLongContextModel("gemini-2.5-flash"))
}

func TestGeminiServiceTierPriceRatios(t *testing.T) {
	tests := []struct {
		model          string
		tier           string
		wantModel      float64
		wantCache      float64
		wantConfigured bool
	}{
		{model: "gemini-3.5-flash", tier: "flex", wantModel: 0.5, wantCache: 0.08 / 0.75, wantConfigured: true},
		{model: "gemini-3.6-flash", tier: "flex", wantModel: 0.5, wantCache: 0.1, wantConfigured: true},
		{model: "gemini-3.5-flash-lite", tier: "priority", wantModel: 1.8, wantCache: 0.05 / 0.54, wantConfigured: true},
		{model: "gemini-3.1-flash-lite", tier: "flex", wantModel: 0.5, wantCache: 0.1, wantConfigured: true},
		{model: "gemini-3-flash-preview", tier: "flex", wantModel: 0.5, wantCache: 0.2, wantConfigured: true},
		{model: "gemini-2.5-pro", tier: "priority", wantModel: 1.8, wantCache: 0.1, wantConfigured: true},
		{model: "gemini-robotics-er-1.6-preview", tier: "priority", wantConfigured: false},
		{model: "gemini-2.5-flash-image", tier: "priority", wantConfigured: false},
		{model: "gemini-3-pro-image", tier: "priority", wantModel: 1.8, wantCache: 1, wantConfigured: true},
		{model: "gemini-3.5-flash", tier: "standard", wantConfigured: false},
	}

	for _, tt := range tests {
		t.Run(tt.model+"/"+tt.tier, func(t *testing.T) {
			pricing, ok := GetGeminiServiceTierPriceRatios(tt.model, tt.tier)
			require.Equal(t, tt.wantConfigured, ok)
			if !ok {
				return
			}
			assert.Equal(t, tt.wantModel, pricing.ModelMultiplier)
			assert.Equal(t, tt.wantCache, pricing.CacheRatio)
		})
	}
}

func TestGeminiImageOutputPricingAndPreConsumeBounds(t *testing.T) {
	tests := []struct {
		model         string
		outputRatio   float64
		maxImageToken int
	}{
		{model: "gemini-2.5-flash-image", outputRatio: 100, maxImageToken: 1290},
		{model: "gemini-3.1-flash-image", outputRatio: 120, maxImageToken: 2520},
		{model: "gemini-3.1-flash-lite-image", outputRatio: 120, maxImageToken: 1120},
		{model: "gemini-3-pro-image", outputRatio: 60, maxImageToken: 2000},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			outputRatio, ok := GetGeminiImageOutputRatio(tt.model)
			require.True(t, ok)
			assert.Equal(t, tt.outputRatio, outputRatio)
			maxImageTokens, ok := GetGeminiMaxImageOutputTokens(tt.model)
			require.True(t, ok)
			assert.Equal(t, tt.maxImageToken, maxImageTokens)
		})
	}
}

func TestClaudeInferenceGeoPricingModels(t *testing.T) {
	for _, model := range []string{
		"claude-opus-4-6",
		"claude-opus-4-7",
		"claude-opus-4-8-high",
		"claude-opus-5",
		"claude-opus-5-xhigh",
		"claude-sonnet-4-6",
		"claude-sonnet-5",
		"claude-fable-5",
		"claude-mythos-5",
	} {
		assert.True(t, IsClaudeInferenceGeoPricingModel(model), model)
	}

	assert.False(t, IsClaudeInferenceGeoPricingModel("claude-sonnet-4-5-20250929"))
	assert.False(t, IsClaudeInferenceGeoPricingModel("claude-opus-4-5-20251101"))
}
