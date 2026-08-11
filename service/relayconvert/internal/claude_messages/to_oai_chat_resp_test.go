package claudemessages

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseClaude2OpenAIConcatenatesTextAndThinkingBlocks(t *testing.T) {
	response := ResponseClaude2OpenAI(&dto.ClaudeResponse{
		Id:    "msg_1",
		Model: "claude-sonnet-4",
		Content: []dto.ClaudeMediaMessage{
			{Type: "text", Text: ptr("first")},
			{Type: "thinking", Thinking: ptr("plan")},
			{Type: "text", Text: ptr("second")},
			{Type: "tool_use", Id: "call_1", Name: "lookup", Input: map[string]any{"q": "x"}},
		},
		StopReason: "tool_use",
	})

	require.Len(t, response.Choices, 1)
	choice := response.Choices[0]
	assert.Equal(t, "firstsecond", choice.Message.StringContent())
	assert.Equal(t, "plan", choice.Message.GetReasoningContent())
	toolCalls := choice.Message.ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, `{"q":"x"}`, toolCalls[0].Function.Arguments)
	assert.Equal(t, "tool_calls", choice.FinishReason)
}

func TestStreamResponseClaude2OpenAIUsesToolOrdinalAndIgnoresSignature(t *testing.T) {
	info := &ClaudeResponseInfo{}
	textIndex := 0
	toolIndex := 1

	textChunk := StreamResponseClaude2OpenAIWithInfo(&dto.ClaudeResponse{
		Type:  "content_block_start",
		Index: &textIndex,
		ContentBlock: &dto.ClaudeMediaMessage{
			Type: "text",
			Text: ptr("hello"),
		},
	}, info)
	require.Len(t, textChunk.Choices, 1)

	toolChunk := StreamResponseClaude2OpenAIWithInfo(&dto.ClaudeResponse{
		Type:  "content_block_start",
		Index: &toolIndex,
		ContentBlock: &dto.ClaudeMediaMessage{
			Type: "tool_use",
			Id:   "call_1",
			Name: "lookup",
		},
	}, info)
	require.Len(t, toolChunk.Choices, 1)
	require.Len(t, toolChunk.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, 0, *toolChunk.Choices[0].Delta.ToolCalls[0].Index)

	partial := `{"q":"x"}`
	argsChunk := StreamResponseClaude2OpenAIWithInfo(&dto.ClaudeResponse{
		Type:  "content_block_delta",
		Index: &toolIndex,
		Delta: &dto.ClaudeMediaMessage{Type: "input_json_delta", PartialJson: &partial},
	}, info)
	require.Len(t, argsChunk.Choices, 1)
	require.Len(t, argsChunk.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, 0, *argsChunk.Choices[0].Delta.ToolCalls[0].Index)

	signature := StreamResponseClaude2OpenAIWithInfo(&dto.ClaudeResponse{
		Type:  "content_block_delta",
		Index: &toolIndex,
		Delta: &dto.ClaudeMediaMessage{Type: "signature_delta", Delta: "signature"},
	}, info)
	assert.Nil(t, signature)
}

func ptr[T any](value T) *T {
	return &value
}
