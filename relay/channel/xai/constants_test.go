package xai

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
)

func TestModelListUsesCurrentXAIModels(t *testing.T) {
	for _, model := range []string{
		"grok-4.5",
		"grok-4.3",
		"grok-4.20-0309-reasoning",
		"grok-4.20-0309",
		"grok-4.20-0309-non-reasoning",
		"grok-4.20-multi-agent-0309",
		"grok-build-0.1",
		"grok-build-latest",
		"grok-code-fast-1",
		"grok-imagine-image-quality",
	} {
		assert.Contains(t, ModelList, model)
	}

	for _, retired := range []string{
		"grok-4-1-fast-reasoning",
		"grok-4-fast-non-reasoning",
		"grok-4-0709",
		"grok-3",
		"grok-imagine-image-pro",
		"grok-imagine-video-1.5",
		"grok-3-search",
	} {
		assert.NotContains(t, ModelList, retired)
	}
}

func TestAdvertisedXAIModelsHaveBuiltInPricing(t *testing.T) {
	modelRatios := ratio_setting.GetDefaultModelRatioMap()
	modelPrices := ratio_setting.GetDefaultModelPriceMap()
	for _, model := range ModelList {
		_, hasRatio := modelRatios[model]
		_, hasPrice := modelPrices[model]
		assert.Truef(t, hasRatio || hasPrice, "advertised model %q has no built-in billing configuration", model)
	}
}
