package baidu

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

// https://cloud.baidu.com/doc/WENXINWORKSHOP/s/flfmc9do2

var (
	baiduTokenStore        sync.Map
	baiduTokenRefreshGroup singleflight.Group
)

func requestOpenAI2Baidu(request dto.GeneralOpenAIRequest) *BaiduChatRequest {
	baiduRequest := BaiduChatRequest{
		Temperature:    request.Temperature,
		TopP:           request.TopP,
		PenaltyScore:   request.FrequencyPenalty,
		Stream:         request.Stream,
		DisableSearch:  false,
		EnableCitation: false,
		UserId:         request.User,
	}
	if requestedMaxTokens := request.GetMaxTokensPointer(); requestedMaxTokens != nil {
		maxTokens := int(*requestedMaxTokens)
		if *requestedMaxTokens == 1 {
			maxTokens = 2
		}
		baiduRequest.MaxOutputTokens = &maxTokens
	}
	for _, message := range request.Messages {
		if message.Role == "system" {
			baiduRequest.System = message.StringContent()
		} else {
			baiduRequest.Messages = append(baiduRequest.Messages, BaiduMessage{
				Role:    message.Role,
				Content: message.StringContent(),
			})
		}
	}
	return &baiduRequest
}

func responseBaidu2OpenAI(response *BaiduChatResponse) *dto.OpenAITextResponse {
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:    "assistant",
			Content: response.Result,
		},
		FinishReason: "stop",
	}
	fullTextResponse := dto.OpenAITextResponse{
		Id:      response.Id,
		Object:  "chat.completion",
		Created: response.Created,
		Choices: []dto.OpenAITextResponseChoice{choice},
		Usage:   response.Usage,
	}
	return &fullTextResponse
}

func streamResponseBaidu2OpenAI(baiduResponse *BaiduChatStreamResponse) *dto.ChatCompletionsStreamResponse {
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString(baiduResponse.Result)
	if baiduResponse.IsEnd {
		choice.FinishReason = &constant.FinishReasonStop
	}
	response := dto.ChatCompletionsStreamResponse{
		Id:      baiduResponse.Id,
		Object:  "chat.completion.chunk",
		Created: baiduResponse.Created,
		Model:   "ernie-bot",
		Choices: []dto.ChatCompletionsStreamResponseChoice{choice},
	}
	return &response
}

func embeddingRequestOpenAI2Baidu(request dto.EmbeddingRequest) *BaiduEmbeddingRequest {
	return &BaiduEmbeddingRequest{
		Input: request.ParseInput(),
	}
}

func embeddingResponseBaidu2OpenAI(response *BaiduEmbeddingResponse) *dto.OpenAIEmbeddingResponse {
	openAIEmbeddingResponse := dto.OpenAIEmbeddingResponse{
		Object: "list",
		Data:   make([]dto.OpenAIEmbeddingResponseItem, 0, len(response.Data)),
		Model:  "baidu-embedding",
		Usage:  response.Usage,
	}
	for _, item := range response.Data {
		openAIEmbeddingResponse.Data = append(openAIEmbeddingResponse.Data, dto.OpenAIEmbeddingResponseItem{
			Object:    item.Object,
			Index:     item.Index,
			Embedding: item.Embedding,
		})
	}
	return &openAIEmbeddingResponse
}

func baiduStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	usage := &dto.Usage{}
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var baiduResponse BaiduChatStreamResponse
		if err := common.Unmarshal([]byte(data), &baiduResponse); err != nil {
			common.SysLog("error unmarshalling stream response: " + err.Error())
			sr.Error(err)
			return
		}
		if baiduResponse.Usage.TotalTokens != 0 {
			usage.TotalTokens = baiduResponse.Usage.TotalTokens
			usage.PromptTokens = baiduResponse.Usage.PromptTokens
			usage.CompletionTokens = baiduResponse.Usage.TotalTokens - baiduResponse.Usage.PromptTokens
		}
		response := streamResponseBaidu2OpenAI(&baiduResponse)
		if err := helper.ObjectData(c, response); err != nil {
			common.SysLog("error sending stream response: " + err.Error())
			sr.Error(err)
		}
	})
	service.CloseResponseBodyGracefully(resp)
	return nil, usage
}

func baiduHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	defer service.CloseResponseBodyGracefully(resp)

	var baiduResponse BaiduChatResponse
	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	err = common.Unmarshal(responseBody, &baiduResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	if baiduResponse.ErrorMsg != "" {
		return types.NewError(fmt.Errorf("%s", baiduResponse.ErrorMsg), types.ErrorCodeBadResponseBody), nil
	}
	fullTextResponse := responseBaidu2OpenAI(&baiduResponse)
	jsonResponse, err := common.Marshal(fullTextResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return nil, &fullTextResponse.Usage
}

func baiduEmbeddingHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	defer service.CloseResponseBodyGracefully(resp)

	var baiduResponse BaiduEmbeddingResponse
	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	err = common.Unmarshal(responseBody, &baiduResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	if baiduResponse.ErrorMsg != "" {
		return types.NewError(fmt.Errorf("%s", baiduResponse.ErrorMsg), types.ErrorCodeBadResponseBody), nil
	}
	fullTextResponse := embeddingResponseBaidu2OpenAI(&baiduResponse)
	jsonResponse, err := common.Marshal(fullTextResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return nil, &fullTextResponse.Usage
}

func getBaiduAccessToken(apiKey string, ctx context.Context) (string, error) {
	if val, ok := baiduTokenStore.Load(apiKey); ok {
		var accessToken BaiduAccessToken
		if accessToken, ok = val.(BaiduAccessToken); ok {
			now := time.Now()
			if !now.Before(accessToken.ExpiresAt) {
				baiduTokenStore.Delete(apiKey)
			} else {
				// Refresh a still-valid token in the background before it expires,
				// but never return an already expired cached credential.
				if now.Add(time.Hour).After(accessToken.ExpiresAt) {
					// DoChan starts at most one bounded refresh per key. Duplicate
					// callers join the in-flight operation without spawning an
					// unbounded set of waiting goroutines.
					_ = baiduTokenRefreshGroup.DoChan(apiKey, func() (any, error) {
						return getBaiduAccessTokenHelper(apiKey, context.Background())
					})
				}
				return accessToken.AccessToken, nil
			}
		}
	}

	result := baiduTokenRefreshGroup.DoChan(apiKey, func() (any, error) {
		// The shared refresh must not be canceled by whichever request happens
		// to become the leader. getBaiduAccessTokenHelper supplies its own
		// finite timeout; individual waiters can still stop waiting below.
		return getBaiduAccessTokenHelper(apiKey, context.Background())
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case refreshResult := <-result:
		if refreshResult.Err != nil {
			return "", refreshResult.Err
		}
		accessToken, ok := refreshResult.Val.(*BaiduAccessToken)
		if !ok || accessToken == nil {
			return "", errors.New("getBaiduAccessToken return a nil token")
		}
		return accessToken.AccessToken, nil
	}
}

func getBaiduAccessTokenHelper(apiKey string, ctx context.Context) (*BaiduAccessToken, error) {
	parts := strings.Split(apiKey, "|")
	if len(parts) != 2 {
		return nil, errors.New("invalid baidu apikey")
	}
	tokenURL, err := url.Parse("https://aip.baidubce.com/oauth/2.0/token")
	if err != nil {
		return nil, err
	}
	query := tokenURL.Query()
	query.Set("grant_type", "client_credentials")
	query.Set("client_id", parts[0])
	query.Set("client_secret", parts[1])
	tokenURL.RawQuery = query.Encode()

	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, tokenURL.String(), nil)
	if err != nil {
		return nil, service.SanitizeNetworkError(err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")
	res, err := service.DoUpstreamRequest(service.GetHttpClient(), req)
	if err != nil {
		return nil, fmt.Errorf("Baidu token request failed: %s", common.MaskSensitiveInfo(err.Error()))
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Baidu token endpoint returned status %d", res.StatusCode)
	}

	var accessToken BaiduAccessToken
	body, err := service.ReadResponseBodyWithLimit(res.Body, 1<<20)
	if err != nil {
		return nil, err
	}
	if err = common.Unmarshal(body, &accessToken); err != nil {
		return nil, err
	}
	if accessToken.Error != "" {
		return nil, errors.New(accessToken.Error + ": " + accessToken.ErrorDescription)
	}
	if accessToken.AccessToken == "" {
		return nil, errors.New("getBaiduAccessTokenHelper get empty access token")
	}
	expiresIn, ok := common.SafeOptionalDuration64(accessToken.ExpiresIn, time.Second, "Baidu access token expiry")
	if !ok {
		return nil, errors.New("Baidu token response contains invalid expires_in")
	}
	accessToken.ExpiresAt = time.Now().Add(expiresIn)
	baiduTokenStore.Store(apiKey, accessToken)
	return &accessToken, nil
}
