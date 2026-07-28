package perplexity

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIRequestPreservesWebSearchOptions(t *testing.T) {
	stream := true
	request := &dto.GeneralOpenAIRequest{
		Model:  "sonar-pro",
		Stream: &stream,
		Messages: []dto.Message{{
			Role:    "user",
			Content: "Compare the sources",
		}},
		WebSearchOptions: &dto.WebSearchOptions{
			SearchContextSize: "high",
			SearchType:        "PRO",
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(&gin.Context{}, &common.RelayInfo{}, request)
	require.NoError(t, err)

	upstream, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, upstream.WebSearchOptions)
	assert.Equal(t, "high", upstream.WebSearchOptions.SearchContextSize)
	assert.Equal(t, "pro", upstream.WebSearchOptions.SearchType)
}

func TestConvertOpenAIRequestRejectsUnknownSearchType(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "sonar-pro",
		WebSearchOptions: &dto.WebSearchOptions{
			SearchType: "expensive",
		},
	}

	_, err := (&Adaptor{}).ConvertOpenAIRequest(&gin.Context{}, &common.RelayInfo{}, request)
	require.ErrorContains(t, err, "unsupported Perplexity search_type")
}

func TestConvertOpenAIRequestPreservesProviderDefaultSearchContext(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:            "sonar",
		WebSearchOptions: &dto.WebSearchOptions{},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)

	require.NoError(t, err)
	upstream, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, upstream.WebSearchOptions)
	assert.Empty(t, upstream.WebSearchOptions.SearchContextSize)
}

func TestConvertOpenAIRequestPreservesDocumentedGenerationParameters(t *testing.T) {
	topP := 1.0
	stop := []string{"END"}
	responseFormat := &dto.ResponseFormat{
		Type:       "json_schema",
		JsonSchema: []byte(`{"name":"answer","schema":{"type":"object"}}`),
	}
	request := &dto.GeneralOpenAIRequest{
		Model:           "sonar-reasoning-pro",
		TopP:            &topP,
		Stop:            stop,
		ResponseFormat:  responseFormat,
		ReasoningEffort: "high",
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)

	require.NoError(t, err)
	upstream, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, upstream.TopP)
	assert.Equal(t, 1.0, *upstream.TopP)
	assert.Equal(t, stop, upstream.Stop)
	assert.Equal(t, responseFormat, upstream.ResponseFormat)
	assert.Equal(t, "high", upstream.ReasoningEffort)
}

func TestConvertOpenAIRequestValidatesProSearchRequirements(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "sonar",
		WebSearchOptions: &dto.WebSearchOptions{
			SearchType: "pro",
		},
	}

	_, err := (&Adaptor{}).ConvertOpenAIRequest(&gin.Context{}, &common.RelayInfo{}, request)
	require.ErrorContains(t, err, "only supported by sonar-pro")

	request.Model = "sonar-pro"
	_, err = (&Adaptor{}).ConvertOpenAIRequest(&gin.Context{}, &common.RelayInfo{}, request)
	require.ErrorContains(t, err, "requires streaming")
}

func TestPerplexityModelListContainsOnlyCurrentSonarModels(t *testing.T) {
	assert.Equal(t, []string{
		"sonar",
		"sonar-pro",
		"sonar-reasoning-pro",
		"sonar-deep-research",
	}, ModelList)
}

func TestAdvertisedPerplexityModelsHaveBuiltInPricing(t *testing.T) {
	for _, model := range ModelList {
		_, ok := ratio_setting.GetDefaultModelRatioMap()[model]
		assert.Truef(t, ok, "advertised model %q has no built-in billing configuration", model)
	}
}
