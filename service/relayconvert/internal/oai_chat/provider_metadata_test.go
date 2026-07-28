package oaichat

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUsageFromChatUsagePreservesProviderBillingMetadata(t *testing.T) {
	ticks := int64(123456789)
	source := &dto.Usage{
		PromptTokens:      11,
		CompletionTokens:  7,
		TotalTokens:       18,
		CostInUSDTicks:    &ticks,
		ActualServiceTier: "priority",
		Cost:              map[string]interface{}{"total_cost": 0.018},
	}

	usage := UsageFromChatUsage(source)

	require.NotNil(t, usage.CostInUSDTicks)
	assert.Equal(t, ticks, *usage.CostInUSDTicks)
	assert.Equal(t, "priority", usage.ActualServiceTier)
	assert.Equal(t, source.Cost, usage.Cost)
}

func TestOpenAIChatRequestToClaudePreservesExplicitZeroControls(t *testing.T) {
	zeroFloat := 0.0
	zeroUint := uint(0)
	request, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model:     "claude-sonnet-4-6",
		TopP:      &zeroFloat,
		MaxTokens: &zeroUint,
	})

	require.NoError(t, err)
	require.NotNil(t, request.TopP)
	assert.Zero(t, *request.TopP)
	require.NotNil(t, request.MaxTokens)
	assert.Zero(t, *request.MaxTokens)

	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(encoded, "top_p").Exists())
	assert.True(t, gjson.GetBytes(encoded, "max_tokens").Exists())
}

func TestOpenAIChatRequestToClaudeOpus5UsesCurrentProtocol(t *testing.T) {
	request, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model:           "claude-opus-5",
		Temperature:     common.GetPointer(0.7),
		TopP:            common.GetPointer(0.9),
		TopK:            common.GetPointer(40),
		ReasoningEffort: "max",
		Messages:        []dto.Message{{Role: "user", Content: "hello"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "claude-opus-5", request.Model)
	assert.Nil(t, request.Temperature)
	assert.Nil(t, request.TopP)
	assert.Nil(t, request.TopK)
	require.NotNil(t, request.Thinking)
	assert.Equal(t, "adaptive", request.Thinking.Type)
	assert.Equal(t, "summarized", request.Thinking.Display)
	assert.Nil(t, request.Thinking.BudgetTokens)
	assert.JSONEq(t, `{"effort":"max"}`, string(request.OutputConfig))
}

func TestOpenAIChatRequestToClaudeOpus5ConvertsLegacyReasoningBudget(t *testing.T) {
	request, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model:     "claude-opus-5",
		Reasoning: json.RawMessage(`{"max_tokens":8192}`),
		Messages:  []dto.Message{{Role: "user", Content: "hello"}},
	})

	require.NoError(t, err)
	require.NotNil(t, request.Thinking)
	assert.Equal(t, "adaptive", request.Thinking.Type)
	assert.Nil(t, request.Thinking.BudgetTokens)
	assert.JSONEq(t, `{"effort":"high"}`, string(request.OutputConfig))
}

func TestOpenAIChatRequestToClaudeOpus5NoneDisablesDefaultThinking(t *testing.T) {
	request, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model:           "claude-opus-5-max",
		ReasoningEffort: "none",
		Messages:        []dto.Message{{Role: "user", Content: "hello"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "claude-opus-5", request.Model)
	require.NotNil(t, request.Thinking)
	assert.Equal(t, "disabled", request.Thinking.Type)
	assert.Empty(t, request.OutputConfig)
}
