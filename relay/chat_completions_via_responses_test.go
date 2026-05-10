package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type fakeResponsesAdaptor struct {
	responseBody        string
	responseContentType string
	convertedRequest    dto.OpenAIResponsesRequest
	sentRequest         dto.OpenAIResponsesRequest
	requestURLPath      string
	relayMode           int
}

func (a *fakeResponsesAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *fakeResponsesAdaptor) GetRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return "", nil
}

func (a *fakeResponsesAdaptor) SetupRequestHeader(_ *gin.Context, _ *http.Header, _ *relaycommon.RelayInfo) error {
	return nil
}

func (a *fakeResponsesAdaptor) ConvertOpenAIRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	return request, nil
}

func (a *fakeResponsesAdaptor) ConvertRerankRequest(_ *gin.Context, _ int, request dto.RerankRequest) (any, error) {
	return request, nil
}

func (a *fakeResponsesAdaptor) ConvertEmbeddingRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return request, nil
}

func (a *fakeResponsesAdaptor) ConvertAudioRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ dto.AudioRequest) (io.Reader, error) {
	return nil, nil
}

func (a *fakeResponsesAdaptor) ConvertImageRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return request, nil
}

func (a *fakeResponsesAdaptor) ConvertOpenAIResponsesRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	a.convertedRequest = request
	return request, nil
}

func (a *fakeResponsesAdaptor) DoRequest(_ *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	a.requestURLPath = info.RequestURLPath
	a.relayMode = info.RelayMode

	body, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, err
	}
	if err := common.Unmarshal(body, &a.sentRequest); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{a.responseContentType}},
		Body:       io.NopCloser(strings.NewReader(a.responseBody)),
	}, nil
}

func (a *fakeResponsesAdaptor) DoResponse(_ *gin.Context, _ *http.Response, _ *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	return nil, nil
}

func (a *fakeResponsesAdaptor) GetModelList() []string {
	return nil
}

func (a *fakeResponsesAdaptor) GetChannelName() string {
	return "fake"
}

func (a *fakeResponsesAdaptor) ConvertClaudeRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	return request, nil
}

func (a *fakeResponsesAdaptor) ConvertGeminiRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	return request, nil
}

func setupChatCompletionsViaResponsesTest(t *testing.T, stream bool) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *dto.GeneralOpenAIRequest) {
	t.Helper()

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = oldTimeout
	})

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		IsStream:       stream,
		RelayMode:      relayconstant.RelayModeChatCompletions,
		RequestURLPath: "/v1/chat/completions",
		RelayFormat:    types.RelayFormatOpenAI,
		DisablePing:    true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName:    "gpt-test",
			SupportStreamOptions: true,
		},
	}

	return c, recorder, info, chatToolCallingRequest(stream)
}

func chatToolCallingRequest(stream bool) *dto.GeneralOpenAIRequest {
	parallelToolCalls := true
	assistant := dto.Message{
		Role:    "assistant",
		Content: "",
	}
	assistant.SetToolCalls([]dto.ToolCallRequest{
		{
			ID:   "call_weather",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "get_weather",
				Arguments: `{"location":"Paris"}`,
			},
		},
	})

	return &dto.GeneralOpenAIRequest{
		Model:            "gpt-test",
		Stream:           &stream,
		ParallelTooCalls: &parallelToolCalls,
		ToolChoice:       map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}},
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        "get_weather",
					Description: "Get weather by city",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]any{"type": "string"},
						},
						"required": []any{"location"},
					},
				},
			},
		},
		Messages: []dto.Message{
			{Role: "user", Content: "What's the weather in Paris?"},
			assistant,
			{Role: "tool", ToolCallId: "call_weather", Content: `{"temperature":"20C","condition":"sunny"}`},
		},
	}
}

func assertResponsesToolCallingRequest(t *testing.T, request dto.OpenAIResponsesRequest, stream bool) {
	t.Helper()

	if request.Stream == nil || *request.Stream != stream {
		t.Fatalf("expected responses stream=%v, got %#v", stream, request.Stream)
	}

	var input []map[string]any
	if err := common.Unmarshal(request.Input, &input); err != nil {
		t.Fatalf("failed to unmarshal responses input: %v", err)
	}
	if len(input) != 4 {
		t.Fatalf("expected 4 responses input items, got %d: %s", len(input), string(request.Input))
	}
	if got, _ := input[2]["type"].(string); got != "function_call" {
		t.Fatalf("expected third input item to be function_call, got %#v", input[2])
	}
	if got, _ := input[2]["call_id"].(string); got != "call_weather" {
		t.Fatalf("expected function call id call_weather, got %#v", input[2])
	}
	if got, _ := input[2]["arguments"].(string); got != `{"location":"Paris"}` {
		t.Fatalf("expected function call arguments, got %#v", input[2])
	}
	if got, _ := input[3]["type"].(string); got != "function_call_output" {
		t.Fatalf("expected fourth input item to be function_call_output, got %#v", input[3])
	}

	if got := gjson.GetBytes(request.Tools, "0.name").String(); got != "get_weather" {
		t.Fatalf("expected responses tool get_weather, got %s", string(request.Tools))
	}
	if got := gjson.GetBytes(request.ToolChoice, "name").String(); got != "get_weather" {
		t.Fatalf("expected responses tool_choice get_weather, got %s", string(request.ToolChoice))
	}
	var parallelToolCalls bool
	if err := common.Unmarshal(request.ParallelToolCalls, &parallelToolCalls); err != nil {
		t.Fatalf("failed to unmarshal parallel_tool_calls: %v", err)
	}
	if !parallelToolCalls {
		t.Fatalf("expected parallel_tool_calls=true, got %s", string(request.ParallelToolCalls))
	}
}

