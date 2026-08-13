package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesToClaudePreservesStrictToolsAndStructuredOutput(t *testing.T) {
	request, err := OpenAIResponsesRequestToClaudeMessages(nil, testClaudeDefaultMeta(), &dto.OpenAIResponsesRequest{
		Model: "claude-sonnet-4",
		Tools: mustRawMessage(t, []map[string]any{{
			"type":        "function",
			"name":        "lookup",
			"description": "look something up",
			"parameters":  map[string]any{"type": "object"},
			"strict":      true,
		}}),
		Text: mustRawMessage(t, map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "answer",
				"schema": map[string]any{"type": "object"},
				"strict": true,
			},
		}),
	})

	require.NoError(t, err)
	tools, ok := request.Tools.([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(*dto.Tool)
	require.True(t, ok)
	require.NotNil(t, tool.Strict)
	assert.True(t, *tool.Strict)
	assert.JSONEq(t, `{"format":{"schema":{"type":"object"},"type":"json_schema"}}`, string(request.OutputConfig))
}

func TestOpenAIResponsesToClaudePreservesFunctionCallArguments(t *testing.T) {
	request, err := OpenAIResponsesRequestToClaudeMessages(nil, testClaudeDefaultMeta(), &dto.OpenAIResponsesRequest{
		Model: "claude-sonnet-4",
		Input: mustRawMessage(t, []map[string]any{{
			"type":      "function_call",
			"call_id":   "call_1",
			"name":      "lookup",
			"arguments": `{"q":"x"}`,
		}}),
	})

	require.NoError(t, err)
	require.Len(t, request.Messages, 2)
	parts, err := request.Messages[1].ParseContent()
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.Equal(t, map[string]any{"q": "x"}, parts[0].Input)
}

func TestOpenAIResponsesToClaudeRejectsUnsupportedMedia(t *testing.T) {
	_, err := OpenAIResponsesRequestToClaudeMessages(nil, testClaudeDefaultMeta(), &dto.OpenAIResponsesRequest{
		Model: "claude-sonnet-4",
		Input: mustRawMessage(t, []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "input_audio",
				"input_audio": map[string]any{
					"data":   "abc",
					"format": "wav",
				},
			}},
		}}),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input_audio")
}
