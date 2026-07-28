package claude

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
)

func TestModelListIncludesCurrentClaudeModels(t *testing.T) {
	for _, model := range []string{
		"claude-fable-5",
		"claude-mythos-5",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-opus-4-8",
		"claude-opus-5",
		"claude-opus-5-max",
		"claude-opus-5-thinking",
	} {
		assert.Contains(t, ModelList, model)
	}
}

func TestAdvertisedClaudeModelsHaveDefaultTokenAndCachePricing(t *testing.T) {
	defaultRatios := ratio_setting.GetDefaultModelRatioMap()
	for _, model := range ModelList {
		if _, ok := defaultRatios[model]; !assert.True(t, ok, "missing input pricing for %s", model) {
			continue
		}
		assert.Greater(t, ratio_setting.GetDefaultCompletionRatio(model), 0.0, model)
		assert.Equal(t, 0.1, ratio_setting.GetDefaultCacheRatio(model), model)
		assert.Equal(t, 1.25, ratio_setting.GetDefaultCreateCacheRatio(model), model)
	}
}
