package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/tidwall/gjson"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupResponsesChatTest(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = oldTimeout
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-test",
		},
		RelayFormat: types.RelayFormatOpenAI,
		DisablePing: true,
	}

	return c, recorder, resp, info
}

func emptyResponsesCompletedEvent() string {
	return `{"type":"response.completed","response":{"id":"resp_empty","created_at":123,"model":"gpt-test","usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`
}

func incompleteResponsesEvent(reason string) string {
	return `{"type":"response.incomplete","response":{"id":"resp_incomplete","created_at":123,"model":"gpt-test","status":"incomplete","incomplete_details":{"reason":"` + reason + `"},"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`
}

func responsesToolCallBody() string {
	return `{"id":"resp_tool","created_at":123,"model":"gpt-test","status":"completed","output":[{"type":"function_call","id":"fc_weather","call_id":"call_weather","name":"get_weather","arguments":"{\"location\":\"Paris\"}"}],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}`
}

func responsesToolCallStreamBody() string {
	return strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_weather","call_id":"call_weather","name":"get_weather"}}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_weather","delta":"{\"location\""}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_weather","delta":":\"Paris\"}"}`,
		`data: {"type":"response.completed","response":{"id":"resp_tool","created_at":123,"model":"gpt-test","status":"completed","usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`,
		"data: [DONE]",
		"",
	}, "\n\n")
}

func responsesMixedTextToolCallStreamDoneBody() string {
	return strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"Let me check."}`,
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_weather","call_id":"call_weather","name":"get_weather"}}`,
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_weather","arguments":"{\"location\":\"Paris\"}"}`,
		`data: {"type":"response.completed","response":{"id":"resp_tool","created_at":123,"model":"gpt-test","status":"completed","usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`,
		"data: [DONE]",
		"",
	}, "\n\n")
}

func collectSSEData(body string) []string {
	events := strings.Split(body, "\n\n")
	data := make([]string, 0, len(events))
	for _, event := range events {
		for _, line := range strings.Split(event, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			value := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if value == "" || value == "[DONE]" {
				continue
			}
			data = append(data, value)
		}
	}
	return data
}

func assertChatToolCallResponse(t *testing.T, body string) {
	t.Helper()

	if got := gjson.Get(body, "choices.0.finish_reason").String(); got != constant.FinishReasonToolCalls {
		t.Fatalf("expected finish_reason=%q, got %q in %s", constant.FinishReasonToolCalls, got, body)
	}
	if got := gjson.Get(body, "choices.0.message.role").String(); got != "assistant" {
		t.Fatalf("expected assistant role, got %q in %s", got, body)
	}
	if got := gjson.Get(body, "choices.0.message.tool_calls.0.id").String(); got != "call_weather" {
		t.Fatalf("expected tool call id call_weather, got %q in %s", got, body)
	}
	if got := gjson.Get(body, "choices.0.message.tool_calls.0.type").String(); got != "function" {
		t.Fatalf("expected tool call type function, got %q in %s", got, body)
	}
	if got := gjson.Get(body, "choices.0.message.tool_calls.0.function.name").String(); got != "get_weather" {
		t.Fatalf("expected function name get_weather, got %q in %s", got, body)
	}
	if got := gjson.Get(body, "choices.0.message.tool_calls.0.function.arguments").String(); got != `{"location":"Paris"}` {
		t.Fatalf("expected function arguments for Paris, got %q in %s", got, body)
	}
}

func assertChatToolCallResponseWithContent(t *testing.T, body string, wantContent string) {
	t.Helper()

	assertChatToolCallResponse(t, body)
	if got := gjson.Get(body, "choices.0.message.content").String(); got != wantContent {
		t.Fatalf("expected assistant content %q, got %q in %s", wantContent, got, body)
	}
}

func TestOaiResponsesToChatStreamToNonStreamHandlerRejectsEmptyAssistantResult(t *testing.T) {
	body := "data: " + emptyResponsesCompletedEvent() + "\n\ndata: [DONE]\n\n"
	c, recorder, resp, info := setupResponsesChatTest(t, body)

	usage, err := OaiResponsesToChatStreamToNonStreamHandler(c, info, resp)

	if err == nil {
		t.Fatal("expected empty assistant result to return an error")
	}
	if usage != nil {
		t.Fatalf("expected nil usage on error, got %#v", usage)
	}
	if !strings.Contains(err.Error(), "empty assistant response") {
		t.Fatalf("expected empty assistant response error, got %q", err.Error())
	}
	if err.GetErrorCode() != types.ErrorCodeEmptyResponse {
		t.Fatalf("expected empty_response error code, got %q", err.GetErrorCode())
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("expected no synthetic chat response, got %q", recorder.Body.String())
	}
}

func TestOaiResponsesToChatHandlerConvertsToolCall(t *testing.T) {
	c, recorder, resp, info := setupResponsesChatTest(t, responsesToolCallBody())

	usage, err := OaiResponsesToChatHandler(c, info, resp)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 14 {
		t.Fatalf("expected total usage 14, got %#v", usage)
	}
	assertChatToolCallResponse(t, recorder.Body.String())
}

func TestOaiResponsesToChatStreamToNonStreamHandlerConvertsToolCall(t *testing.T) {
	c, recorder, resp, info := setupResponsesChatTest(t, responsesToolCallStreamBody())

	usage, err := OaiResponsesToChatStreamToNonStreamHandler(c, info, resp)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 14 {
		t.Fatalf("expected total usage 14, got %#v", usage)
	}
	assertChatToolCallResponse(t, recorder.Body.String())
}

