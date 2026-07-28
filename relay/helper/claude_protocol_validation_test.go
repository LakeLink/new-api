package helper

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validClaudeProtocolRequest(model string) *dto.ClaudeRequest {
	return &dto.ClaudeRequest{
		Model:    model,
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	}
}

func TestValidateClaudeCurrentModelsRejectUnsupportedSampling(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*dto.ClaudeRequest)
	}{
		{
			name: "temperature",
			mutate: func(request *dto.ClaudeRequest) {
				request.Temperature = common.GetPointer(0.7)
			},
		},
		{
			name: "top_p",
			mutate: func(request *dto.ClaudeRequest) {
				request.TopP = common.GetPointer(0.9)
			},
		},
		{
			name: "top_k",
			mutate: func(request *dto.ClaudeRequest) {
				request.TopK = common.GetPointer(40)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validClaudeProtocolRequest("claude-opus-5")
			test.mutate(request)
			require.Error(t, ValidateClaudeRequest(request))
		})
	}

	request := validClaudeProtocolRequest("claude-opus-5")
	request.Temperature = common.GetPointer(1.0)
	request.TopP = common.GetPointer(0.99)
	require.NoError(t, ValidateClaudeRequest(request))
}

func TestValidateClaudeCurrentThinkingModes(t *testing.T) {
	for _, model := range []string{
		"claude-opus-4-8",
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-fable-5",
	} {
		t.Run(model+"/manual", func(t *testing.T) {
			request := validClaudeProtocolRequest(model)
			request.Thinking = &dto.Thinking{Type: "enabled", BudgetTokens: common.GetPointer(2048)}
			require.ErrorContains(t, ValidateClaudeRequest(request), "manual thinking budgets")
		})
	}

	fable := validClaudeProtocolRequest("claude-fable-5")
	fable.Thinking = &dto.Thinking{Type: "disabled"}
	require.ErrorContains(t, ValidateClaudeRequest(fable), "cannot be disabled")
}

func TestValidateClaudeOpus5ThinkingDisabledEffortConstraint(t *testing.T) {
	for _, effort := range []string{"xhigh", "max"} {
		t.Run(effort, func(t *testing.T) {
			request := validClaudeProtocolRequest("claude-opus-5")
			request.Thinking = &dto.Thinking{Type: "disabled"}
			request.OutputConfig = json.RawMessage(`{"effort":"` + effort + `"}`)
			require.ErrorContains(t, ValidateClaudeRequest(request), "cannot disable thinking")
		})
	}

	request := validClaudeProtocolRequest("claude-opus-5")
	request.Thinking = &dto.Thinking{Type: "disabled"}
	request.OutputConfig = json.RawMessage(`{"effort":"high"}`)
	require.NoError(t, ValidateClaudeRequest(request))

	request.OutputConfig = json.RawMessage(`{"effort":"minimal"}`)
	require.ErrorContains(t, ValidateClaudeRequest(request), "effort is invalid")
}

func TestValidateClaudeEffortAliasDefersToMaterializedRequest(t *testing.T) {
	request := validClaudeProtocolRequest("claude-opus-5-xhigh")
	request.Temperature = common.GetPointer(0.7)
	request.TopP = common.GetPointer(0.9)
	request.TopK = common.GetPointer(40)
	request.Thinking = &dto.Thinking{Type: "enabled", BudgetTokens: common.GetPointer(2048)}

	assert.NoError(t, ValidateClaudeRequest(request))
}

func TestValidateClaudeToolChoiceContract(t *testing.T) {
	request := validClaudeProtocolRequest("claude-sonnet-4")
	request.ToolChoice = dto.ClaudeToolChoice{Type: "tool"}
	require.ErrorContains(t, ValidateClaudeRequest(request), "name is required")

	request.ToolChoice = dto.ClaudeToolChoice{Type: "unsupported"}
	require.ErrorContains(t, ValidateClaudeRequest(request), "must be one of")

	request.ToolChoice = dto.ClaudeToolChoice{
		Type:                   "none",
		DisableParallelToolUse: common.GetPointer(false),
	}
	require.ErrorContains(t, ValidateClaudeRequest(request), "does not accept")

	request.ToolChoice = dto.ClaudeToolChoice{
		Type:                   "auto",
		DisableParallelToolUse: common.GetPointer(false),
	}
	require.NoError(t, ValidateClaudeRequest(request))
}
