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
