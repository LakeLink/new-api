package cloudflare

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudflareCompletionPreservesExplicitZeroAndFalse(t *testing.T) {
	maxTokens := uint(0)
	stream := false
	temperature := 0.0
	converted := convertCf2CompletionsRequest(dto.GeneralOpenAIRequest{
		Prompt:      "hello",
		MaxTokens:   &maxTokens,
		Stream:      &stream,
		Temperature: &temperature,
	})

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	assert.Equal(t, float64(0), payload["max_tokens"])
	assert.Equal(t, false, payload["stream"])
	assert.Equal(t, float64(0), payload["temperature"])
}
