package geminichat

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiResponseConversionsExposeActualServiceTier(t *testing.T) {
	response := &dto.GeminiChatResponse{
		HasUsageMetadata: true,
		UsageMetadata: dto.GeminiUsageMetadata{
			ServiceTier: dto.GeminiServiceTierPriority,
		},
	}

	nonStream := ResponseGeminiChat2OpenAI("id", 1, response)
	assert.Equal(t, dto.GeminiServiceTierPriority, nonStream.ServiceTier)

	stream, _ := StreamResponseGeminiChat2OpenAI(response)
	require.NotNil(t, stream)
	assert.Equal(t, dto.GeminiServiceTierPriority, stream.ServiceTier)
}
