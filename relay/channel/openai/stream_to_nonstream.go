package openai

import (
	"bufio"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// OaiStreamToNonStreamHandler buffers an upstream Chat Completions SSE stream
// for clients that requested a regular JSON response. The request-side
// conversion is intentionally handled before the adaptor sends the request;
// this handler restores the original client-facing response contract.
func OaiStreamToNonStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	responseID := helper.GetResponseID(c)
	createdAt := time.Now().Unix()
	model := info.UpstreamModelName
	serviceTier := ""
	usage := &dto.Usage{}
	includeStreamUsage := false
	responseText := strings.Builder{}
	toolCount := 0
	lastStreamData := ""
	choices := make(map[int]*dto.OpenAITextResponseChoice)
	toolCallsByChoice := make(map[int]map[int]*dto.ToolCallResponse)
	seenToolCalls := make(map[string]struct{})
	var streamFunctionCallNames []string
	var streamErr *types.NewAPIError

	getChoice := func(index int) *dto.OpenAITextResponseChoice {
		choice, ok := choices[index]
		if !ok {
			choice = &dto.OpenAITextResponseChoice{Index: index}
			choices[index] = choice
		}
		return choice
	}

	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		data := strings.TrimSpace(scanner.Text())
		if data == "" || strings.HasPrefix(data, "event:") {
			continue
		}
		if !strings.HasPrefix(data, "data:") && !strings.HasPrefix(data, "[DONE]") {
			continue
		}
		if strings.HasPrefix(data, "data:") {
			data = strings.TrimSpace(data[5:])
		}
		if data == "" || strings.HasPrefix(data, "[DONE]") {
			break
		}

		info.SetFirstResponseTime()
		info.ReceivedResponseCount++
		lastStreamData = data

		var errorResponse dto.OpenAITextResponse
		if err := common.UnmarshalJsonStr(data, &errorResponse); err == nil {
			if oaiError := errorResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
				streamErr = types.WithOpenAIError(*oaiError, resp.StatusCode)
				break
			}
		}

		var streamResponse dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			break
		}
		if streamResponse.Id != "" {
			responseID = streamResponse.Id
		}
		if streamResponse.Created != 0 {
			createdAt = streamResponse.Created
		}
		if streamResponse.Model != "" {
			model = streamResponse.Model
		}
		if streamResponse.ServiceTier != "" {
			serviceTier = streamResponse.ServiceTier
		}
		if service.ValidUsage(streamResponse.Usage) {
			usage = streamResponse.Usage
			if usage.TotalTokens == 0 {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			includeStreamUsage = true
		}

		_ = ProcessStreamResponse(streamResponse, &responseText, &toolCount)
		collectStreamFunctionCallNames(data, seenToolCalls, &streamFunctionCallNames)
		for _, streamChoice := range streamResponse.Choices {
			choice := getChoice(streamChoice.Index)
			if streamChoice.FinishReason != nil {
				choice.FinishReason = *streamChoice.FinishReason
			}
			if content := streamChoice.Delta.GetContentString(); content != "" {
				choice.Message.SetStringContent(choice.Message.StringContent() + content)
			}
			if reasoning := streamChoice.Delta.GetReasoningContent(); reasoning != "" {
				reasoningContent := choice.Message.GetReasoningContent() + reasoning
				choice.Message.ReasoningContent = common.GetPointer(reasoningContent)
				choice.Message.Reasoning = nil
			}
			if len(streamChoice.Delta.ToolCalls) == 0 {
				continue
			}

			choice.Message.Role = "assistant"
			choiceToolCalls := toolCallsByChoice[streamChoice.Index]
			if choiceToolCalls == nil {
				choiceToolCalls = make(map[int]*dto.ToolCallResponse)
				toolCallsByChoice[streamChoice.Index] = choiceToolCalls
			}
			for position, deltaToolCall := range streamChoice.Delta.ToolCalls {
				toolIndex := position
				if deltaToolCall.Index != nil {
					toolIndex = *deltaToolCall.Index
				}
				toolCall := choiceToolCalls[toolIndex]
				if toolCall == nil {
					toolCall = &dto.ToolCallResponse{}
					choiceToolCalls[toolIndex] = toolCall
				}
				if deltaToolCall.ID != "" {
					toolCall.ID = deltaToolCall.ID
				}
				if deltaToolCall.Type != nil {
					toolCall.Type = deltaToolCall.Type
				}
				if deltaToolCall.Function.Name != "" {
					toolCall.Function.Name = deltaToolCall.Function.Name
				}
				if deltaToolCall.Function.Arguments != "" {
					toolCall.Function.Arguments += deltaToolCall.Function.Arguments
				}
			}
		}
	}
	if err := scanner.Err(); err != nil && streamErr == nil {
		streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if streamErr != nil {
		return nil, streamErr
	}

	if !includeStreamUsage {
		usage = service.ResponseText2Usage(c, responseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		usage.CompletionTokens += toolCount * 7
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	indexes := make([]int, 0, len(choices))
	for index := range choices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	responseChoices := make([]dto.OpenAITextResponseChoice, 0, len(indexes))
	for _, index := range indexes {
		choice := choices[index]
		choice.Message.Role = "assistant"
		if choice.Message.Content == nil {
			choice.Message.SetStringContent("")
		}
		if choice.FinishReason == "" {
			choice.FinishReason = constant.FinishReasonStop
		}
		if choice.FinishReason == constant.FinishReasonContentFilter {
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "openai_finish_reason=content_filter")
		}
		if choiceToolCalls := toolCallsByChoice[index]; len(choiceToolCalls) > 0 {
			toolIndexes := make([]int, 0, len(choiceToolCalls))
			for toolIndex := range choiceToolCalls {
				toolIndexes = append(toolIndexes, toolIndex)
			}
			sort.Ints(toolIndexes)
			toolCalls := make([]dto.ToolCallResponse, 0, len(toolIndexes))
			for _, toolIndex := range toolIndexes {
				if toolCall := choiceToolCalls[toolIndex]; toolCall != nil {
					toolCalls = append(toolCalls, *toolCall)
				}
			}
			choice.Message.SetToolCalls(toolCalls)
		}
		responseChoices = append(responseChoices, *choice)
	}
	if len(responseChoices) == 0 {
		responseChoices = append(responseChoices, dto.OpenAITextResponseChoice{
			Index:        0,
			FinishReason: constant.FinishReasonStop,
			Message:      dto.Message{Role: "assistant", Content: ""},
		})
	}

	for _, name := range streamFunctionCallNames {
		info.CountBillableToolCall(dto.BuildInCallFunctionCall, name)
	}
	applyUsagePostProcessing(info, usage, common.StringToByteSlice(lastStreamData))
	applyOpenAIUsagePricing(info, usage, serviceTier)

	chatResponse := &dto.OpenAITextResponse{
		Id:          responseID,
		Model:       model,
		Object:      "chat.completion",
		Created:     createdAt,
		ServiceTier: serviceTier,
		Choices:     responseChoices,
		Usage:       *usage,
	}
	responseValue := any(chatResponse)
	if info.RelayFormat != "" && info.RelayFormat != types.RelayFormatOpenAI {
		converted, err := relayconvert.ConvertResponse(c, info, info.RelayFormat, chatResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		responseValue = converted.Value
	}
	responseBody, err := common.Marshal(responseValue)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	c.Header("Content-Type", "application/json")
	service.IOCopyBytesGracefully(c, nil, responseBody)
	return usage, nil
}