func assertChatToolCallJSON(t *testing.T, body string) {
	t.Helper()

	if got := gjson.Get(body, "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("expected tool_calls finish reason, got %q in %s", got, body)
	}
	if got := gjson.Get(body, "choices.0.message.tool_calls.0.id").String(); got != "call_weather" {
		t.Fatalf("expected call_weather tool call, got %q in %s", got, body)
	}
	if got := gjson.Get(body, "choices.0.message.tool_calls.0.function.name").String(); got != "get_weather" {
		t.Fatalf("expected get_weather function, got %q in %s", got, body)
	}
	if got := gjson.Get(body, "choices.0.message.tool_calls.0.function.arguments").String(); got != `{"location":"Paris"}` {
		t.Fatalf("expected Paris arguments, got %q in %s", got, body)
	}
}

func assertStreamedChatToolCall(t *testing.T, body string) {
	t.Helper()

	if !strings.Contains(body, `"id":"call_weather"`) ||
		!strings.Contains(body, `"name":"get_weather"`) ||
		!strings.Contains(body, `Paris`) ||
		!strings.Contains(body, `"finish_reason":"tool_calls"`) {
		t.Fatalf("expected streamed chat tool call chunks, got %q", body)
	}
}

func responsesToolCallNonStreamBody() string {
	return `{"id":"resp_tool","created_at":123,"model":"gpt-test","status":"completed","output":[{"type":"function_call","id":"fc_weather","call_id":"call_weather","name":"get_weather","arguments":"{\"location\":\"Paris\"}"}],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}`
}

func responsesToolCallSSEBody() string {
	return strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_weather","call_id":"call_weather","name":"get_weather"}}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_weather","delta":"{\"location\""}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_weather","delta":":\"Paris\"}"}`,
		`data: {"type":"response.completed","response":{"id":"resp_tool","created_at":123,"model":"gpt-test","status":"completed","usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`,
		"data: [DONE]",
		"",
	}, "\n\n")
}

func TestChatCompletionsViaResponsesToolCallNonStream(t *testing.T) {
	c, recorder, info, request := setupChatCompletionsViaResponsesTest(t, false)
	adaptor := &fakeResponsesAdaptor{
		responseBody:        responsesToolCallNonStreamBody(),
		responseContentType: "application/json",
	}

	usage, err := chatCompletionsViaResponses(c, info, adaptor, request)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 14 {
		t.Fatalf("expected usage total 14, got %#v", usage)
	}
	assertResponsesToolCallingRequest(t, adaptor.convertedRequest, false)
	assertResponsesToolCallingRequest(t, adaptor.sentRequest, false)
	if adaptor.requestURLPath != "/v1/responses" {
		t.Fatalf("expected upstream request path /v1/responses, got %q", adaptor.requestURLPath)
	}
	if adaptor.relayMode != relayconstant.RelayModeResponses {
		t.Fatalf("expected upstream relay mode responses, got %d", adaptor.relayMode)
	}
	assertChatToolCallJSON(t, recorder.Body.String())
	if info.RelayMode != relayconstant.RelayModeChatCompletions {
		t.Fatalf("expected relay mode restored to chat completions, got %d", info.RelayMode)
	}
	if info.RequestURLPath != "/v1/chat/completions" {
		t.Fatalf("expected request URL path restored, got %q", info.RequestURLPath)
	}
}

func TestChatCompletionsViaResponsesToolCallStream(t *testing.T) {
	c, recorder, info, request := setupChatCompletionsViaResponsesTest(t, true)
	adaptor := &fakeResponsesAdaptor{
		responseBody:        responsesToolCallSSEBody(),
		responseContentType: "text/event-stream",
	}

	usage, err := chatCompletionsViaResponses(c, info, adaptor, request)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 14 {
		t.Fatalf("expected usage total 14, got %#v", usage)
	}
	assertResponsesToolCallingRequest(t, adaptor.convertedRequest, true)
	assertResponsesToolCallingRequest(t, adaptor.sentRequest, true)
	if adaptor.requestURLPath != "/v1/responses" {
		t.Fatalf("expected upstream request path /v1/responses, got %q", adaptor.requestURLPath)
	}
	if adaptor.relayMode != relayconstant.RelayModeResponses {
		t.Fatalf("expected upstream relay mode responses, got %d", adaptor.relayMode)
	}
	assertStreamedChatToolCall(t, recorder.Body.String())
	if info.RelayMode != relayconstant.RelayModeChatCompletions {
		t.Fatalf("expected relay mode restored to chat completions, got %d", info.RelayMode)
	}
	if info.RequestURLPath != "/v1/chat/completions" {
		t.Fatalf("expected request URL path restored, got %q", info.RequestURLPath)
	}
}
