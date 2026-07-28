package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIToClaudeConversionPreservesExplicitZeroReasoningBudget(t *testing.T) {
	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model:     "claude-sonnet-4",
		Reasoning: []byte(`{"enabled":true,"max_tokens":0}`),
	})
	require.NoError(t, err)
	require.NotNil(t, converted.Thinking)
	require.NotNil(t, converted.Thinking.BudgetTokens)
	assert.Zero(t, *converted.Thinking.BudgetTokens)
}

func TestOpenAIToClaudeConversionBoundsDefaultWebSearchContext(t *testing.T) {
	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model:            "claude-sonnet-4",
		WebSearchOptions: &dto.WebSearchOptions{},
	})
	require.NoError(t, err)
	tools, ok := converted.Tools.([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(*dto.ClaudeWebSearchTool)
	require.True(t, ok)
	require.NotNil(t, tool.MaxUses)
	assert.EqualValues(t, webSearchMaxUsesMedium, *tool.MaxUses)
}

func TestOpenAIToClaudeConversionKeepsParameterlessFunctionTool(t *testing.T) {
	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model: "claude-sonnet-4",
		Tools: []dto.ToolCallRequest{{
			Type: "function",
			Function: dto.FunctionRequest{
				Name: "ping",
			},
		}},
	})

	require.NoError(t, err)
	tools, ok := converted.Tools.([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(*dto.Tool)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"type": "object"}, tool.InputSchema)
}

func TestOpenAIToClaudeConversionRejectsMalformedFunctionSchemaWithoutPanic(t *testing.T) {
	tests := []struct {
		name       string
		parameters any
		errorText  string
	}{
		{
			name:       "non object parameters",
			parameters: []any{"invalid"},
			errorText:  "parameters must be a JSON object",
		},
		{
			name:       "non string schema type",
			parameters: map[string]any{"type": 1},
			errorText:  "parameters.type must be object",
		},
		{
			name:       "non object schema type",
			parameters: map[string]any{"type": "array"},
			errorText:  "parameters.type must be object",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				_, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
					Model: "claude-sonnet-4",
					Tools: []dto.ToolCallRequest{{
						Type: "function",
						Function: dto.FunctionRequest{
							Name:       "lookup",
							Parameters: test.parameters,
						},
					}},
				})
				require.ErrorContains(t, err, test.errorText)
			})
		})
	}
}

func TestOpenAIToClaudeConversionRejectsMalformedStopWithoutPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		_, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
			Model: "claude-sonnet-4",
			Stop:  []any{"valid", 1},
		})
		require.ErrorContains(t, err, "stop[1] must be a string")
	})
}
