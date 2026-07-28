package aws

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relay/reasonmap"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"

	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockruntimeTypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go/auth/bearer"
)

// getAwsErrorStatusCode extracts HTTP status code from AWS SDK error
func getAwsErrorStatusCode(err error) int {
	// Check for HTTP response error which contains status code
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) {
		return httpErr.HTTPStatusCode()
	}
	// Default to 500 if we can't determine the status code
	return http.StatusInternalServerError
}

func newAwsInvokeContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if common.RelayTimeout == 0 {
		return context.WithCancel(parent)
	}
	timeout := common.SafeIntervalDuration(
		common.RelayTimeout,
		time.Second,
		60*time.Second,
		"AWS relay request timeout",
	)
	return context.WithTimeout(parent, timeout)
}

func newAwsClient(c *gin.Context, info *relaycommon.RelayInfo) (*bedrockruntime.Client, error) {
	var (
		httpClient *http.Client
		err        error
	)
	if info.ChannelSetting.Proxy != "" {
		httpClient, err = service.NewProxyHttpClient(info.ChannelSetting.Proxy)
		if err != nil {
			return nil, fmt.Errorf("new proxy http client failed: %w", err)
		}
	} else {
		httpClient = service.GetHttpClient()
	}

	awsSecret := strings.Split(info.ApiKey, "|")
	var client *bedrockruntime.Client
	switch len(awsSecret) {
	case 2:
		apiKey := awsSecret[0]
		region := awsSecret[1]
		client = bedrockruntime.New(bedrockruntime.Options{
			Region:                  region,
			BearerAuthTokenProvider: bearer.StaticTokenProvider{Token: bearer.Token{Value: apiKey}},
			HTTPClient:              httpClient,
		})
	case 3:
		ak := awsSecret[0]
		sk := awsSecret[1]
		region := awsSecret[2]
		client = bedrockruntime.New(bedrockruntime.Options{
			Region:      region,
			Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(ak, sk, "")),
			HTTPClient:  httpClient,
		})
	default:
		return nil, errors.New("invalid aws secret key")
	}

	return client, nil
}

func doAwsClientRequest(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor, requestBody io.Reader) (any, error) {
	awsCli, err := newAwsClient(c, info)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelAwsClientError)
	}
	a.AwsClient = awsCli

	// 获取对应的AWS模型ID
	awsModelId := getAwsModelID(info.UpstreamModelName)

	awsRegionPrefix := getAwsRegionPrefix(awsCli.Options().Region)
	canCrossRegion := awsModelCanCrossRegion(awsModelId, awsRegionPrefix)
	if canCrossRegion {
		awsModelId = awsModelCrossRegion(awsModelId, awsRegionPrefix)
	}

	// init empty request.header
	requestHeader := http.Header{}
	a.SetupRequestHeader(c, &requestHeader, info)
	headerOverride, err := channel.ResolveHeaderOverride(info, c)
	if err != nil {
		return nil, err
	}
	for key, value := range headerOverride {
		requestHeader.Set(key, value)
	}

	if isNovaModel(awsModelId) {
		if !isNovaTextModel(awsModelId) {
			return nil, types.NewError(
				fmt.Errorf("AWS Nova model %q is not supported by the chat adapter", awsModelId),
				types.ErrorCodeInvalidRequest,
			)
		}
		var novaReq *NovaRequest
		err = common.DecodeJson(requestBody, &novaReq)
		if err != nil {
			return nil, types.NewError(errors.Wrap(err, "decode nova request fail"), types.ErrorCodeBadRequestBody)
		}

		reqBody, err := common.Marshal(novaReq)
		if err != nil {
			return nil, types.NewError(errors.Wrap(err, "marshal nova request"), types.ErrorCodeBadResponseBody)
		}
		if info.IsStream {
			a.AwsReq = &bedrockruntime.InvokeModelWithResponseStreamInput{
				ModelId:     aws.String(awsModelId),
				Accept:      aws.String("application/json"),
				ContentType: aws.String("application/json"),
				Body:        reqBody,
			}
		} else {
			a.AwsReq = &bedrockruntime.InvokeModelInput{
				ModelId:     aws.String(awsModelId),
				Accept:      aws.String("application/json"),
				ContentType: aws.String("application/json"),
				Body:        reqBody,
			}
		}
		return nil, nil
	} else {
		awsClaudeReq, err := formatRequest(requestBody, requestHeader)
		if err != nil {
			return nil, types.NewError(errors.Wrap(err, "format aws request fail"), types.ErrorCodeBadRequestBody)
		}

		if info.IsStream {
			awsReq := &bedrockruntime.InvokeModelWithResponseStreamInput{
				ModelId:     aws.String(awsModelId),
				Accept:      aws.String("application/json"),
				ContentType: aws.String("application/json"),
			}
			awsReq.Body, err = buildAwsRequestBody(c, info, awsClaudeReq)
			if err != nil {
				return nil, types.NewError(errors.Wrap(err, "marshal aws request fail"), types.ErrorCodeBadRequestBody)
			}
			a.AwsReq = awsReq
			return nil, nil
		} else {
			awsReq := &bedrockruntime.InvokeModelInput{
				ModelId:     aws.String(awsModelId),
				Accept:      aws.String("application/json"),
				ContentType: aws.String("application/json"),
			}
			awsReq.Body, err = buildAwsRequestBody(c, info, awsClaudeReq)
			if err != nil {
				return nil, types.NewError(errors.Wrap(err, "marshal aws request fail"), types.ErrorCodeBadRequestBody)
			}
			a.AwsReq = awsReq
			return nil, nil
		}
	}
}

