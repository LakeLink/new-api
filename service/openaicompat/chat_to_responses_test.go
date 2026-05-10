package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

func TestChatCompletionsRequestToResponsesRequestConvertsToolCallingConversation(t *testing.T) {
	tests := []struct {
		name   string
		stream bool
	}{
		{name: "non-stream", stream: false},
		{name: "stream", stream: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parallelToolCalls := true
			req := &dto.GeneralOpenAIRequest{
				Model:               "gpt-test",
				Stream:              &tt.stream,
				ParallelTooCalls:    &parallelToolCalls,
				ToolChoice:          map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}},
				Tools:               weatherTools(),
				Messages:            weatherToolConversation(),
				MaxCompletionTokens: common.GetPointer[uint](128),
			}

			got, err := ChatCompletionsRequestToResponsesRequest(req)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got.Stream == nil || *got.Stream != tt.stream {
				t.Fatalf("expected stream=%v, got %#v", tt.stream, got.Stream)
			}
			if got.MaxOutputTokens == nil || *got.MaxOutputTokens != 128 {
				t.Fatalf("expected max_output_tokens=128, got %#v", got.MaxOutputTokens)
			}

			var input []map[string]any
			if err := common.Unmarshal(got.Input, &input); err != nil {
				t.Fatalf("failed to unmarshal responses input: %v", err)
			}
			if len(input) != 4 {
				t.Fatalf("expected 4 input items, got %d: %s", len(input), string(got.Input))
			}
			assertMapString(t, input[0], "role", "user")
			assertMapString(t, input[0], "content", "What's the weather in Paris?")
			assertMapString(t, input[1], "role", "assistant")
			assertMapString(t, input[1], "content", "")
			assertMapString(t, input[2], "type", "function_call")
			assertMapString(t, input[2], "call_id", "call_weather")
			assertMapString(t, input[2], "name", "get_weather")
			assertMapString(t, input[2], "arguments", `{"location":"Paris"}`)
			assertMapString(t, input[3], "type", "function_call_output")
			assertMapString(t, input[3], "call_id", "call_weather")
			assertMapString(t, input[3], "output", `{"temperature":"20C","condition":"sunny"}`)

			var tools []map[string]any
			if err := common.Unmarshal(got.Tools, &tools); err != nil {
				t.Fatalf("failed to unmarshal responses tools: %v", err)
			}
			if len(tools) != 1 {
				t.Fatalf("expected 1 tool, got %d: %s", len(tools), string(got.Tools))
			}
			assertMapString(t, tools[0], "type", "function")
			assertMapString(t, tools[0], "name", "get_weather")
			assertMapString(t, tools[0], "description", "Get weather by city")
			if _, ok := tools[0]["parameters"].(map[string]any); !ok {
				t.Fatalf("expected tool parameters object, got %#v", tools[0]["parameters"])
			}

			var toolChoice map[string]any
			if err := common.Unmarshal(got.ToolChoice, &toolChoice); err != nil {
				t.Fatalf("failed to unmarshal tool_choice: %v", err)
			}
			assertMapString(t, toolChoice, "type", "function")
			assertMapString(t, toolChoice, "name", "get_weather")

			var convertedParallelToolCalls bool
			if err := common.Unmarshal(got.ParallelToolCalls, &convertedParallelToolCalls); err != nil {
				t.Fatalf("failed to unmarshal parallel_tool_calls: %v", err)
			}
			if !convertedParallelToolCalls {
				t.Fatal("expected parallel_tool_calls=true")
			}
		})
	}
}

func weatherTools() []dto.ToolCallRequest {
	return []dto.ToolCallRequest{
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
	}
}

func weatherToolConversation() []dto.Message {
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

	return []dto.Message{
		{
			Role:    "user",
			Content: "What's the weather in Paris?",
		},
		assistant,
		{
			Role:       "tool",
			ToolCallId: "call_weather",
			Content:    `{"temperature":"20C","condition":"sunny"}`,
		},
	}
}

func assertMapString(t *testing.T, m map[string]any, key string, want string) {
	t.Helper()
	got, ok := m[key].(string)
	if !ok {
		t.Fatalf("expected %q to be a string, got %#v", key, m[key])
	}
	if got != want {
		t.Fatalf("expected %s=%q, got %q", key, want, got)
	}
}