func TestOaiResponsesToChatStreamHandlerConvertsToolCall(t *testing.T) {
	c, recorder, resp, info := setupResponsesChatTest(t, responsesToolCallStreamBody())

	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 14 {
		t.Fatalf("expected total usage 14, got %#v", usage)
	}

	var sawName bool
	var sawFirstArgsDelta bool
	var sawSecondArgsDelta bool
	var sawToolCallFinish bool
	for _, data := range collectSSEData(recorder.Body.String()) {
		if !gjson.Valid(data) {
			continue
		}
		toolCall := gjson.Get(data, "choices.0.delta.tool_calls.0")
		if toolCall.Exists() {
			if gjson.Get(data, "choices.0.delta.tool_calls.0.id").String() == "call_weather" &&
				gjson.Get(data, "choices.0.delta.tool_calls.0.type").String() == "function" &&
				gjson.Get(data, "choices.0.delta.tool_calls.0.function.name").String() == "get_weather" {
				sawName = true
			}
			switch gjson.Get(data, "choices.0.delta.tool_calls.0.function.arguments").String() {
			case `{"location"`:
				sawFirstArgsDelta = true
			case `:"Paris"}`:
				sawSecondArgsDelta = true
			}
		}
		if gjson.Get(data, "choices.0.finish_reason").String() == constant.FinishReasonToolCalls {
			sawToolCallFinish = true
		}
	}

	if !sawName {
		t.Fatalf("expected streamed tool call name chunk, got %q", recorder.Body.String())
	}
	if !sawFirstArgsDelta || !sawSecondArgsDelta {
		t.Fatalf("expected streamed tool call argument deltas, got %q", recorder.Body.String())
	}
	if !sawToolCallFinish {
		t.Fatalf("expected streamed tool_calls finish reason, got %q", recorder.Body.String())
	}
}

func TestOaiResponsesToChatStreamToNonStreamHandlerPreservesTextAndDoneToolCall(t *testing.T) {
	c, recorder, resp, info := setupResponsesChatTest(t, responsesMixedTextToolCallStreamDoneBody())

	usage, err := OaiResponsesToChatStreamToNonStreamHandler(c, info, resp)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 14 {
		t.Fatalf("expected total usage 14, got %#v", usage)
	}
	assertChatToolCallResponseWithContent(t, recorder.Body.String(), "Let me check.")
}

func TestOaiResponsesToChatStreamHandlerPreservesTextAndDoneToolCall(t *testing.T) {
	c, recorder, resp, info := setupResponsesChatTest(t, responsesMixedTextToolCallStreamDoneBody())

	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 14 {
		t.Fatalf("expected total usage 14, got %#v", usage)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, `"content":"Let me check."`) ||
		!strings.Contains(body, `"name":"get_weather"`) ||
		!strings.Contains(body, `Paris`) ||
		!strings.Contains(body, `"finish_reason":"tool_calls"`) {
		t.Fatalf("expected streamed text and tool call chunks, got %q", body)
	}
}

func TestOaiStreamToNonStreamHandlerDefaultsMissingToolCallType(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","created":123,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_weather","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","created":123,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"location\""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","created":123,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"Paris\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","created":123,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`,
		"data: [DONE]",
		"",
	}, "\n\n")
	c, recorder, resp, info := setupResponsesChatTest(t, body)
	info.RelayMode = relayconstant.RelayModeChatCompletions

	usage, err := OaiStreamToNonStreamHandler(c, info, resp)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 14 {
		t.Fatalf("expected total usage 14, got %#v", usage)
	}
	assertChatToolCallResponse(t, recorder.Body.String())
}

func TestOaiResponsesToChatStreamHandlerRejectsEmptyAssistantResult(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":""}`,
		"data: " + emptyResponsesCompletedEvent(),
		"data: [DONE]",
		"",
	}, "\n\n")
	c, recorder, resp, info := setupResponsesChatTest(t, body)

	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)

	if err == nil {
		t.Fatal("expected empty assistant result to return an error")
	}
	if usage != nil {
		t.Fatalf("expected nil usage on error, got %#v", usage)
	}
	if !strings.Contains(err.Error(), "empty assistant response") {
		t.Fatalf("expected empty assistant response error, got %q", err.Error())
	}
	if err.GetErrorCode() != types.ErrorCodeEmptyResponse {
		t.Fatalf("expected empty_response error code, got %q", err.GetErrorCode())
	}
	if strings.Contains(recorder.Body.String(), "chat.completion.chunk") {
		t.Fatalf("expected no synthetic stream chunks, got %q", recorder.Body.String())
	}
}

func TestOaiResponsesToChatStreamToNonStreamHandlerMapsIncompleteFinishReason(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		"data: " + incompleteResponsesEvent("max_output_tokens"),
		"data: [DONE]",
		"",
	}, "\n\n")
	c, recorder, resp, info := setupResponsesChatTest(t, body)

	usage, err := OaiResponsesToChatStreamToNonStreamHandler(c, info, resp)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage == nil {
		t.Fatal("expected usage")
	}
	if !strings.Contains(recorder.Body.String(), `"finish_reason":"length"`) {
		t.Fatalf("expected length finish_reason, got %q", recorder.Body.String())
	}
}

func TestOaiResponsesToChatStreamHandlerMapsIncompleteFinishReason(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		"data: " + incompleteResponsesEvent("content_filter"),
		"data: [DONE]",
		"",
	}, "\n\n")
	c, recorder, resp, info := setupResponsesChatTest(t, body)

	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage == nil {
		t.Fatal("expected usage")
	}
	if !strings.Contains(recorder.Body.String(), `"finish_reason":"content_filter"`) {
		t.Fatalf("expected content_filter finish_reason, got %q", recorder.Body.String())
	}
}
