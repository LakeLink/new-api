package openai

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	if responsesResponse.HasImageGenerationCall() {
		c.Set("image_generation_call", true)
		c.Set("image_generation_call_quality", responsesResponse.GetQuality())
		c.Set("image_generation_call_size", responsesResponse.GetSize())
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := dto.Usage{}
	if responsesResponse.Usage != nil {
		usage = *responsesResponse.Usage
		usage.PromptTokens = responsesResponse.Usage.InputTokens
		usage.CompletionTokens = responsesResponse.Usage.OutputTokens
		usage.TotalTokens = responsesResponse.Usage.TotalTokens
		if responsesResponse.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = responsesResponse.Usage.InputTokensDetails.CachedTokens
			usage.PromptTokensDetails.CacheWriteTokens = responsesResponse.Usage.InputTokensDetails.CacheWriteTokens
		}
	}
	applyOpenAIUsagePricing(info, &usage, responsesResponse.ServiceTier)
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return &usage, nil
	}
	// The response.tools field echoes tool definitions. Bill only actual
	// built-in calls represented by output items.
	for index := range responsesResponse.Output {
		recordResponsesBuiltInToolCall(c, info, &responsesResponse.Output[index])
	}
	return &usage, nil
}

func recordResponsesBuiltInToolCall(c *gin.Context, info *relaycommon.RelayInfo, output *dto.ResponsesOutput) {
	if output == nil {
		return
	}
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return
	}

	var tool *relaycommon.BuildInToolInfo
	switch output.Type {
	case dto.BuildInCallWebSearchCall:
		_, configuredTool, err := info.ResponsesUsageInfo.WebSearchTool()
		if err != nil {
			logger.LogError(c, "invalid Responses web-search billing configuration: "+err.Error())
			return
		}
		tool = configuredTool
	case dto.BuildInCallFileSearchCall:
		tool = info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch]
	case dto.ResponsesOutputTypeImageGenerationCall:
		tool = info.ResponsesUsageInfo.BuiltInTools["image_generation"]
		if tool == nil {
			// An image output is itself authoritative billable usage even if a
			// compatible provider failed to echo or preserve the configured
			// tool definition.
			tool = &relaycommon.BuildInToolInfo{
				ToolName:     "image_generation",
				ImageModel:   "gpt-image-1",
				ImageQuality: "auto",
				ImageSize:    "auto",
			}
			info.ResponsesUsageInfo.BuiltInTools["image_generation"] = tool
		}
		c.Set("image_generation_call", true)
		if ctxQuality := c.GetString("image_generation_call_quality"); ctxQuality == "" {
			c.Set("image_generation_call_quality", output.Quality)
			c.Set("image_generation_call_size", output.Size)
		}
	default:
		return
	}

	if tool == nil {
		logger.LogError(c, fmt.Sprintf("BuiltInTools not found for output call type: %s", output.Type))
		return
	}
	if output.ID != "" {
		if info.ResponsesUsageInfo.SeenOutputItems == nil {
			info.ResponsesUsageInfo.SeenOutputItems = make(map[string]struct{})
		}
		outputKey := output.Type + ":" + output.ID
		if _, seen := info.ResponsesUsageInfo.SeenOutputItems[outputKey]; seen {
			return
		}
		info.ResponsesUsageInfo.SeenOutputItems[outputKey] = struct{}{}
	}
	tool.CallCount++
	if output.Type == dto.ResponsesOutputTypeImageGenerationCall {
		tool.ImageOutputs = append(tool.ImageOutputs, relaycommon.ImageGenerationOutputInfo{
			Quality: output.Quality,
			Size:    output.Size,
		})
	}
}

