package jimeng

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertImageRequestLocksJimengModelAndPreservesExplicitScalars(t *testing.T) {
	imageCount := uint(1)
	convertedValue, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{}, dto.ImageRequest{
		Model:  "jimeng_high_aes_general_v21_L",
		Prompt: "a red paper umbrella",
		N:      &imageCount,
		ExtraFields: []byte(`{
			"req_key":"a-different-priced-model",
			"prompt":"a different prompt",
			"seed":0,
			"use_pre_llm":false,
			"use_sr":false,
			"return_url":false
		}`),
	})

	require.NoError(t, err)
	converted := convertedValue.(imageRequestPayload)
	assert.Equal(t, "jimeng_high_aes_general_v21_L", converted.ReqKey)
	assert.Equal(t, "a red paper umbrella", converted.Prompt)
	require.NotNil(t, converted.Seed)
	assert.Equal(t, int64(0), *converted.Seed)
	require.NotNil(t, converted.UsePreLLM)
	assert.False(t, *converted.UsePreLLM)
	require.NotNil(t, converted.UseSR)
	assert.False(t, *converted.UseSR)
	require.NotNil(t, converted.ReturnURL)
	assert.False(t, *converted.ReturnURL)

	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"seed":0`)
	assert.Contains(t, string(body), `"use_pre_llm":false`)
	assert.Contains(t, string(body), `"return_url":false`)
}

func TestConvertImageRequestRejectsUnsupportedJimengImageCount(t *testing.T) {
	imageCount := uint(2)

	_, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{}, dto.ImageRequest{
		Model:  "jimeng_high_aes_general_v21_L",
		Prompt: "a red paper umbrella",
		N:      &imageCount,
	})

	require.ErrorContains(t, err, "supports exactly one image")
}

func TestConvertImageRequestBoundsJimengDimensions(t *testing.T) {
	tests := []struct {
		name        string
		extraFields string
	}{
		{name: "width below minimum", extraFields: `{"width":255}`},
		{name: "width above maximum", extraFields: `{"width":769}`},
		{name: "height below minimum", extraFields: `{"height":255}`},
		{name: "height above maximum", extraFields: `{"height":769}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{}, dto.ImageRequest{
				Model:       "jimeng_high_aes_general_v21_L",
				Prompt:      "a red paper umbrella",
				ExtraFields: []byte(test.extraFields),
			})

			require.Error(t, err)
		})
	}
}
