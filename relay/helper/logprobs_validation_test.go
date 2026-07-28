package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/require"
)

func validOpenAIProtocolRequest() *dto.GeneralOpenAIRequest {
	return &dto.GeneralOpenAIRequest{
		Model:    "gpt-4.1",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}
}

func TestValidateTextRequestLogprobsContract(t *testing.T) {
	request := validOpenAIProtocolRequest()
	request.TopLogProbs = common.GetPointer(-1)
	require.ErrorContains(t, ValidateTextRequest(request, relayconstant.RelayModeChatCompletions), "between 0 and 20")

	request = validOpenAIProtocolRequest()
	request.TopLogProbs = common.GetPointer(21)
	require.ErrorContains(t, ValidateTextRequest(request, relayconstant.RelayModeChatCompletions), "between 0 and 20")

	request = validOpenAIProtocolRequest()
	request.TopLogProbs = common.GetPointer(0)
	request.LogProbs = common.GetPointer(false)
	require.ErrorContains(t, ValidateTextRequest(request, relayconstant.RelayModeChatCompletions), "logprobs must be true")

	request = validOpenAIProtocolRequest()
	request.TopLogProbs = common.GetPointer(0)
	request.LogProbs = common.GetPointer(true)
	require.NoError(t, ValidateTextRequest(request, relayconstant.RelayModeChatCompletions))
}

func TestValidateTextRequestStopAndFunctionSchemaContract(t *testing.T) {
	request := validOpenAIProtocolRequest()
	request.Stop = []any{"valid", 1}
	require.ErrorContains(t, ValidateTextRequest(request, relayconstant.RelayModeChatCompletions), "stop[1]")

	request = validOpenAIProtocolRequest()
	request.Stop = []string{"1", "2", "3", "4", "5"}
	require.ErrorContains(t, ValidateTextRequest(request, relayconstant.RelayModeChatCompletions), "at most 4")

	request = validOpenAIProtocolRequest()
	request.Tools = []dto.ToolCallRequest{{
		Type: "function",
		Function: dto.FunctionRequest{
			Name:       "lookup",
			Parameters: []any{"invalid"},
		},
	}}
	require.ErrorContains(t, ValidateTextRequest(request, relayconstant.RelayModeChatCompletions), "must be a JSON object")

	request = validOpenAIProtocolRequest()
	request.Tools = []dto.ToolCallRequest{{
		Type: "function",
		Function: dto.FunctionRequest{
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		},
	}}
	require.NoError(t, ValidateTextRequest(request, relayconstant.RelayModeChatCompletions))
}

func TestValidateGeminiRequestLogprobsContract(t *testing.T) {
	validRequest := func() *dto.GeminiChatRequest {
		return &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{{
				Parts: []dto.GeminiPart{{Text: "hello"}},
			}},
		}
	}

	request := validRequest()
	request.GenerationConfig.Logprobs = common.GetPointer(int32(21))
	request.GenerationConfig.ResponseLogprobs = common.GetPointer(true)
	require.ErrorContains(t, ValidateGeminiRequest(request), "between 0 and 20")

	request = validRequest()
	request.GenerationConfig.Logprobs = common.GetPointer(int32(0))
	request.GenerationConfig.ResponseLogprobs = common.GetPointer(false)
	require.ErrorContains(t, ValidateGeminiRequest(request), "responseLogprobs must be true")

	request = validRequest()
	request.GenerationConfig.Logprobs = common.GetPointer(int32(0))
	request.GenerationConfig.ResponseLogprobs = common.GetPointer(true)
	require.NoError(t, ValidateGeminiRequest(request))
}