func reconcileResponsesBuiltInToolCalls(c *gin.Context, info *relaycommon.RelayInfo, outputs []dto.ResponsesOutput) {
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return
	}
	finalCounts := make(map[*relaycommon.BuildInToolInfo]int)
	finalImageOutputs := make([]relaycommon.ImageGenerationOutputInfo, 0)
	seenFinalImageIDs := make(map[string]struct{})
	seenFinalOutputIDs := make(map[string]struct{})
	for _, output := range outputs {
		var tool *relaycommon.BuildInToolInfo
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			_, configuredTool, err := info.ResponsesUsageInfo.WebSearchTool()
			if err != nil {
				logger.LogError(c, "invalid Responses web-search billing configuration: "+err.Error())
				continue
			}
			tool = configuredTool
		case dto.BuildInCallFileSearchCall:
			tool = info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch]
		case dto.ResponsesOutputTypeImageGenerationCall:
			tool = info.ResponsesUsageInfo.BuiltInTools["image_generation"]
			if tool == nil {
				tool = &relaycommon.BuildInToolInfo{
					ToolName:     "image_generation",
					ImageModel:   "gpt-image-1",
					ImageQuality: "auto",
					ImageSize:    "auto",
				}
				info.ResponsesUsageInfo.BuiltInTools["image_generation"] = tool
			}
			if output.ID == "" {
				finalImageOutputs = append(finalImageOutputs, relaycommon.ImageGenerationOutputInfo{
					Quality: output.Quality,
					Size:    output.Size,
				})
			} else if _, exists := seenFinalImageIDs[output.ID]; !exists {
				seenFinalImageIDs[output.ID] = struct{}{}
				finalImageOutputs = append(finalImageOutputs, relaycommon.ImageGenerationOutputInfo{
					Quality: output.Quality,
					Size:    output.Size,
				})
			}
		}
		if tool != nil {
			if output.ID != "" {
				key := output.Type + ":" + output.ID
				if _, exists := seenFinalOutputIDs[key]; exists {
					continue
				}
				seenFinalOutputIDs[key] = struct{}{}
			}
			finalCounts[tool]++
		}
	}
	for tool, count := range finalCounts {
		if count > tool.CallCount {
			tool.CallCount = count
		}
	}
	if imageTool := info.ResponsesUsageInfo.BuiltInTools["image_generation"]; imageTool != nil &&
		len(finalImageOutputs) >= len(imageTool.ImageOutputs) {
		imageTool.ImageOutputs = finalImageOutputs
	}
}

func recordResponsesImagePartial(c *gin.Context, info *relaycommon.RelayInfo, event *dto.ResponsesStreamResponse) {
	if info == nil || info.ResponsesUsageInfo == nil || event == nil {
		return
	}
	tool := info.ResponsesUsageInfo.BuiltInTools["image_generation"]
	if tool == nil || tool.ImagePartialImages == 0 {
		return
	}
	if event.ItemID == "" || event.PartialImageIndex == nil ||
		*event.PartialImageIndex < 0 || *event.PartialImageIndex >= tool.ImagePartialImages {
		logger.LogError(c, "invalid Responses image-generation partial-image event")
		return
	}
	if info.ResponsesUsageInfo.SeenPartialImages == nil {
		info.ResponsesUsageInfo.SeenPartialImages = make(map[string]struct{})
	}
	key := fmt.Sprintf("%s:%d", event.ItemID, *event.PartialImageIndex)
	if _, seen := info.ResponsesUsageInfo.SeenPartialImages[key]; seen {
		return
	}
	maxCalls := uint(common.MaxTextToolCallCount)
	if info.ResponsesUsageInfo.MaxToolCalls != nil {
		maxCalls = *info.ResponsesUsageInfo.MaxToolCalls
	}
	maxPartials := uint64(tool.ImagePartialImages) * uint64(maxCalls)
	if uint64(tool.ImagePartialCount) >= maxPartials {
		logger.LogError(c, "Responses image-generation partial-image count exceeds configured limit")
		return
	}
	info.ResponsesUsageInfo.SeenPartialImages[key] = struct{}{}
	tool.ImagePartialCount++
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}
		sendResponsesStreamData(c, streamResponse, data)
		switch streamResponse.Type {
		case "response.completed", "response.incomplete":
			if streamResponse.Response != nil {
				if streamResponse.Response.Usage != nil {
					*usage = *streamResponse.Response.Usage
					usage.PromptTokens = streamResponse.Response.Usage.InputTokens
					usage.CompletionTokens = streamResponse.Response.Usage.OutputTokens
					usage.TotalTokens = streamResponse.Response.Usage.TotalTokens
					if streamResponse.Response.Usage.InputTokensDetails != nil {
						usage.PromptTokensDetails.CachedTokens = streamResponse.Response.Usage.InputTokensDetails.CachedTokens
						usage.PromptTokensDetails.CacheWriteTokens = streamResponse.Response.Usage.InputTokensDetails.CacheWriteTokens
					}
				}
				if streamResponse.Response.HasImageGenerationCall() {
					c.Set("image_generation_call", true)
					c.Set("image_generation_call_quality", streamResponse.Response.GetQuality())
					c.Set("image_generation_call_size", streamResponse.Response.GetSize())
				}
				reconcileResponsesBuiltInToolCalls(c, info, streamResponse.Response.Output)
				applyOpenAIUsagePricing(info, usage, streamResponse.Response.ServiceTier)
			}
		case "response.output_text.delta":
			// 处理输出文本
			responseTextBuilder.WriteString(streamResponse.Delta)
		case dto.ResponsesOutputTypeItemDone:
			if streamResponse.Item != nil {
				recordResponsesBuiltInToolCall(c, info, streamResponse.Item)
			}
		case "response.image_generation_call.partial_image":
			recordResponsesImagePartial(c, info, &streamResponse)
		}
	})

	if usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := service.CountTextToken(tempStr, info.UpstreamModelName)
			usage.CompletionTokens = completionTokens
		}
	}

	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	return usage, nil
}
