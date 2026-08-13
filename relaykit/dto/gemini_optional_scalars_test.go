package dto

import (
	"testing"

	common "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiRequestScalarsPreserveExplicitZeroValues(t *testing.T) {
	var request GeminiChatRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello","thought":false}]}],
		"generationConfig":{"thinkingConfig":{"includeThoughts":false}}
	}`), &request))

	require.NotNil(t, request.GenerationConfig.ThinkingConfig)
	require.NotNil(t, request.GenerationConfig.ThinkingConfig.IncludeThoughts)
	assert.False(t, *request.GenerationConfig.ThinkingConfig.IncludeThoughts)
	require.Len(t, request.Contents, 1)
	require.Len(t, request.Contents[0].Parts, 1)
	require.NotNil(t, request.Contents[0].Parts[0].Thought)
	assert.False(t, *request.Contents[0].Parts[0].Thought)

	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"includeThoughts":false`)
	assert.Contains(t, string(encoded), `"thought":false`)
}

func TestGeminiRequestScalarsOmitAbsentValues(t *testing.T) {
	request := GeminiChatRequest{
		Contents: []GeminiChatContent{{
			Role:  "user",
			Parts: []GeminiPart{{Text: "hello"}},
		}},
		GenerationConfig: GeminiChatGenerationConfig{
			ThinkingConfig: &GeminiThinkingConfig{},
		},
	}

	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"includeThoughts"`)
	assert.NotContains(t, string(encoded), `"thought"`)
}

func TestGeminiEmbeddingPreservesExplicitZeroOutputDimensionality(t *testing.T) {
	var request GeminiEmbeddingRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"models/gemini-embedding-001",
		"content":{"parts":[{"text":"hello"}]},
		"outputDimensionality":0
	}`), &request))

	require.NotNil(t, request.OutputDimensionality)
	assert.Zero(t, *request.OutputDimensionality)

	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"outputDimensionality":0`)
}
