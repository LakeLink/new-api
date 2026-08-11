package claudemessages

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeToOpenAIConversionPreservesLegacyAndReasoningZeros(t *testing.T) {
	explicitZeroUint := uint(0)
	explicitZeroInt := 0
	request := dto.ClaudeRequest{
		Model:             "claude-sonnet-4",
		MaxTokensToSample: &explicitZeroUint,
		Thinking: &dto.Thinking{
			Type:         "enabled",
			BudgetTokens: &explicitZeroInt,
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenRouter},
	}

	converted, err := ClaudeMessagesRequestToOpenAIChat(request, info)
	require.NoError(t, err)
	require.NotNil(t, converted.MaxTokens)
	assert.Zero(t, *converted.MaxTokens)

	var reasoning map[string]any
	require.NoError(t, common.Unmarshal(converted.Reasoning, &reasoning))
	assert.Equal(t, float64(0), reasoning["max_tokens"])
}

func TestClaudeToOpenAIConversionMapsToolChoiceAndParallelSetting(t *testing.T) {
	disableParallel := false
	converted, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{
		ToolChoice: dto.ClaudeToolChoice{
			Type:                   "tool",
			Name:                   "lookup",
			DisableParallelToolUse: &disableParallel,
		},
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "lookup",
		},
	}, converted.ToolChoice)
	require.NotNil(t, converted.ParallelTooCalls)
	assert.True(t, *converted.ParallelTooCalls)
}

func TestClaudeToOpenAIConversionMapsToolChoiceModes(t *testing.T) {
	for _, test := range []struct {
		claudeType string
		openAIType string
	}{
		{claudeType: "auto", openAIType: "auto"},
		{claudeType: "any", openAIType: "required"},
		{claudeType: "none", openAIType: "none"},
	} {
		t.Run(test.claudeType, func(t *testing.T) {
			converted, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{
				ToolChoice: dto.ClaudeToolChoice{Type: test.claudeType},
			}, nil)

			require.NoError(t, err)
			assert.Equal(t, test.openAIType, converted.ToolChoice)
		})
	}
}

func TestClaudeToOpenAIConversionPreservesMixedContentThinkingAndToolResults(t *testing.T) {
	request := dto.ClaudeRequest{
		Model: "claude-sonnet-4",
		Messages: []dto.ClaudeMessage{
			{
				Role: "assistant",
				Content: []dto.ClaudeMediaMessage{
					{Type: "text", Text: common.GetPointer("I will check.")},
					{Type: "thinking", Thinking: common.GetPointer("plan")},
					{Type: "tool_use", Id: "call_1", Name: "lookup", Input: map[string]any{"q": "x"}},
				},
			},
			{
				Role: "user",
				Content: []dto.ClaudeMediaMessage{{
					Type:      "tool_result",
					ToolUseId: "call_1",
					Content:   "result",
				}},
			},
		},
	}

	converted, err := ClaudeMessagesRequestToOpenAIChat(request, nil)
	require.NoError(t, err)
	require.Len(t, converted.Messages, 2)

	assistant := converted.Messages[0]
	assert.Equal(t, "assistant", assistant.Role)
	assert.Equal(t, "I will check.", assistant.StringContent())
	assert.Equal(t, "plan", assistant.GetReasoningContent())
	toolCalls := assistant.ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "call_1", toolCalls[0].ID)

	toolMessage := converted.Messages[1]
	assert.Equal(t, "tool", toolMessage.Role)
	assert.Equal(t, "result", toolMessage.Content)
}

func TestClaudeToOpenAIConversionPreservesURLImagesAndStructuredOutput(t *testing.T) {
	converted, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{
		OutputConfig: []byte(`{"format":{"type":"json_schema","name":"answer","schema":{"type":"object"},"strict":true}}`),
		Messages: []dto.ClaudeMessage{{
			Role: "user",
			Content: []dto.ClaudeMediaMessage{{
				Type:   "image",
				Source: &dto.ClaudeMessageSource{Type: "url", Url: "https://example.test/image.png"},
			}},
		}},
	}, nil)

	require.NoError(t, err)
	parts := converted.Messages[0].ParseContent()
	require.Len(t, parts, 1)
	image := parts[0].GetImageMedia()
	require.NotNil(t, image)
	assert.Equal(t, "https://example.test/image.png", image.Url)
	assert.Equal(t, "json_schema", converted.ResponseFormat.Type)
	assert.JSONEq(t, `{"name":"response","schema":{"type":"object"},"strict":true}`, string(converted.ResponseFormat.JsonSchema))
}
