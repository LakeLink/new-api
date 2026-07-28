package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiChatGenerationConfigPreservesExplicitZeroValuesCamelCase(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"generationConfig":{
			"topP":0,
			"topK":0,
			"maxOutputTokens":0,
			"candidateCount":0,
			"seed":0,
			"responseLogprobs":false
		}
	}`)

	var req GeminiChatRequest
	require.NoError(t, common.Unmarshal(raw, &req))

	encoded, err := common.Marshal(req)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, common.Unmarshal(encoded, &out))

	generationConfig, ok := out["generationConfig"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, generationConfig, "topP")
	assert.Contains(t, generationConfig, "topK")
	assert.Contains(t, generationConfig, "maxOutputTokens")
	assert.Contains(t, generationConfig, "candidateCount")
	assert.Contains(t, generationConfig, "seed")
	assert.Contains(t, generationConfig, "responseLogprobs")

	assert.Equal(t, float64(0), generationConfig["topP"])
	assert.Equal(t, float64(0), generationConfig["topK"])
	assert.Equal(t, float64(0), generationConfig["maxOutputTokens"])
	assert.Equal(t, float64(0), generationConfig["candidateCount"])
	assert.Equal(t, float64(0), generationConfig["seed"])
	assert.Equal(t, false, generationConfig["responseLogprobs"])
}

func TestGeminiChatGenerationConfigPreservesExplicitZeroValuesSnakeCase(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"generationConfig":{
			"top_p":0,
			"top_k":0,
			"max_output_tokens":0,
			"candidate_count":0,
			"seed":0,
			"response_logprobs":false
		}
	}`)

	var req GeminiChatRequest
	require.NoError(t, common.Unmarshal(raw, &req))

	encoded, err := common.Marshal(req)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, common.Unmarshal(encoded, &out))

	generationConfig, ok := out["generationConfig"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, generationConfig, "topP")
	assert.Contains(t, generationConfig, "topK")
	assert.Contains(t, generationConfig, "maxOutputTokens")
	assert.Contains(t, generationConfig, "candidateCount")
	assert.Contains(t, generationConfig, "seed")
	assert.Contains(t, generationConfig, "responseLogprobs")

	assert.Equal(t, float64(0), generationConfig["topP"])
	assert.Equal(t, float64(0), generationConfig["topK"])
	assert.Equal(t, float64(0), generationConfig["maxOutputTokens"])
	assert.Equal(t, float64(0), generationConfig["candidateCount"])
	assert.Equal(t, float64(0), generationConfig["seed"])
	assert.Equal(t, false, generationConfig["responseLogprobs"])
}

func TestGeminiChatGenerationConfigRejectsFractionalTopK(t *testing.T) {
	for _, field := range []string{"topK", "top_k"} {
		var request GeminiChatRequest
		err := common.Unmarshal([]byte(`{
			"contents":[{"role":"user","parts":[{"text":"hello"}]}],
			"generationConfig":{"`+field+`":1.5}
		}`), &request)
		require.Error(t, err)
	}
}

func TestGeminiChatRequestPreservesServiceTierAndExplicitStoreFalse(t *testing.T) {
	var req GeminiChatRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"serviceTier":"priority",
		"store":false
	}`), &req))

	require.NotNil(t, req.ServiceTier)
	assert.Equal(t, GeminiServiceTierPriority, *req.ServiceTier)
	require.NotNil(t, req.Store)
	assert.False(t, *req.Store)

	encoded, err := common.Marshal(req)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, common.Unmarshal(encoded, &out))
	assert.Equal(t, "priority", out["serviceTier"])
	assert.Equal(t, false, out["store"])
}

func TestGeminiChatRequestAcceptsSnakeCaseServiceTier(t *testing.T) {
	var req GeminiChatRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"service_tier":"flex"
	}`), &req))

	require.NotNil(t, req.ServiceTier)
	assert.Equal(t, GeminiServiceTierFlex, *req.ServiceTier)
}
