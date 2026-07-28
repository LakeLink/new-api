package xunfei

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// https://console.xfyun.cn/services/cbm
// https://www.xfyun.cn/doc/spark/Web.html

func requestOpenAI2Xunfei(request dto.GeneralOpenAIRequest, xunfeiAppId string, domain string) *XunfeiChatRequest {
	messages := make([]XunfeiMessage, 0, len(request.Messages))
	supportsSystemMessage := domain == "generalv3.5" || domain == "max-32k" || domain == "4.0Ultra"
	pendingSystem := make([]string, 0)
	for _, message := range request.Messages {
		role := message.Role
		if role == "system" && !supportsSystemMessage {
			pendingSystem = append(pendingSystem, message.StringContent())
			continue
		}
		content := message.StringContent()
		if len(pendingSystem) > 0 {
			systemContent := strings.Join(pendingSystem, "\n\n")
			if role == "user" {
				content = systemContent + "\n\n" + content
			} else {
				messages = append(messages, XunfeiMessage{Role: "user", Content: systemContent})
			}
			pendingSystem = pendingSystem[:0]
		}
		messages = append(messages, XunfeiMessage{
			Role:    role,
			Content: content,
		})
	}
	if len(pendingSystem) > 0 {
		messages = append(messages, XunfeiMessage{Role: "user", Content: strings.Join(pendingSystem, "\n\n")})
	}
	xunfeiRequest := XunfeiChatRequest{}
	xunfeiRequest.Header.AppId = xunfeiAppId
	xunfeiRequest.Parameter.Chat.Domain = domain
	xunfeiRequest.Parameter.Chat.Temperature = request.Temperature
	xunfeiRequest.Parameter.Chat.TopK = request.TopK
	xunfeiRequest.Parameter.Chat.MaxTokens = request.MaxCompletionTokens
	if xunfeiRequest.Parameter.Chat.MaxTokens == nil {
		xunfeiRequest.Parameter.Chat.MaxTokens = request.MaxTokens
	}
	xunfeiRequest.Payload.Message.Text = messages
	return &xunfeiRequest
}

func responseXunfei2OpenAI(response *XunfeiChatResponse) *dto.OpenAITextResponse {
	if len(response.Payload.Choices.Text) == 0 {
		response.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:    "assistant",
			Content: response.Payload.Choices.Text[0].Content,
		},
		FinishReason: constant.FinishReasonStop,
	}
	fullTextResponse := dto.OpenAITextResponse{
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Choices: []dto.OpenAITextResponseChoice{choice},
		Usage:   response.Payload.Usage.Text,
	}
	return &fullTextResponse
}

func streamResponseXunfei2OpenAI(xunfeiResponse *XunfeiChatResponse) *dto.ChatCompletionsStreamResponse {
	if len(xunfeiResponse.Payload.Choices.Text) == 0 {
		xunfeiResponse.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString(xunfeiResponse.Payload.Choices.Text[0].Content)
	if xunfeiResponse.Payload.Choices.Status == 2 {
		choice.FinishReason = &constant.FinishReasonStop
	}
	response := dto.ChatCompletionsStreamResponse{
		Object:  "chat.completion.chunk",
		Created: common.GetTimestamp(),
		Model:   "SparkDesk",
		Choices: []dto.ChatCompletionsStreamResponseChoice{choice},
	}
	return &response
}

func buildXunfeiAuthURL(hostURL string, apiKey, apiSecret string) (string, error) {
	HmacWithShaToBase64 := func(algorithm, data, key string) string {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(data))
		encodeData := mac.Sum(nil)
		return base64.StdEncoding.EncodeToString(encodeData)
	}
	ul, err := url.Parse(hostURL)
	if err != nil {
		return "", fmt.Errorf("invalid Xunfei endpoint: %w", err)
	}
	if ul.Scheme != "wss" || ul.Host == "" || ul.User != nil || ul.RawQuery != "" || ul.Fragment != "" {
		return "", errors.New("invalid Xunfei websocket endpoint")
	}
	date := time.Now().UTC().Format(time.RFC1123)
	signString := []string{"host: " + ul.Host, "date: " + date, "GET " + ul.Path + " HTTP/1.1"}
	sign := strings.Join(signString, "\n")
	sha := HmacWithShaToBase64("hmac-sha256", sign, apiSecret)
	authUrl := fmt.Sprintf("hmac username=\"%s\", algorithm=\"%s\", headers=\"%s\", signature=\"%s\"", apiKey,
		"hmac-sha256", "host date request-line", sha)
	authorization := base64.StdEncoding.EncodeToString([]byte(authUrl))
	v := url.Values{}
	v.Add("host", ul.Host)
	v.Add("date", date)
	v.Add("authorization", authorization)
	callURL := hostURL + "?" + v.Encode()
	return callURL, nil
}

func xunfeiStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, textRequest dto.GeneralOpenAIRequest, appId string, apiSecret string, apiKey string) (*dto.Usage, *types.NewAPIError) {
	domain, authUrl, err := getXunfeiAuthURL(c, apiKey, apiSecret, textRequest.Model)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest)
	}
	relayCtx := info.GetRelayContext(c.Request.Context())
	resultChan, err := xunfeiMakeRequest(relayCtx, textRequest, domain, authUrl, appId)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}

	var first xunfeiResult
	select {
	case result, ok := <-resultChan:
		if !ok {
			return nil, types.NewOpenAIError(errors.New("xunfei upstream closed without a response"), types.ErrorCodeBadResponse, http.StatusBadGateway)
		}
		if result.Err != nil {
			return nil, types.NewOpenAIError(result.Err, types.ErrorCodeBadResponse, http.StatusBadGateway)
		}
		first = result
	case <-relayCtx.Done():
		return nil, types.NewError(relayCtx.Err(), types.ErrorCodeDoRequestFailed)
	}

	helper.SetEventStreamHeaders(c)
	var usage dto.Usage
	pendingFirst := true
	c.Stream(func(w io.Writer) bool {
		var result xunfeiResult
		if pendingFirst {
			result = first
			pendingFirst = false
		} else {
			select {
			case next, ok := <-resultChan:
				if !ok {
					helper.Done(c)
					return false
				}
				result = next
			case <-relayCtx.Done():
				return false
			}
		}

		if result.Err != nil {
			_ = writeXunfeiStreamError(c, result.Err.Error())
			helper.Done(c)
			return false
		}
		xunfeiResponse := result.Response
		usage.PromptTokens += xunfeiResponse.Payload.Usage.Text.PromptTokens
		usage.CompletionTokens += xunfeiResponse.Payload.Usage.Text.CompletionTokens
		usage.TotalTokens += xunfeiResponse.Payload.Usage.Text.TotalTokens
		response := streamResponseXunfei2OpenAI(&xunfeiResponse)
		jsonResponse, err := common.Marshal(response)
		if err != nil {
			_ = writeXunfeiStreamError(c, "failed to encode Xunfei response")
			helper.Done(c)
			return false
		}
		if err := helper.StringData(c, string(jsonResponse)); err != nil {
			return false
		}
		if info.OnOutputChunk != nil {
			info.OnOutputChunk()
		}
		return true
	})
	return &usage, nil
}

func xunfeiHandler(c *gin.Context, info *relaycommon.RelayInfo, textRequest dto.GeneralOpenAIRequest, appId string, apiSecret string, apiKey string) (*dto.Usage, *types.NewAPIError) {
	domain, authUrl, err := getXunfeiAuthURL(c, apiKey, apiSecret, textRequest.Model)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest)
	}
	relayCtx := info.GetRelayContext(c.Request.Context())
	resultChan, err := xunfeiMakeRequest(relayCtx, textRequest, domain, authUrl, appId)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	var usage dto.Usage
	var content string
	var xunfeiResponse XunfeiChatResponse
	receivedResponse := false
	for {
		select {
		case result, ok := <-resultChan:
			if !ok {
				if !receivedResponse {
					return nil, types.NewOpenAIError(errors.New("xunfei upstream closed without a response"), types.ErrorCodeBadResponse, http.StatusBadGateway)
				}
				goto complete
			}
			if result.Err != nil {
				return nil, types.NewOpenAIError(result.Err, types.ErrorCodeBadResponse, http.StatusBadGateway)
			}
			receivedResponse = true
			xunfeiResponse = result.Response
			if len(xunfeiResponse.Payload.Choices.Text) == 0 {
				continue
			}
			content += xunfeiResponse.Payload.Choices.Text[0].Content
			usage.PromptTokens += xunfeiResponse.Payload.Usage.Text.PromptTokens
			usage.CompletionTokens += xunfeiResponse.Payload.Usage.Text.CompletionTokens
			usage.TotalTokens += xunfeiResponse.Payload.Usage.Text.TotalTokens
		case <-relayCtx.Done():
			return nil, types.NewError(relayCtx.Err(), types.ErrorCodeDoRequestFailed)
		}
	}

complete:
	if len(xunfeiResponse.Payload.Choices.Text) == 0 {
		xunfeiResponse.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	xunfeiResponse.Payload.Choices.Text[0].Content = content

	response := responseXunfei2OpenAI(&xunfeiResponse)
	jsonResponse, err := common.Marshal(response)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	_, _ = c.Writer.Write(jsonResponse)
	return &usage, nil
}

