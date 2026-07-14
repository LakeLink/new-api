package perplexity

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/common"
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

func TestPerplexityModelListContainsOnlyCurrentSonarModels(t *testing.T) {
	assert.Equal(t, []string{
		"sonar",
		"sonar-pro",
		"sonar-reasoning-pro",
		"sonar-deep-research",
	}, ModelList)
}
