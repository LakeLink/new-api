package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesRequestToGeminiMapsDefaultTierAndStore(t *testing.T) {
	request, err := OpenAIResponsesRequestToGeminiChat(nil, &dto.OpenAIResponsesRequest{
		Model:       "gemini-3.5-flash",
		Input:       []byte("\"hello\""),
		ServiceTier: "default",
		Store:       []byte("false"),
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, request.ServiceTier)
	assert.Equal(t, dto.GeminiServiceTierStandard, *request.ServiceTier)
	require.NotNil(t, request.Store)
	assert.False(t, *request.Store)
}

func TestOpenAIResponsesRequestToGeminiPreservesExplicitZeroControls(t *testing.T) {
	zeroFloat := 0.0
	zeroUint := uint(0)
	request, err := OpenAIResponsesRequestToGeminiChat(nil, &dto.OpenAIResponsesRequest{
		Model:           "gemini-2.5-flash",
		Input:           []byte(`"hello"`),
		TopP:            &zeroFloat,
		MaxOutputTokens: &zeroUint,
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, request.GenerationConfig.TopP)
	assert.Zero(t, *request.GenerationConfig.TopP)
	require.NotNil(t, request.GenerationConfig.MaxOutputTokens)
	assert.Zero(t, *request.GenerationConfig.MaxOutputTokens)

	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(encoded, "generationConfig.topP").Exists())
	assert.True(t, gjson.GetBytes(encoded, "generationConfig.maxOutputTokens").Exists())
}
