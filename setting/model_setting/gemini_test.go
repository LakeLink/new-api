package model_setting

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultGeminiImageModelsTrackCurrentEndpoints(t *testing.T) {
	for _, model := range []string{
		"gemini-2.5-flash-image",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-lite-image",
		"gemini-3-pro-image",
	} {
		assert.Contains(t, defaultGeminiSettings.SupportedImagineModels, model)
	}

	for _, model := range []string{
		"gemini-2.0-flash-exp-image-generation",
		"gemini-2.0-flash-exp",
		"gemini-3.1-flash-image-preview",
		"gemini-3-pro-image-preview",
	} {
		assert.NotContains(t, defaultGeminiSettings.SupportedImagineModels, model)
	}
}

func TestGeminiThinkingBudgetTokensRejectUnsafeConfiguredRatios(t *testing.T) {
	tests := []struct {
		name       string
		percentage float64
		want       int
	}{
		{name: "configured ratio", percentage: 0.6, want: 600},
		{name: "minimum ratio", percentage: 0.002, want: 2},
		{name: "full ratio", percentage: 1, want: 1000},
		{name: "NaN falls back", percentage: math.NaN(), want: 600},
		{name: "infinity falls back", percentage: math.Inf(1), want: 600},
		{name: "oversized falls back", percentage: 2, want: 600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &GeminiSettings{ThinkingAdapterBudgetTokensPercentage: tt.percentage}
			assert.Equal(t, tt.want, settings.GetThinkingBudgetTokens(1000))
		})
	}
}
