package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	common "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func testClaudeDefaultMeta() convmeta.Meta {
	return &convmeta.Values{
		Options: &convmeta.Options{
			Claude: convmeta.ClaudeOptions{
				DefaultMaxTokens: func(string) int { return 4096 },
			},
		},
	}
}

func TestUsageFromResponsesUsagePreservesProviderBillingMetadata(t *testing.T) {
	ticks := int64(123456789)
	source := &dto.Usage{
		InputTokens:       11,
		OutputTokens:      7,
		TotalTokens:       18,
		CostInUSDTicks:    &ticks,
		ActualServiceTier: "priority",
		Cost:              map[string]interface{}{"total_cost": 0.018},
	}

	usage := UsageFromResponsesUsage(source)

	require.NotNil(t, usage.CostInUSDTicks)
	assert.Equal(t, ticks, *usage.CostInUSDTicks)
	assert.Equal(t, "priority", usage.ActualServiceTier)
	assert.Equal(t, source.Cost, usage.Cost)
}

func TestOpenAIResponsesRequestToClaudePreservesExplicitZeroControls(t *testing.T) {
	zeroFloat := 0.0
	zeroUint := uint(0)
	request, err := OpenAIResponsesRequestToClaudeMessages(nil, testClaudeDefaultMeta(), &dto.OpenAIResponsesRequest{
		Model:           "claude-sonnet-4-6",
		Input:           []byte(`"hello"`),
		TopP:            &zeroFloat,
		MaxOutputTokens: &zeroUint,
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

func TestOpenAIResponsesRequestToClaudeOpus5EffortAliasUsesAdaptiveThinking(t *testing.T) {
	request, err := OpenAIResponsesRequestToClaudeMessages(nil, testClaudeDefaultMeta(), &dto.OpenAIResponsesRequest{
		Model:       "claude-opus-5-xhigh",
		Input:       []byte(`"hello"`),
		Temperature: common.GetPointer(0.7),
		TopP:        common.GetPointer(0.9),
	})

	require.NoError(t, err)
	assert.Equal(t, "claude-opus-5", request.Model)
	assert.Nil(t, request.Temperature)
	assert.Nil(t, request.TopP)
	require.NotNil(t, request.Thinking)
	assert.Equal(t, "adaptive", request.Thinking.Type)
	assert.Equal(t, "summarized", request.Thinking.Display)
	assert.Nil(t, request.Thinking.BudgetTokens)
	assert.JSONEq(t, `{"effort":"xhigh"}`, string(request.OutputConfig))
}

func TestOpenAIResponsesRequestToClaudeOpus5ReasoningUsesAdaptiveThinking(t *testing.T) {
	request, err := OpenAIResponsesRequestToClaudeMessages(nil, testClaudeDefaultMeta(), &dto.OpenAIResponsesRequest{
		Model:     "claude-opus-5",
		Input:     []byte(`"hello"`),
		Reasoning: &dto.Reasoning{Effort: "max"},
	})

	require.NoError(t, err)
	require.NotNil(t, request.Thinking)
	assert.Equal(t, "adaptive", request.Thinking.Type)
	assert.Nil(t, request.Thinking.BudgetTokens)
	assert.JSONEq(t, `{"effort":"max"}`, string(request.OutputConfig))
}

func TestOpenAIResponsesRequestToClaudeOpus5NoneDisablesDefaultThinking(t *testing.T) {
	request, err := OpenAIResponsesRequestToClaudeMessages(nil, testClaudeDefaultMeta(), &dto.OpenAIResponsesRequest{
		Model:     "claude-opus-5-xhigh",
		Input:     []byte(`"hello"`),
		Reasoning: &dto.Reasoning{Effort: "none"},
	})

	require.NoError(t, err)
	assert.Equal(t, "claude-opus-5", request.Model)
	require.NotNil(t, request.Thinking)
	assert.Equal(t, "disabled", request.Thinking.Type)
	assert.Empty(t, request.OutputConfig)
}

func TestResponsesToChatConversionPreservesServiceTier(t *testing.T) {
	response := &dto.OpenAIResponsesResponse{
		ID:          "resp_1",
		Model:       "grok-4.5",
		ServiceTier: "priority",
		Usage:       &dto.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
	}

	chat, _, err := ResponsesResponseToChatCompletionsResponse(response, "chat_1")

	require.NoError(t, err)
	assert.Equal(t, "priority", chat.ServiceTier)
}

func TestResponsesStreamConversionPreservesServiceTier(t *testing.T) {
	state := NewResponsesToChatStreamState("grok-4.5", true)
	ticks := int64(123)
	event := &dto.ResponsesStreamResponse{
		Type: "response.completed",
		Response: &dto.OpenAIResponsesResponse{
			ID:          "resp_1",
			Model:       "grok-4.5",
			ServiceTier: "priority",
			Usage:       &dto.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2, CostInUSDTicks: &ticks},
		},
	}

	chunks, err := ResponsesStreamEventToChatChunks(event, state)

	require.NoError(t, err)
	require.NotEmpty(t, chunks)
	for _, chunk := range chunks {
		assert.Equal(t, "priority", chunk.ServiceTier)
	}
	require.NotNil(t, state.Usage.CostInUSDTicks)
	assert.Equal(t, ticks, *state.Usage.CostInUSDTicks)
	assert.Equal(t, "priority", state.Usage.ActualServiceTier)
}