type xunfeiResult struct {
	Response XunfeiChatResponse
	Err      error
}

func xunfeiMakeRequest(ctx context.Context, textRequest dto.GeneralOpenAIRequest, domain, authUrl, appId string) (<-chan xunfeiResult, error) {
	d := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	conn, resp, err := d.DialContext(ctx, authUrl, nil)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, service.SanitizeNetworkError(err)
	}
	if resp == nil || resp.StatusCode != http.StatusSwitchingProtocols {
		if conn != nil {
			_ = conn.Close()
		}
		if resp == nil {
			return nil, errors.New("xunfei websocket handshake returned no response")
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("xunfei websocket handshake returned status %d", resp.StatusCode)
	}
	helper.LimitUpstreamWebsocketMessages(conn)

	data := requestOpenAI2Xunfei(textRequest, appId, domain)
	requestBody, err := common.Marshal(data)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, requestBody); err != nil {
		_ = conn.Close()
		return nil, err
	}

	resultChan := make(chan xunfeiResult)
	connectionDone := make(chan struct{})
	go func() {
		defer conn.Close()
		defer close(resultChan)
		defer close(connectionDone)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() == nil {
					sendXunfeiResult(ctx, resultChan, xunfeiResult{Err: fmt.Errorf("xunfei websocket read failed: %w", err)})
				}
				return
			}
			var response XunfeiChatResponse
			if err := common.Unmarshal(msg, &response); err != nil {
				sendXunfeiResult(ctx, resultChan, xunfeiResult{Err: fmt.Errorf("xunfei response decode failed: %w", err)})
				return
			}
			if err := xunfeiResponseError(&response); err != nil {
				sendXunfeiResult(ctx, resultChan, xunfeiResult{Err: err})
				return
			}
			if !sendXunfeiResult(ctx, resultChan, xunfeiResult{Response: response}) {
				return
			}
			if response.Payload.Choices.Status == 2 {
				return
			}
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-connectionDone:
		}
	}()

	return resultChan, nil
}

func sendXunfeiResult(ctx context.Context, resultChan chan<- xunfeiResult, result xunfeiResult) bool {
	select {
	case resultChan <- result:
		return true
	case <-ctx.Done():
		return false
	}
}

func xunfeiResponseError(response *XunfeiChatResponse) error {
	if response == nil || response.Header.Code == 0 {
		return nil
	}
	message := strings.TrimSpace(response.Header.Message)
	if message == "" {
		message = "unknown upstream error"
	}
	if response.Header.Sid != "" {
		return fmt.Errorf("xunfei upstream error %d: %s (sid: %s)", response.Header.Code, message, response.Header.Sid)
	}
	return fmt.Errorf("xunfei upstream error %d: %s", response.Header.Code, message)
}

func writeXunfeiStreamError(c *gin.Context, message string) error {
	payload := struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}{}
	payload.Error.Message = message
	payload.Error.Type = "upstream_error"
	payload.Error.Code = "xunfei_error"
	data, err := common.Marshal(payload)
	if err != nil {
		return err
	}
	return helper.StringData(c, string(data))
}

func apiVersion2domain(apiVersion string) string {
	switch apiVersion {
	case "v1.1":
		return "lite"
	case "v2.1":
		return "generalv2"
	case "v3.1":
		return "generalv3"
	case "v3.5":
		return "generalv3.5"
	case "v4.0":
		return "4.0Ultra"
	}
	return "general" + apiVersion
}

func getXunfeiAuthURL(c *gin.Context, apiKey string, apiSecret string, modelName string) (string, string, error) {
	apiVersion := getAPIVersion(c, modelName)
	domain := apiVersion2domain(apiVersion)
	authURL, err := buildXunfeiAuthURL(fmt.Sprintf("wss://spark-api.xf-yun.com/%s/chat", apiVersion), apiKey, apiSecret)
	if err != nil {
		return "", "", err
	}
	return domain, authURL, nil
}

func getAPIVersion(c *gin.Context, modelName string) string {
	query := c.Request.URL.Query()
	apiVersion := query.Get("api-version")
	if apiVersion != "" {
		return apiVersion
	}
	parts := strings.Split(modelName, "-")
	if len(parts) == 2 {
		apiVersion = parts[1]
		return apiVersion

	}
	apiVersion = c.GetString("api_version")
	if apiVersion != "" {
		return apiVersion
	}
	apiVersion = "v1.1"
	common.SysLog("api_version not found, using default: " + apiVersion)
	return apiVersion
}
