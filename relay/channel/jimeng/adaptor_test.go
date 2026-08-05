package jimeng

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	assert.True(t, *converted.ReturnURL)

	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"seed":0`)
	assert.Contains(t, string(body), `"use_pre_llm":false`)
	assert.Contains(t, string(body), `"return_url":true`)
}

func TestConvertImageRequestKeepsOpenAIResponseFormatAuthoritative(t *testing.T) {
	convertedValue, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{}, dto.ImageRequest{
		Model:          "jimeng_high_aes_general_v21_L",
		Prompt:         "a red paper umbrella",
		ResponseFormat: "b64_json",
		ExtraFields:    []byte(`{"return_url":true}`),
	})

	require.NoError(t, err)
	converted := convertedValue.(imageRequestPayload)
	require.NotNil(t, converted.ReturnURL)
	assert.False(t, *converted.ReturnURL)

	_, err = (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{}, dto.ImageRequest{
		Model:          "jimeng_high_aes_general_v21_L",
		Prompt:         "a red paper umbrella",
		ResponseFormat: "unsupported",
	})
	require.ErrorContains(t, err, "unsupported response_format")
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

func TestConvertImageRequestValidatesJimengNativeOptions(t *testing.T) {
	convertedValue, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{}, dto.ImageRequest{
		Model:  "jimeng_high_aes_general_v21_L",
		Prompt: "a red paper umbrella",
		ExtraFields: []byte(`{
			"logo_info":{
				"add_logo":false,
				"position":0,
				"language":0,
				"opacity":0,
				"logo_text_content":""
			},
			"aigc_meta":{
				"content_producer":"",
				"producer_id":"producer-id"
			}
		}`),
	})

	require.NoError(t, err)
	converted := convertedValue.(imageRequestPayload)
	require.NotNil(t, converted.LogoInfo)
	require.NotNil(t, converted.LogoInfo.AddLogo)
	assert.False(t, *converted.LogoInfo.AddLogo)
	require.NotNil(t, converted.LogoInfo.Position)
	assert.Zero(t, *converted.LogoInfo.Position)
	require.NotNil(t, converted.LogoInfo.Language)
	assert.Zero(t, *converted.LogoInfo.Language)
	require.NotNil(t, converted.LogoInfo.Opacity)
	assert.Zero(t, *converted.LogoInfo.Opacity)
	require.NotNil(t, converted.LogoInfo.LogoTextContent)
	assert.Empty(t, *converted.LogoInfo.LogoTextContent)
	require.NotNil(t, converted.AIGCMeta)
	require.NotNil(t, converted.AIGCMeta.ProducerID)
	assert.Equal(t, "producer-id", *converted.AIGCMeta.ProducerID)

	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"add_logo":false`)
	assert.Contains(t, string(body), `"position":0`)
	assert.Contains(t, string(body), `"language":0`)
	assert.Contains(t, string(body), `"opacity":0`)
	assert.Contains(t, string(body), `"logo_text_content":""`)

	tests := []struct {
		name        string
		extraFields string
		errorSubstr string
	}{
		{
			name:        "logo position below range",
			extraFields: `{"logo_info":{"position":-1}}`,
			errorSubstr: "logo_info.position",
		},
		{
			name:        "logo position above range",
			extraFields: `{"logo_info":{"position":4}}`,
			errorSubstr: "logo_info.position",
		},
		{
			name:        "logo language below range",
			extraFields: `{"logo_info":{"language":-1}}`,
			errorSubstr: "logo_info.language",
		},
		{
			name:        "logo language above range",
			extraFields: `{"logo_info":{"language":2}}`,
			errorSubstr: "logo_info.language",
		},
		{
			name:        "logo opacity below range",
			extraFields: `{"logo_info":{"opacity":-0.01}}`,
			errorSubstr: "logo_info.opacity",
		},
		{
			name:        "logo opacity above range",
			extraFields: `{"logo_info":{"opacity":1.01}}`,
			errorSubstr: "logo_info.opacity",
		},
		{
			name:        "aigc producer id missing",
			extraFields: `{"aigc_meta":{"content_producer":"publisher"}}`,
			errorSubstr: "aigc_meta.producer_id",
		},
		{
			name:        "aigc producer id blank",
			extraFields: `{"aigc_meta":{"producer_id":"  "}}`,
			errorSubstr: "aigc_meta.producer_id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{}, dto.ImageRequest{
				Model:       "jimeng_high_aes_general_v21_L",
				Prompt:      "a red paper umbrella",
				ExtraFields: []byte(test.extraFields),
			})

			require.ErrorContains(t, err, test.errorSubstr)
		})
	}
}

func TestJimengImageModelListKeepsOnlySynchronousCompatibilityModel(t *testing.T) {
	models := (&Adaptor{}).GetModelList()

	assert.Equal(t, []string{"jimeng_high_aes_general_v21_L"}, models)
	for _, asyncModel := range []string{
		"jimeng_t2i_v30",
		"jimeng_t2i_v31",
		"jimeng_i2i_v30",
		"jimeng_t2i_v40",
		"jimeng_seedream46_cvtob",
	} {
		assert.NotContains(t, models, asyncModel)
		_, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{}, dto.ImageRequest{
			Model:  asyncModel,
			Prompt: "a red paper umbrella",
		})
		require.ErrorContains(t, err, "requires Jimeng's asynchronous image protocol")
	}
}

func TestJimengImageRequestURLAvoidsDuplicateSlashes(t *testing.T) {
	requestURL, err := (&Adaptor{}).GetRequestURL(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://visual.volcengineapi.com/"},
	})

	require.NoError(t, err)
	assert.Equal(t, "https://visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31", requestURL)
}

func TestJimengImageSignerUsesOutboundRequestAndRejectsEmptyCredentials(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"https://visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31",
		strings.NewReader(`{"req_key":"jimeng_high_aes_general_v21_L"}`),
	)

	require.NoError(t, Sign(nil, request, "access-key|secret-key"))
	assert.Contains(t, request.Header.Get("Authorization"), "Credential=access-key/")
	assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
	body, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"req_key":"jimeng_high_aes_general_v21_L"}`, string(body))

	require.Error(t, Sign(nil, request, "|secret-key"))
	require.Error(t, Sign(nil, request, "access-key|"))
}