// buildAwsRequestBody prepares the payload for AWS requests, applying passthrough rules when enabled.
func buildAwsRequestBody(c *gin.Context, info *relaycommon.RelayInfo, awsClaudeReq any) ([]byte, error) {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, errors.Wrap(err, "get request body for pass-through fail")
		}
		body, err := storage.Bytes()
		if err != nil {
			return nil, errors.Wrap(err, "get request body bytes fail")
		}
		var data map[string]interface{}
		if err := common.Unmarshal(body, &data); err != nil {
			return nil, errors.Wrap(err, "pass-through unmarshal request body fail")
		}
		delete(data, "model")
		delete(data, "stream")
		return common.Marshal(data)
	}
	return common.Marshal(awsClaudeReq)
}

func getAwsRegionPrefix(awsRegionId string) string {
	parts := strings.Split(awsRegionId, "-")
	regionPrefix := ""
	if len(parts) > 0 {
		regionPrefix = parts[0]
	}
	return regionPrefix
}

func awsModelCanCrossRegion(awsModelId, awsRegionPrefix string) bool {
	regionSet, exists := awsModelCanCrossRegionMap[awsModelId]
	return exists && regionSet[awsRegionPrefix]
}

func awsModelCrossRegion(awsModelId, awsRegionPrefix string) string {
	modelPrefix, find := awsRegionCrossModelPrefixMap[awsRegionPrefix]
	if !find {
		return awsModelId
	}
	return modelPrefix + "." + awsModelId
}

func getAwsModelID(requestModel string) string {
	if awsModelIDName, ok := awsModelIDMap[requestModel]; ok {
		return awsModelIDName
	}
	return requestModel
}

func awsHandler(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) (*types.NewAPIError, *dto.Usage) {

	ctx, cancel := newAwsInvokeContext(info.GetRelayContext(c.Request.Context()))
	defer cancel()

	awsResp, err := a.AwsClient.InvokeModel(ctx, a.AwsReq.(*bedrockruntime.InvokeModelInput))
	if err != nil {
		statusCode := getAwsErrorStatusCode(err)
		return types.NewOpenAIError(errors.Wrap(err, "InvokeModel"), types.ErrorCodeAwsInvokeError, statusCode), nil
	}

	claudeInfo := &claude.ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}

	// 复制上游 Content-Type 到客户端响应头
	if awsResp.ContentType != nil && *awsResp.ContentType != "" {
		c.Writer.Header().Set("Content-Type", *awsResp.ContentType)
	}

	handlerErr := claude.HandleClaudeResponseData(c, info, claudeInfo, nil, awsResp.Body)
	if handlerErr != nil {
		return handlerErr, nil
	}
	return nil, claudeInfo.Usage
}

func awsStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) (*types.NewAPIError, *dto.Usage) {
	ctx, cancel := newAwsInvokeContext(info.GetRelayContext(c.Request.Context()))
	defer cancel()

	awsResp, err := a.AwsClient.InvokeModelWithResponseStream(ctx, a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput))
	if err != nil {
		statusCode := getAwsErrorStatusCode(err)
		return types.NewOpenAIError(errors.Wrap(err, "InvokeModelWithResponseStream"), types.ErrorCodeAwsInvokeError, statusCode), nil
	}
	stream := awsResp.GetStream()
	defer stream.Close()

	claudeInfo := &claude.ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}

	for event := range stream.Events() {
		switch v := event.(type) {
		case *bedrockruntimeTypes.ResponseStreamMemberChunk:
			info.SetFirstResponseTime()
			respErr := claude.HandleStreamResponseData(c, info, claudeInfo, string(v.Value.Bytes))
			if respErr != nil {
				return respErr, nil
			}
		case *bedrockruntimeTypes.UnknownUnionMember:
			logger.LogWarn(c, fmt.Sprintf("AWS response stream returned unknown event tag %q", v.Tag))
			return types.NewError(errors.New("unknown response type"), types.ErrorCodeInvalidRequest), nil
		default:
			logger.LogWarn(c, "AWS response stream returned a nil or unknown event type")
			return types.NewError(errors.New("nil or unknown response type"), types.ErrorCodeInvalidRequest), nil
		}
	}

	claude.HandleStreamFinalResponse(c, info, claudeInfo)
	return nil, claudeInfo.Usage
}

func novaStopReasonToOpenAI(stopReason string) string {
	if strings.EqualFold(stopReason, "content_filtered") {
		return "content_filter"
	}
	if stopReason == "" {
		return "stop"
	}
	return reasonmap.ClaudeStopReasonToOpenAIFinishReason(stopReason)
}

func novaUsageToOpenAI(usage NovaUsage) dto.Usage {
	totalTokens := usage.TotalTokens
	if totalTokens == 0 && (usage.InputTokens != 0 || usage.OutputTokens != 0) {
		totalTokens = usage.InputTokens + usage.OutputTokens
	}
	return dto.Usage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      totalTokens,
	}
}

func novaResponseToOpenAI(responseID string, created int64, model string, novaResp NovaResponse) dto.OpenAITextResponse {
	var content strings.Builder
	for _, block := range novaResp.Output.Message.Content {
		content.WriteString(block.Text)
	}
	return dto.OpenAITextResponse{
		Id:      responseID,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []dto.OpenAITextResponseChoice{{
			Index: 0,
			Message: dto.Message{
				Role:    "assistant",
				Content: content.String(),
			},
			FinishReason: novaStopReasonToOpenAI(novaResp.StopReason),
		}},
		Usage: novaUsageToOpenAI(novaResp.Usage),
	}
}

// handleNovaRequest converts the documented Nova Invoke response into the
// OpenAI chat-completions contract.
func handleNovaRequest(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) (*types.NewAPIError, *dto.Usage) {
	ctx, cancel := newAwsInvokeContext(info.GetRelayContext(c.Request.Context()))
	defer cancel()

	awsResp, err := a.AwsClient.InvokeModel(ctx, a.AwsReq.(*bedrockruntime.InvokeModelInput))
	if err != nil {
		statusCode := getAwsErrorStatusCode(err)
		return types.NewOpenAIError(errors.Wrap(err, "InvokeModel"), types.ErrorCodeAwsInvokeError, statusCode), nil
	}

	var novaResp NovaResponse
	if err := common.Unmarshal(awsResp.Body, &novaResp); err != nil {
		return types.NewError(errors.Wrap(err, "unmarshal nova response"), types.ErrorCodeBadResponseBody), nil
	}

	response := novaResponseToOpenAI(
		helper.GetResponseID(c),
		common.GetTimestamp(),
		info.UpstreamModelName,
		novaResp,
	)
	c.JSON(http.StatusOK, response)
	return nil, &response.Usage
}

