package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdaptorConvertsUpstreamChatStreamToNonStreamResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","service_tier":"default","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello "},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"world"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta:                &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		RelayMode:                  relayconstant.RelayModeChatCompletions,
		RelayFormat:                types.RelayFormatOpenAI,
		IsStream:                   true,
		UpstreamStreamForNonStream: true,
	}

	usage, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 4, usage.(*dto.Usage).PromptTokens)
	assert.Equal(t, 6, usage.(*dto.Usage).CompletionTokens)
	assert.Equal(t, 10, usage.(*dto.Usage).TotalTokens)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

	var got dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &got))
	require.Len(t, got.Choices, 1)
	assert.Equal(t, "chat.completion", got.Object)
	assert.Equal(t, float64(1710000000), got.Created)
	assert.Equal(t, "Hello world", got.Choices[0].Message.StringContent())
	assert.Equal(t, "tool_calls", got.Choices[0].FinishReason)

	var toolCalls []dto.ToolCallResponse
	require.NoError(t, common.Unmarshal(got.Choices[0].Message.ToolCalls, &toolCalls))
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "call_1", toolCalls[0].ID)
	assert.Equal(t, "lookup", toolCalls[0].Function.Name)
	assert.Equal(t, `{"q":"x"}`, toolCalls[0].Function.Arguments)
}

func TestOaiStreamToNonStreamRejectsEmptyUpstreamStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: types.RelayFormatOpenAI,
	}

	usage, apiErr := OaiStreamToNonStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
	assert.Empty(t, recorder.Body.Bytes())
}

func TestOaiStreamToNonStreamReturnsUpstreamStreamErrorAsGatewayError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			`data: {"error":{"message":"upstream failed","type":"server_error","code":"overloaded"}}` + "\n\n",
		)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: types.RelayFormatOpenAI,
	}

	usage, apiErr := OaiStreamToNonStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.Equal(t, "upstream failed", apiErr.Error())
	assert.Empty(t, recorder.Body.Bytes())
}

func TestOaiStreamToNonStreamStopsWhenRequestIsCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestContext, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestContext)
	reader, writer := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: reader}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: types.RelayFormatOpenAI,
	}

	usage, apiErr := OaiStreamToNonStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusGatewayTimeout, apiErr.StatusCode)
	require.NoError(t, writer.Close())
}

func TestOaiStreamToNonStreamReturnsIdleTimeout(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 1
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	resp := &http.Response{StatusCode: http.StatusOK, Body: reader}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: types.RelayFormatOpenAI,
	}
	type handlerResult struct {
		usage *dto.Usage
		err   *types.NewAPIError
	}
	resultCh := make(chan handlerResult, 1)
	go func() {
		usage, apiErr := OaiStreamToNonStreamHandler(c, info, resp)
		resultCh <- handlerResult{usage: usage, err: apiErr}
	}()

	select {
	case result := <-resultCh:
		require.Nil(t, result.usage)
		require.NotNil(t, result.err)
		assert.Equal(t, http.StatusGatewayTimeout, result.err.StatusCode)
	case <-time.After(3 * time.Second):
		t.Fatal("buffered stream did not stop after idle timeout")
	}
}
