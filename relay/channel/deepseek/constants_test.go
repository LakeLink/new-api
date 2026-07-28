package deepseek

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelListIncludesDocumentedV4EffortAliases(t *testing.T) {
	models := (&Adaptor{}).GetModelList()

	for _, family := range []string{"deepseek-v4-flash", "deepseek-v4-pro"} {
		assert.Contains(t, models, family)
		for _, suffix := range []string{"-none", "-low", "-medium", "-high", "-xhigh", "-max"} {
			assert.Contains(t, models, family+suffix)
		}
	}
	assert.Contains(t, models, "deepseek-chat")
	assert.Contains(t, models, "deepseek-reasoner")
}

func TestDocumentedV4PricingDefaults(t *testing.T) {
	tests := []struct {
		model      string
		inputRatio float64
		cacheRatio float64
	}{
		{model: "deepseek-chat", inputRatio: 0.14 / 2, cacheRatio: 0.0028 / 0.14},
		{model: "deepseek-reasoner", inputRatio: 0.14 / 2, cacheRatio: 0.0028 / 0.14},
		{model: "deepseek-v4-flash", inputRatio: 0.14 / 2, cacheRatio: 0.0028 / 0.14},
		{model: "deepseek-v4-pro", inputRatio: 0.435 / 2, cacheRatio: 0.003625 / 0.435},
	}

	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			inputRatio, ok := ratio_setting.GetDefaultModelRatioMap()[test.model]
			require.True(t, ok)
			assert.InDelta(t, test.inputRatio, inputRatio, 1e-12)
			assert.Equal(t, 2.0, ratio_setting.GetDefaultCompletionRatio(test.model))
			assert.InDelta(t, test.cacheRatio, ratio_setting.GetDefaultCacheRatio(test.model), 1e-12)
		})
	}

	assert.Equal(t, "deepseek-v4-flash", ratio_setting.FormatMatchingModelName("deepseek-v4-flash-low"))
	assert.Equal(t, "deepseek-v4-pro", ratio_setting.FormatMatchingModelName("deepseek-v4-pro-xhigh"))
	assert.InDelta(t, 0.003625/0.435, ratio_setting.GetDefaultCacheRatio("deepseek-v4-pro-max"), 1e-12)
}
