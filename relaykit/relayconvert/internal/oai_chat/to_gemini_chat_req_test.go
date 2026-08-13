package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	common "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIChatRequestToGeminiPreservesEquivalentPlatformControls(t *testing.T) {
	request, err := OpenAIChatRequestToGeminiGenerateContent(nil, dto.GeneralOpenAIRequest{
		ServiceTier: []byte("\"priority\""),
		Store:       []byte("false"),
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, request.ServiceTier)
	assert.Equal(t, dto.GeminiServiceTierPriority, *request.ServiceTier)
	require.NotNil(t, request.Store)
	assert.False(t, *request.Store)
}

func TestOpenAIChatRequestToGeminiPreservesExplicitZeroControls(t *testing.T) {
	zeroFloat := 0.0
	zeroInt := 0
	zeroSeed := int64(0)
	zeroUint := uint(0)
	falseValue := false
	request, err := OpenAIChatRequestToGeminiGenerateContent(nil, dto.GeneralOpenAIRequest{
		Model:            "gemini-2.5-flash",
		TopP:             &zeroFloat,
		TopK:             &zeroInt,
		N:                &zeroInt,
		Seed:             &zeroSeed,
		MaxTokens:        &zeroUint,
		LogProbs:         &falseValue,
		PresencePenalty:  &zeroFloat,
		FrequencyPenalty: &zeroFloat,
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, request.GenerationConfig.TopP)
	assert.Zero(t, *request.GenerationConfig.TopP)
	require.NotNil(t, request.GenerationConfig.TopK)
	assert.Zero(t, *request.GenerationConfig.TopK)
	require.NotNil(t, request.GenerationConfig.CandidateCount)
	assert.Zero(t, *request.GenerationConfig.CandidateCount)
	require.NotNil(t, request.GenerationConfig.Seed)
	assert.Zero(t, *request.GenerationConfig.Seed)
	require.NotNil(t, request.GenerationConfig.MaxOutputTokens)
	assert.Zero(t, *request.GenerationConfig.MaxOutputTokens)
	require.NotNil(t, request.GenerationConfig.ResponseLogprobs)
	assert.False(t, *request.GenerationConfig.ResponseLogprobs)
	require.NotNil(t, request.GenerationConfig.PresencePenalty)
	assert.Zero(t, *request.GenerationConfig.PresencePenalty)
	require.NotNil(t, request.GenerationConfig.FrequencyPenalty)
	assert.Zero(t, *request.GenerationConfig.FrequencyPenalty)

	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(encoded, "generationConfig.topP").Exists())
	assert.True(t, gjson.GetBytes(encoded, "generationConfig.topK").Exists())
	assert.True(t, gjson.GetBytes(encoded, "generationConfig.candidateCount").Exists())
	assert.True(t, gjson.GetBytes(encoded, "generationConfig.seed").Exists())
	assert.True(t, gjson.GetBytes(encoded, "generationConfig.maxOutputTokens").Exists())
	assert.True(t, gjson.GetBytes(encoded, "generationConfig.responseLogprobs").Exists())
	assert.True(t, gjson.GetBytes(encoded, "generationConfig.presencePenalty").Exists())
	assert.True(t, gjson.GetBytes(encoded, "generationConfig.frequencyPenalty").Exists())
}

func TestOpenAIChatRequestToGeminiMapsTopLogProbs(t *testing.T) {
	logprobs := true
	topLogProbs := 0

	request, err := OpenAIChatRequestToGeminiGenerateContent(nil, dto.GeneralOpenAIRequest{
		LogProbs:    &logprobs,
		TopLogProbs: &topLogProbs,
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, request.GenerationConfig.ResponseLogprobs)
	assert.True(t, *request.GenerationConfig.ResponseLogprobs)
	require.NotNil(t, request.GenerationConfig.Logprobs)
	assert.Zero(t, *request.GenerationConfig.Logprobs)
}

func TestOpenAIChatRequestToGeminiRejectsInvalidTopLogProbsCombination(t *testing.T) {
	logprobs := false
	topLogProbs := 1

	_, err := OpenAIChatRequestToGeminiGenerateContent(nil, dto.GeneralOpenAIRequest{
		LogProbs:    &logprobs,
		TopLogProbs: &topLogProbs,
	}, nil)

	require.ErrorContains(t, err, "logprobs must be true")
}
