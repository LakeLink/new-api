package cohere

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCohereModelListTracksCurrentPublishedCatalog(t *testing.T) {
	currentModels := []string{
		"command-a-plus-05-2026",
		"command-a-03-2025",
		"command-r7b-12-2024",
		"command-r-08-2024",
		"command-r-plus-08-2024",
		"c4ai-aya-expanse-32b",
		"rerank-v4.0-fast",
		"rerank-v4.0-pro",
		"rerank-v3.5",
	}
	for _, model := range currentModels {
		assert.Contains(t, ModelList, model)
	}

	retiredOrDeprecatedModels := []string{
		"command",
		"command-light",
		"command-r",
		"command-r-plus",
		"c4ai-aya-23-8b",
		"c4ai-aya-23-35b",
		"rerank-english-v2.0",
		"rerank-multilingual-v2.0",
	}
	for _, model := range retiredOrDeprecatedModels {
		assert.NotContains(t, ModelList, model)
	}
}

func TestCoherePublishedDefaultPricesUseProviderBillingUnits(t *testing.T) {
	prices := ratio_setting.GetDefaultModelPriceMap()
	require.Contains(t, prices, "rerank-v4.0-fast")
	require.Contains(t, prices, "rerank-v4.0-pro")
	assert.InDelta(t, 2.0/1000, prices["rerank-v4.0-fast"], 1e-12)
	assert.InDelta(t, 2.5/1000, prices["rerank-v4.0-pro"], 1e-12)

	ratios := ratio_setting.GetDefaultModelRatioMap()
	for _, freeModel := range []string{
		"command-a-plus-05-2026",
		"command-a-translate-08-2025",
		"command-a-reasoning-08-2025",
		"command-a-vision-07-2025",
		"north-mini-code-1-0",
	} {
		require.Contains(t, ratios, freeModel)
		assert.Zero(t, ratios[freeModel])
	}
	assert.InDelta(t, 0.15/2, ratios["command-r-08-2024"], 1e-12)
	assert.InDelta(t, 0.0375/2, ratios["command-r7b-12-2024"], 1e-12)
	assert.InDelta(t, 2.5/2, ratios["command-r-plus-08-2024"], 1e-12)
	assert.InDelta(t, 0.5/2, ratios["c4ai-aya-expanse-32b"], 1e-12)
	assert.InDelta(t, 3, ratio_setting.GetDefaultCompletionRatio("c4ai-aya-expanse-32b"), 1e-12)
	assert.NotContains(t, ratios, "c4ai-aya-vision-32b", "Aya Vision has no published pay-as-you-go price")
}
