Failed to create stream fd: Operation not permitted
Failed to create stream fd: Operation not permitted
Failed to create stream fd: Operation not permitted
package openai

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/relayconvert"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type responsesBufferedScanResult struct {
	line string
	err  error
	done bool
}

func responsesBufferedContext(c *gin.Context, info *relaycommon.RelayInfo) context.Context {
	if info != nil && info.RelayCancelCtx != nil {
		return info.RelayCancelCtx
	}
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}

func responsesBufferedIdleTimeout() time.Duration {
	timeout := time.Duration(constant.StreamingTimeout) * time.Second
	if timeout <= 0 {
		return 300 * time.Second
	}
	return timeout
}

func responsesResponseHasContent(resp *dto.OpenAIResponsesResponse) bool {
	if resp == nil {
		return false
	}
	for _, output := range resp.Output {
		if output.Type == "function_call" || output.Type == "custom_tool_call" {
			return true
		}
		for _, content := range output.Content {
			if strings.TrimSpace(content.Text) != "" {
				return true
			}
		}
	}
	return false
}

func OaiResponsesToChatHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var responsesResp dto.OpenAIResponsesResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	if err := common.Unmarshal(body, &responsesResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if oaiError := responsesResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	chatId := helper.GetResponseID(c)
	chatResp, usage, err := service.ResponsesResponseToChatCompletionsResponse(&responsesResp, chatId)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if usage == nil || usage.TotalTokens == 0 {
		text := service.ExtractOutputTextFromResponses(&responsesResp)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		chatResp.Usage = *usage
	}

	var responseBody []byte
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		claudeResp := service.ResponseOpenAI2Claude(chatResp, info)
		responseBody, err = common.Marshal(claudeResp)
	case types.RelayFormatGemini:
		geminiResp := service.ResponseOpenAI2Gemini(chatResp, info)
		responseBody, err = common.Marshal(geminiResp)
	default:
		responseBody, err = common.Marshal(chatResp)
	}
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func OaiResponsesToChatBufferedStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	accumulator := relayconvert.NewResponsesBufferedAccumulator()
	var finalResponse *dto.OpenAIResponsesResponse
	var streamErr *types.NewAPIError

	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	ctx, cancel := context.WithCancel(responsesBufferedContext(c, info))
	defer cancel()

	scanResults := make(chan responsesBufferedScanResult, 1)
	go func() {
		for scanner.Scan() {
			select {
			case scanResults <- responsesBufferedScanResult{line: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		select {
		case scanResults <- responsesBufferedScanResult{err: scanner.Err(), done: true}:
		case <-ctx.Done():
		}
	}()

	idleTimer := time.NewTimer(responsesBufferedIdleTimeout())
	defer idleTimer.Stop()
	scanDone := false

streamLoop:
	for !scanDone && streamErr == nil && finalResponse == nil {
		select {
		case result := <-scanResults:
			if result.done {
				if result.err != nil {
					streamErr = types.NewOpenAIError(result.err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
				}
				scanDone = true
				continue
			}
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(responsesBufferedIdleTimeout())
			line := result.line
			if len(line) < 6 || line[:5] != "data:" {
				continue
			}
			data := strings.TrimSpace(line[5:])
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				break streamLoop
			}

			var streamResp dto.ResponsesStreamResponse
			if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
				logger.LogError(c, "failed to unmarshal buffered responses stream event: "+err.Error())
				streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
				continue
			}
			accumulator.ProcessEvent(&streamResp)
			switch streamResp.Type {
			case "response.completed", "response.done", "response.incomplete":
				finalResponse = streamResp.Response
				if streamResp.Type == "response.incomplete" {
					if finalResponse == nil {
						finalResponse = &dto.OpenAIResponsesResponse{}
					}
					if len(finalResponse.Status) == 0 {
						finalResponse.Status = []byte(`"incomplete"`)
					}
				}
			case "response.failed", "response.error":
				if streamResp.Response != nil {
					if oaiErr := streamResp.Response.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
						streamErr = types.WithOpenAIError(*oaiErr, http.StatusInternalServerError)
						continue
					}
				}
				streamErr = types.NewOpenAIError(fmt.Errorf("responses stream error: %s", streamResp.Type), types.ErrorCodeBadResponse, http.StatusInternalServerError)
			}
		case <-idleTimer.C:
			_ = resp.Body.Close()
			return nil, types.NewOpenAIError(fmt.Errorf("responses stream idle timeout"), types.ErrorCodeBadResponse, http.StatusGatewayTimeout)
		case <-ctx.Done():
			_ = resp.Body.Close()
			return nil, types.NewOpenAIError(ctx.Err(), types.ErrorCodeBadResponse, http.StatusGatewayTimeout)
		}
	}
	if streamErr != nil {
		return nil, streamErr
	}
	if finalResponse == nil {
		finalResponse = &dto.OpenAIResponsesResponse{
			ID:        helper.GetResponseID(c),
			CreatedAt: int(time.Now().Unix()),
			Model:     info.UpstreamModelName,
			Status:    []byte(`"completed"`),
		}
	}
	accumulator.SupplementResponseOutput(finalResponse)
	if !responsesResponseHasContent(finalResponse) {
		return nil, types.NewOpenAIError(fmt.Errorf("responses stream returned empty assistant response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}

	chatId := helper.GetResponseID(c)
	chatResp, usage, err := service.ResponsesResponseToChatCompletionsResponse(finalResponse, chatId)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if usage == nil || usage.TotalTokens == 0 {
		text := service.ExtractOutputTextFromResponses(finalResponse)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		chatResp.Usage = *usage
	}

	var responseBody []byte
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		claudeResp := service.ResponseOpenAI2Claude(chatResp, info)
		responseBody, err = common.Marshal(claudeResp)
	case types.RelayFormatGemini:
		geminiResp := service.ResponseOpenAI2Gemini(chatResp, info)
		responseBody, err = common.Marshal(geminiResp)
	default:
		responseBody, err = common.Marshal(chatResp)
	}
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func OaiResponsesToChatStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	responseId := helper.GetResponseID(c)
	createAt := time.Now().Unix()
	state := relayconvert.NewResponsesToChatStreamState(info.UpstreamModelName, false)
	state.ID = responseId
	state.Created = createAt
	streamErr := (*types.NewAPIError)(nil)

	if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo == nil {
		info.ClaudeConvertInfo = &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone}
	}

	sendChatChunk := func(chunk dto.ChatCompletionsStreamResponse) bool {
		if len(chunk.Choices) == 0 && chunk.Usage == nil {
			return true
		}
		if info.RelayFormat == types.RelayFormatOpenAI {
			if err := helper.ObjectData(c, &chunk); err != nil {
				streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
				return false
			}
			return true
		}

		chunkData, err := common.Marshal(&chunk)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		if err := HandleStreamFormat(c, info, string(chunkData), false, false); err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			return false
		}
		return true
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			sr.Stop(streamErr)
			return
		}

		var streamResp dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
			logger.LogError(c, "failed to unmarshal responses stream event: "+err.Error())
			sr.Error(err)
			return
		}

		if streamResp.Type == "response.error" || streamResp.Type == "response.failed" {
			if streamResp.Response != nil {
				if oaiErr := streamResp.Response.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
					streamErr = types.WithOpenAIError(*oaiErr, http.StatusInternalServerError)
					sr.Stop(streamErr)
					return
				}
			}
			streamErr = types.NewOpenAIError(fmt.Errorf("responses stream error: %s", streamResp.Type), types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}

		chunks, err := relayconvert.ResponsesStreamEventToChatChunks(&streamResp, state)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		for _, chunk := range chunks {
			if !sendChatChunk(chunk) {
				sr.Stop(streamErr)
				return
			}
		}
	})

	if streamErr != nil {
		return nil, streamErr
	}

	usage := state.Usage
	if usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		state.Usage = usage
	}

	if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo != nil {
		info.ClaudeConvertInfo.Usage = usage
	}
	for _, chunk := range relayconvert.FinalizeResponsesToChatStream(state) {
		if !sendChatChunk(chunk) {
			return nil, streamErr
		}
	}
	if info.RelayFormat == types.RelayFormatOpenAI && info.ShouldIncludeUsage && usage != nil {
		if err := helper.ObjectData(c, helper.GenerateFinalUsageResponse(responseId, state.Created, state.Model, *usage)); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
	}

	if info.RelayFormat == types.RelayFormatOpenAI {
		helper.Done(c)
	}
	return usage, nil
}
