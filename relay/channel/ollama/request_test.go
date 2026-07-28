package ollama

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaRequestsAlwaysMaterializeStreamMode(t *testing.T) {
	for _, test := range []struct {
		name    string
		request any
		want    bool
	}{
		{name: "non-stream chat", request: &OllamaChatRequest{Model: "llama3", Stream: false}, want: false},
		{name: "stream chat", request: &OllamaChatRequest{Model: "llama3", Stream: true}, want: true},
		{name: "non-stream generate", request: &OllamaGenerateRequest{Model: "llama3", Stream: false}, want: false},
		{name: "stream generate", request: &OllamaGenerateRequest{Model: "llama3", Stream: true}, want: true},
		{name: "non-stream pull", request: &OllamaPullRequest{Name: "llama3", Stream: false}, want: false},
		{name: "stream pull", request: &OllamaPullRequest{Name: "llama3", Stream: true}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := common.Marshal(test.request)
			require.NoError(t, err)

			var payload map[string]any
			require.NoError(t, common.Unmarshal(encoded, &payload))
			stream, exists := payload["stream"]
			require.True(t, exists, "Ollama defaults to streaming when stream is omitted")
			assert.Equal(t, test.want, stream)
		})
	}
}

func TestOllamaEmbeddingPreservesExplicitZeroDimensions(t *testing.T) {
	dimensions := 0
	converted := requestOpenAI2Embeddings(dto.EmbeddingRequest{
		Model:      "nomic-embed-text",
		Input:      "hello",
		Dimensions: &dimensions,
	})

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	assert.Equal(t, float64(0), payload["dimensions"])
	options, ok := payload["options"].(map[string]any)
	if ok {
		assert.NotContains(t, options, "dimensions")
	}
}

func TestOllamaCompletionPreservesExplicitZeroMaxTokens(t *testing.T) {
	explicitZero := uint(0)
	converted, err := openAIChatToOllamaChat(nil, &dto.GeneralOpenAIRequest{
		Model:     "llama3",
		MaxTokens: &explicitZero,
	})
	require.NoError(t, err)

	assert.Equal(t, 0, converted.Options["num_predict"])
}

func TestOllamaRequestsPreserveExplicitLogprobControls(t *testing.T) {
	logProbs := true
	topLogProbs := 0
	request := &dto.GeneralOpenAIRequest{
		Model:       "llama3",
		LogProbs:    &logProbs,
		TopLogProbs: &topLogProbs,
	}

	chat, err := openAIChatToOllamaChat(nil, request)
	require.NoError(t, err)
	require.NotNil(t, chat.LogProbs)
	assert.True(t, *chat.LogProbs)
	require.NotNil(t, chat.TopLogProbs)
	assert.Zero(t, *chat.TopLogProbs)

	generate, err := openAIToGenerate(nil, request)
	require.NoError(t, err)
	require.NotNil(t, generate.LogProbs)
	assert.True(t, *generate.LogProbs)
	require.NotNil(t, generate.TopLogProbs)
	assert.Zero(t, *generate.TopLogProbs)

	encoded, err := common.Marshal(chat)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"logprobs":true`)
	assert.Contains(t, string(encoded), `"top_logprobs":0`)
}

func TestOllamaNativeRequestsRejectUnsupportedMultipleChoices(t *testing.T) {
	n := 2
	request := &dto.GeneralOpenAIRequest{Model: "llama3", N: &n}

	chat, err := openAIChatToOllamaChat(nil, request)
	require.ErrorContains(t, err, "exactly one choice")
	assert.Nil(t, chat)

	generate, err := openAIToGenerate(nil, request)
	require.ErrorContains(t, err, "exactly one choice")
	assert.Nil(t, generate)
}

func TestOllamaRequestsForwardIntegerSeedWithoutFloatConversion(t *testing.T) {
	seed := int64(4_294_967_296)

	chat, err := openAIChatToOllamaChat(nil, &dto.GeneralOpenAIRequest{
		Model: "llama3",
		Seed:  &seed,
	})
	require.NoError(t, err)
	assert.Equal(t, seed, chat.Options["seed"])

	embedding := requestOpenAI2Embeddings(dto.EmbeddingRequest{
		Model: "nomic-embed-text",
		Input: "hello",
		Seed:  &seed,
	})
	assert.Equal(t, seed, embedding.Options["seed"])
}

func TestOllamaToolResultsResolveToolNameFromOpenAICallID(t *testing.T) {
	toolCalls, err := common.Marshal([]dto.ToolCallRequest{
		{
			ID:   "call_weather",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "get_weather",
				Arguments: `{"city":"Paris"}`,
			},
		},
	})
	require.NoError(t, err)

	converted, err := openAIChatToOllamaChat(nil, &dto.GeneralOpenAIRequest{
		Model: "llama3",
		Messages: []dto.Message{
			{Role: "assistant", ToolCalls: toolCalls},
			{Role: "tool", ToolCallId: "call_weather", Content: `{"temperature":18}`},
		},
	})
	require.NoError(t, err)
	require.Len(t, converted.Messages, 2)
	assert.Equal(t, "get_weather", converted.Messages[1].ToolName)
}

func TestOllamaEmbeddingRejectsUnsupportedBase64Encoding(t *testing.T) {
	_, err := (&Adaptor{}).ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{
		Model:          "nomic-embed-text",
		Input:          "hello",
		EncodingFormat: "base64",
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, `encoding format "base64"`)
}

func TestOllamaRerankFailsBeforeDispatch(t *testing.T) {
	converted, err := (&Adaptor{}).ConvertRerankRequest(nil, 0, dto.RerankRequest{
		Model: "llama3",
		Query: "query",
	})

	require.Error(t, err)
	assert.Nil(t, converted)
}
