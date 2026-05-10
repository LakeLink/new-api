package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
)

func TestMapResponsesTerminalStatusToFinishReason(t *testing.T) {
	tests := []struct {
		name             string
		status           string
		incompleteReason string
		hasToolCalls     bool
		want             string
	}{
		{
			name:         "completed tool call",
			status:       "completed",
			hasToolCalls: true,
			want:         constant.FinishReasonToolCalls,
		},
		{
			name:             "incomplete max output tokens",
			status:           "incomplete",
			incompleteReason: "max_output_tokens",
			hasToolCalls:     true,
			want:             constant.FinishReasonLength,
		},
		{
			name:             "incomplete content filter",
			status:           "incomplete",
			incompleteReason: "content_filter",
			want:             constant.FinishReasonContentFilter,
		},
		{
			name:         "missing status falls back to tool calls",
			hasToolCalls: true,
			want:         constant.FinishReasonToolCalls,
		},
		{
			name:         "failed status does not fall back to tool calls",
			status:       "failed",
			hasToolCalls: true,
			want:         constant.FinishReasonStop,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapResponsesTerminalStatusToFinishReason(tt.status, tt.incompleteReason, tt.hasToolCalls)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestResponsesResponseToChatCompletionsResponsePreservesTextAndToolCalls(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		ID:        "resp_mixed",
		CreatedAt: 123,
		Model:     "gpt-test",
		Output: []dto.ResponsesOutput{
			{
				Type: "message",
				Role: "assistant",
				Content: []dto.ResponsesOutputContent{
					{
						Type: "output_text",
						Text: "Let me check.",
					},
				},
			},
			{
				Type:      "function_call",
				ID:        "fc_weather",
				CallId:    "call_weather",
				Name:      "get_weather",
				Arguments: []byte(`"{\"location\":\"Paris\"}"`),
			},
		},
	}

	got, _, err := ResponsesResponseToChatCompletionsResponse(resp, "chatcmpl-test")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("expected one choice, got %#v", got.Choices)
	}
	choice := got.Choices[0]
	if choice.FinishReason != constant.FinishReasonToolCalls {
		t.Fatalf("expected tool_calls finish reason, got %q", choice.FinishReason)
	}
	if choice.Message.StringContent() != "Let me check." {
		t.Fatalf("expected assistant text to be preserved, got %q", choice.Message.StringContent())
	}
	toolCalls := choice.Message.ParseToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", toolCalls)
	}
	if toolCalls[0].ID != "call_weather" {
		t.Fatalf("expected tool call id call_weather, got %q", toolCalls[0].ID)
	}
	if toolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("expected function name get_weather, got %q", toolCalls[0].Function.Name)
	}
	if toolCalls[0].Function.Arguments != `{"location":"Paris"}` {
		t.Fatalf("expected Paris arguments, got %q", toolCalls[0].Function.Arguments)
	}
}
