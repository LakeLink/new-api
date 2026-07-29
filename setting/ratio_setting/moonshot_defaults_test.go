package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMoonshotPublishedTokenPrices(t *testing.T) {
	tests := []struct {
		model      string
		inputRMB   float64
		cachedRMB  float64
		outputRMB  float64
		hasCaching bool
	}{
		{model: "kimi-k3", inputRMB: 20, cachedRMB: 2, outputRMB: 100, hasCaching: true},
		{model: "kimi-k2.7-code", inputRMB: 6.5, cachedRMB: 1.3, outputRMB: 27, hasCaching: true},
		{model: "kimi-k2.7-code-highspeed", inputRMB: 13, cachedRMB: 2.6, outputRMB: 54, hasCaching: true},
		{model: "kimi-k2.6", inputRMB: 6.5, cachedRMB: 1.1, outputRMB: 27, hasCaching: true},
		{model: "kimi-k2.5", inputRMB: 4, cachedRMB: 0.7, outputRMB: 21, hasCaching: true},
		{model: "moonshot-v1-8k", inputRMB: 2, outputRMB: 10},
		{model: "moonshot-v1-32k", inputRMB: 5, outputRMB: 20},
		{model: "moonshot-v1-128k", inputRMB: 10, outputRMB: 30},
		{model: "moonshot-v1-8k-vision-preview", inputRMB: 2, outputRMB: 10},
		{model: "moonshot-v1-32k-vision-preview", inputRMB: 5, outputRMB: 20},
		{model: "moonshot-v1-128k-vision-preview", inputRMB: 10, outputRMB: 30},
	}

	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			modelRatio, ok := GetDefaultModelRatioMap()[test.model]
			if assert.True(t, ok) {
				assert.InDelta(t, test.inputRMB/1000*RMB, modelRatio, 1e-12)
			}
			assert.InDelta(t, test.outputRMB/test.inputRMB, GetDefaultCompletionRatio(test.model), 1e-12)
			if test.hasCaching {
				assert.InDelta(t, test.cachedRMB/test.inputRMB, GetDefaultCacheRatio(test.model), 1e-12)
			} else {
				assert.Equal(t, 1.0, GetDefaultCacheRatio(test.model))
			}
		})
	}
}
