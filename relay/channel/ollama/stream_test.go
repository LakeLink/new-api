package ollama

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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaChatHandlerNonStreamToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "compact json per-line parse path",
			raw:  `{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Paris","days":0}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`,
		},
		{
			name: "pretty json fallback parse path",
			raw: `{
  "model": "llama3.1",
  "created_at": "2026-05-27T12:00:00Z",
  "message": {
    "role": "assistant",
    "content": "",
    "tool_calls": [
      {
        "function": {
          "name": "get_weather",
          "arguments": {
            "city": "Paris",
            "days": 0
          }
        }
      }
    ]
  },
  "done": true,
  "done_reason": "stop",
  "prompt_eval_count": 5,
  "eval_count": 7
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.raw)),
			}

			usage, apiErr := ollamaChatHandler(c, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
			}, resp)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 12, usage.TotalTokens)

			var out dto.OpenAITextResponse
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
			require.Len(t, out.Choices, 1)
			assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)

			var toolCalls []dto.ToolCallResponse
			require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &toolCalls))
			require.Len(t, toolCalls, 1)
			assert.NotEmpty(t, toolCalls[0].ID)
			assert.Equal(t, "function", toolCalls[0].Type)
			assert.Equal(t, "get_weather", toolCalls[0].Function.Name)
			assert.Nil(t, toolCalls[0].Index)

			var args map[string]any
			require.NoError(t, common.Unmarshal([]byte(toolCalls[0].Function.Arguments), &args))
			assert.Equal(t, "Paris", args["city"])
			assert.Equal(t, float64(0), args["days"])
		})
	}
}

func TestOpenAIResponseFormatToOllama(t *testing.T) {
	for _, test := range []struct {
		name    string
		format  *dto.ResponseFormat
		want    any
		wantErr string
	}{
		{
			name:   "json object uses Ollama JSON mode",
			format: &dto.ResponseFormat{Type: "json_object"},
			want:   "json",
		},
		{
			name: "OpenAI schema wrapper is unwrapped",
			format: &dto.ResponseFormat{
				Type:       "json_schema",
				JsonSchema: []byte(`{"name":"answer","strict":true,"schema":{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"]}}`),
			},
			want: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "integer"},
				},
				"required": []any{"value"},
			},
		},
		{
			name:    "missing nested schema is rejected",
			format:  &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"answer"}`)},
			wantErr: "json_schema.schema is required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := openAIResponseFormatToOllama(test.format)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestOllamaCompletionHandlerUsesLegacyCompletionContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			`{"model":"llama3.2","created_at":"2026-05-27T12:00:00Z","response":"plain completion","done":true,"done_reason":"stop","prompt_eval_count":4,"eval_count":2}`,
		)),
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "llama3.2",
		},
	}

	usage, apiErr := ollamaChatHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 6, usage.TotalTokens)

	var payload map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &payload))
	assert.Equal(t, "text_completion", payload["object"])
	choices, ok := payload["choices"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, choices)
	choice, ok := choices[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "plain completion", choice["text"])
	assert.NotContains(t, choice, "message")
	assert.NotContains(t, choice, "delta")
	assert.Nil(t, choice["logprobs"])
}

func TestOllamaCompletionStreamUsesTextChoices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/completions", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			"{\"model\":\"llama3.2\",\"created_at\":\"2026-05-27T12:00:00Z\",\"response\":\"hello\",\"done\":false}\n" +
				"{\"model\":\"llama3.2\",\"created_at\":\"2026-05-27T12:00:01Z\",\"done\":true,\"done_reason\":\"stop\",\"prompt_eval_count\":2,\"eval_count\":1}\n",
		)),
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "llama3.2",
		},
	}

	usage, apiErr := ollamaStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 3, usage.TotalTokens)
	body := w.Body.String()
	assert.Contains(t, body, `"object":"text_completion"`)
	assert.Contains(t, body, `"text":"hello"`)
	assert.NotContains(t, body, `"delta"`)
	assert.NotContains(t, body, `"message"`)
	assert.Contains(t, body, "data: [DONE]")
}

func TestOllamaStreamEmitsUsageOnlyWhenRequested(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, test := range []struct {
		name         string
		relayMode    int
		includeUsage bool
	}{
		{name: "chat omits usage by default", relayMode: relayconstant.RelayModeChatCompletions},
		{name: "chat includes requested usage", relayMode: relayconstant.RelayModeChatCompletions, includeUsage: true},
		{name: "completion omits usage by default", relayMode: relayconstant.RelayModeCompletions},
		{name: "completion includes requested usage", relayMode: relayconstant.RelayModeCompletions, includeUsage: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"{\"model\":\"llama3.2\",\"created_at\":\"2026-05-27T12:00:00Z\",\"message\":{\"role\":\"assistant\",\"content\":\"hello\"},\"response\":\"hello\",\"done\":false}\n" +
						"{\"model\":\"llama3.2\",\"created_at\":\"2026-05-27T12:00:01Z\",\"done\":true,\"done_reason\":\"stop\",\"prompt_eval_count\":2,\"eval_count\":1}\n",
				)),
			}
			info := &relaycommon.RelayInfo{
				RelayMode:          test.relayMode,
				ShouldIncludeUsage: test.includeUsage,
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: "llama3.2",
				},
			}

			usage, apiErr := ollamaStreamHandler(c, info, resp)

			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 3, usage.TotalTokens)
			assert.Equal(t, test.includeUsage, strings.Contains(w.Body.String(), `"prompt_tokens":2`))
		})
	}
}

func TestOllamaStreamSurfacesUpstreamErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("error before output returns gateway error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{\"error\":\"model failed\"}\n"))}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.2"}}

		_, apiErr := ollamaStreamHandler(c, info, resp)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
		assert.Empty(t, w.Body.String())
		assert.Empty(t, w.Header().Get("Content-Type"))
	})

	t.Run("midstream error is emitted as OpenAI error event", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				"{\"model\":\"llama3.2\",\"message\":{\"role\":\"assistant\",\"content\":\"partial\"},\"done\":false}\n" +
					"{\"error\":\"runner crashed\"}\n",
			)),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.2"}}

		_, apiErr := ollamaStreamHandler(c, info, resp)
		require.Nil(t, apiErr)
		body := w.Body.String()
		assert.Contains(t, body, `"code":"ollama_error"`)
		assert.Contains(t, body, `"message":"runner crashed"`)
		assert.Contains(t, body, "data: [DONE]")
		assert.NotContains(t, body, `"finish_reason":"stop"`)
	})
}

func TestOllamaNonStreamErrorIsNotEmptySuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"model failed"}`))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.2"}}

	_, apiErr := ollamaChatHandler(c, info, resp)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.Empty(t, w.Body.String())
}
