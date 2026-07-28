package deepseek

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAliasResolvesBeforeUpstreamRequest(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{Model: "deepseek-chat"}

	err := applyDeepSeekV4OpenAIThinkingSuffix(nil, request)

	require.NoError(t, err)
	assert.Equal(t, "deepseek-v4-flash", request.Model)
	assert.Empty(t, request.ReasoningEffort)
	var thinking map[string]string
	require.NoError(t, common.Unmarshal(request.THINKING, &thinking))
	assert.Equal(t, "disabled", thinking["type"])
}

func TestClaudeEffortSuffixResolvesBeforeUpstreamRequest(t *testing.T) {
	request := &dto.ClaudeRequest{Model: "deepseek-v4-pro-xhigh"}

	err := applyDeepSeekV4ClaudeThinkingSuffix(nil, request)

	require.NoError(t, err)
	assert.Equal(t, "deepseek-v4-pro", request.Model)
	require.NotNil(t, request.Thinking)
	assert.Equal(t, "enabled", request.Thinking.Type)
	var outputConfig map[string]string
	require.NoError(t, common.Unmarshal(request.OutputConfig, &outputConfig))
	assert.Equal(t, "max", outputConfig["effort"])
}