// handleNovaStreamRequest converts Nova's documented event stream
// (contentBlockDelta, messageStop, and metadata) into OpenAI SSE chunks.
func handleNovaStreamRequest(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) (*types.NewAPIError, *dto.Usage) {
	ctx, cancel := newAwsInvokeContext(info.GetRelayContext(c.Request.Context()))
	defer cancel()

	awsResp, err := a.AwsClient.InvokeModelWithResponseStream(
		ctx,
		a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput),
	)
	if err != nil {
		statusCode := getAwsErrorStatusCode(err)
		return types.NewOpenAIError(errors.Wrap(err, "InvokeModelWithResponseStream"), types.ErrorCodeAwsInvokeError, statusCode), nil
	}

	stream := awsResp.GetStream()
	defer stream.Close()

	responseID := helper.GetResponseID(c)
	created := common.GetTimestamp()
	usage := &dto.Usage{}
	var responseText strings.Builder
	finishReason := "stop"
	streamStarted := false
	sawTerminal := false
	sawUsageMetadata := false

	startStream := func() error {
		if streamStarted {
			return nil
		}
		helper.SetEventStreamHeaders(c)
		if err := helper.ObjectData(c, helper.GenerateStartEmptyResponse(responseID, created, info.UpstreamModelName, nil)); err != nil {
			return err
		}
		streamStarted = true
		return nil
	}

	for event := range stream.Events() {
		switch value := event.(type) {
		case *bedrockruntimeTypes.ResponseStreamMemberChunk:
			var novaEvent NovaStreamEvent
			if err := common.Unmarshal(value.Value.Bytes, &novaEvent); err != nil {
				return types.NewError(errors.Wrap(err, "unmarshal nova stream event"), types.ErrorCodeBadResponseBody), usage
			}
			if novaEvent.ContentBlockDelta != nil && novaEvent.ContentBlockDelta.Delta.Text != "" {
				if err := startStream(); err != nil {
					return types.NewError(err, types.ErrorCodeBadResponseBody), usage
				}
				info.SetFirstResponseTime()
				text := novaEvent.ContentBlockDelta.Delta.Text
				responseText.WriteString(text)
				chunk := &dto.ChatCompletionsStreamResponse{
					Id:      responseID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   info.UpstreamModelName,
					Choices: []dto.ChatCompletionsStreamResponseChoice{{
						Index: 0,
						Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text},
					}},
				}
				if err := helper.ObjectData(c, chunk); err != nil {
					return types.NewError(err, types.ErrorCodeBadResponseBody), usage
				}
			}
			if novaEvent.MessageStop != nil {
				finishReason = novaStopReasonToOpenAI(novaEvent.MessageStop.StopReason)
				sawTerminal = true
			}
			if novaEvent.Metadata != nil {
				*usage = novaUsageToOpenAI(novaEvent.Metadata.Usage)
				sawUsageMetadata = true
			}
		case *bedrockruntimeTypes.UnknownUnionMember:
			return types.NewError(
				fmt.Errorf("AWS Nova response stream returned unknown event tag %q", value.Tag),
				types.ErrorCodeBadResponseBody,
			), usage
		default:
			return types.NewError(errors.New("AWS Nova response stream returned an unknown event"), types.ErrorCodeBadResponseBody), usage
		}
	}
	if err := stream.Err(); err != nil {
		return types.NewOpenAIError(errors.Wrap(err, "read Nova response stream"), types.ErrorCodeAwsInvokeError, getAwsErrorStatusCode(err)), usage
	}
	if !sawTerminal {
		return types.NewError(errors.New("AWS Nova response stream ended before messageStop"), types.ErrorCodeBadResponseBody), usage
	}
	if !sawUsageMetadata {
		estimated := service.ResponseText2Usage(c, responseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		*usage = *estimated
	}
	if err := startStream(); err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), usage
	}
	if err := helper.ObjectData(c, helper.GenerateStopResponse(responseID, created, info.UpstreamModelName, finishReason)); err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), usage
	}
	if info.ShouldIncludeUsage {
		if err := helper.ObjectData(c, helper.GenerateFinalUsageResponse(responseID, created, info.UpstreamModelName, *usage)); err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody), usage
		}
	}
	helper.Done(c)
	return nil, usage
}
