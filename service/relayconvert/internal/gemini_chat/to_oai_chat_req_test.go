package geminichat

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiGenerateContentRequestToOpenAIChatPreservesOptionalScalars(t *testing.T) {
	zeroFloat64 := 0.0
	zeroInt := 0
	zeroInt64 := int64(0)
	zeroUint := uint(0)
	falseValue := false

	converted, err := GeminiGenerateContentRequestToOpenAIChat(&dto.GeminiChatRequest{
		GenerationConfig: dto.GeminiChatGenerationConfig{
			Temperature:      &zeroFloat64,
			TopP:             &zeroFloat64,
			TopK:             &zeroInt,
			MaxOutputTokens:  &zeroUint,
			CandidateCount:   &zeroInt,
			PresencePenalty:  &zeroFloat64,
			FrequencyPenalty: &zeroFloat64,
			ResponseLogprobs: &falseValue,
			Seed:             &zeroInt64,
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, converted.Temperature)
	assert.Zero(t, *converted.Temperature)
	require.NotNil(t, converted.TopP)
	assert.Zero(t, *converted.TopP)
	require.NotNil(t, converted.TopK)
	assert.Zero(t, *converted.TopK)
	require.NotNil(t, converted.MaxTokens)
	assert.Zero(t, *converted.MaxTokens)
	require.NotNil(t, converted.N)
	assert.Zero(t, *converted.N)
	require.NotNil(t, converted.PresencePenalty)
	assert.Zero(t, *converted.PresencePenalty)
	require.NotNil(t, converted.FrequencyPenalty)
	assert.Zero(t, *converted.FrequencyPenalty)
	require.NotNil(t, converted.LogProbs)
	assert.False(t, *converted.LogProbs)
	require.NotNil(t, converted.Seed)
	assert.Zero(t, *converted.Seed)

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	for _, path := range []string{
		"temperature",
		"top_p",
		"top_k",
		"max_tokens",
		"n",
		"presence_penalty",
		"frequency_penalty",
		"logprobs",
		"seed",
	} {
		assert.True(t, gjson.GetBytes(encoded, path).Exists(), path)
	}
}

func TestGeminiGenerateContentRequestToOpenAIChatMapsTopLogProbs(t *testing.T) {
	logprobs := true
	topLogProbs := int32(20)

	converted, err := GeminiGenerateContentRequestToOpenAIChat(&dto.GeminiChatRequest{
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseLogprobs: &logprobs,
			Logprobs:         &topLogProbs,
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, converted.LogProbs)
	assert.True(t, *converted.LogProbs)
	require.NotNil(t, converted.TopLogProbs)
	assert.Equal(t, 20, *converted.TopLogProbs)
}

func TestGeminiGenerateContentRequestToOpenAIChatMapsEquivalentPlatformControls(t *testing.T) {
	serviceTier := dto.GeminiServiceTierStandard
	store := false

	converted, err := GeminiGenerateContentRequestToOpenAIChat(&dto.GeminiChatRequest{
		ServiceTier: &serviceTier,
		Store:       &store,
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, `"default"`, string(converted.ServiceTier))
	assert.Equal(t, `false`, string(converted.Store))
}

func TestGeminiGenerateContentRequestToOpenAIChatMapsAllowedFunctionNames(t *testing.T) {
	request := &dto.GeminiChatRequest{
		ToolConfig: &dto.ToolConfig{
			FunctionCallingConfig: &dto.FunctionCallingConfig{
				Mode:                 "ANY",
				AllowedFunctionNames: []string{"lookup"},
			},
		},
	}
	request.SetTools([]dto.GeminiChatTool{{
		FunctionDeclarations: []dto.FunctionRequest{
			{Name: "lookup"},
			{Name: "search"},
		},
	}})

	converted, err := GeminiGenerateContentRequestToOpenAIChat(request, nil)

	require.NoError(t, err)
	require.Len(t, converted.Tools, 2)
	toolChoice, err := common.Any2Type[map[string]any](converted.ToolChoice)
	require.NoError(t, err)
	assert.Equal(t, "allowed_tools", toolChoice["type"])
	assert.Equal(t, "required", toolChoice["mode"])
	allowedTools, err := common.Any2Type[[]map[string]any](toolChoice["tools"])
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"type": "function", "name": "lookup"}}, allowedTools)
}

func TestGeminiGenerateContentRequestToOpenAIChatRejectsInvalidFunctionConfig(t *testing.T) {
	request := &dto.GeminiChatRequest{
		ToolConfig: &dto.ToolConfig{
			FunctionCallingConfig: &dto.FunctionCallingConfig{
				Mode:                 "AUTO",
				AllowedFunctionNames: []string{"lookup"},
			},
		},
	}
	request.SetTools([]dto.GeminiChatTool{{
		FunctionDeclarations: []dto.FunctionRequest{{Name: "lookup"}},
	}})

	_, err := GeminiGenerateContentRequestToOpenAIChat(request, nil)
	require.ErrorContains(t, err, "only valid for ANY or VALIDATED")

	request.ToolConfig.FunctionCallingConfig.Mode = "ANY"
	request.ToolConfig.FunctionCallingConfig.AllowedFunctionNames = []string{"undeclared"}
	_, err = GeminiGenerateContentRequestToOpenAIChat(request, nil)
	require.ErrorContains(t, err, "undeclared function")
}
