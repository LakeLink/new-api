package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func TestImagenCompatibleRequestPreservesImageCountForPreConsume(t *testing.T) {
	n := 4
	request := GeneralOpenAIRequest{Model: "imagen-4.0-generate-001", N: &n}

	meta := request.GetTokenCountMeta()

	assert.Equal(t, 4.0, meta.BillingRatios["n"])
}

func TestImagenCompatibleRequestPreservesExtraBodyCountForPreConsume(t *testing.T) {
	request := GeneralOpenAIRequest{
		Model:     "imagen-4.0-generate-001",
		ExtraBody: []byte(`{"n":4}`),
	}

	meta := request.GetTokenCountMeta()

	assert.Equal(t, 4.0, meta.BillingRatios["n"])
}

func TestRequestTokenMetadataSaturatesUnvalidatedOutputProducts(t *testing.T) {
	maxTokens := uint(common.MaxTokensLimit)
	candidateCount := 2

	openAI := GeneralOpenAIRequest{MaxTokens: &maxTokens, N: &candidateCount}
	assert.Equal(t, common.MaxTokensLimit, openAI.GetTokenCountMeta().MaxTokens)

	responses := OpenAIResponsesRequest{MaxOutputTokens: &maxTokens}
	assert.Equal(t, common.MaxTokensLimit, responses.GetTokenCountMeta().MaxTokens)

	gemini := GeminiChatRequest{
		GenerationConfig: GeminiChatGenerationConfig{
			MaxOutputTokens: &maxTokens,
			CandidateCount:  &candidateCount,
		},
	}
	assert.Equal(t, common.MaxTokensLimit, gemini.GetTokenCountMeta().MaxTokens)

	invalidCount := -1
	failSafe := GeneralOpenAIRequest{MaxTokens: &maxTokens, N: &invalidCount}
	assert.Equal(t, common.MaxTokensLimit, failSafe.GetTokenCountMeta().MaxTokens)
}

func TestClaudeTokenMetadataAccountsForLegacyCompletionLimit(t *testing.T) {
	current := uint(0)
	legacy := uint(4096)
	request := ClaudeRequest{
		MaxTokens:         &current,
		MaxTokensToSample: &legacy,
	}

	assert.Equal(t, 4096, request.GetTokenCountMeta().MaxTokens)
	assert.Equal(t, 4096, request.GetMaxTokenEstimate())
}
