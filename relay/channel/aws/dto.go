package aws

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type AwsClaudeRequest struct {
	// AnthropicVersion should be "bedrock-2023-05-31"
	AnthropicVersion  string              `json:"anthropic_version"`
	AnthropicBeta     json.RawMessage     `json:"anthropic_beta,omitempty"`
	System            any                 `json:"system,omitempty"`
	Messages          []dto.ClaudeMessage `json:"messages"`
	MaxTokens         *uint               `json:"max_tokens,omitempty"`
	MaxTokensToSample *uint               `json:"max_tokens_to_sample,omitempty"`
	Temperature       *float64            `json:"temperature,omitempty"`
	TopP              *float64            `json:"top_p,omitempty"`
	TopK              *int                `json:"top_k,omitempty"`
	StopSequences     []string            `json:"stop_sequences,omitempty"`
	Tools             any                 `json:"tools,omitempty"`
	ToolChoice        any                 `json:"tool_choice,omitempty"`
	ContextManagement json.RawMessage     `json:"context_management,omitempty"`
	Thinking          *dto.Thinking       `json:"thinking,omitempty"`
	OutputConfig      json.RawMessage     `json:"output_config,omitempty"`
	//Metadata         json.RawMessage     `json:"metadata,omitempty"`
}

func formatRequest(requestBody io.Reader, requestHeader http.Header) (*AwsClaudeRequest, error) {
	var awsClaudeRequest AwsClaudeRequest
	err := common.DecodeJson(requestBody, &awsClaudeRequest)
	if err != nil {
		return nil, err
	}
	if awsClaudeRequest.MaxTokens == nil {
		awsClaudeRequest.MaxTokens = awsClaudeRequest.MaxTokensToSample
	}
	awsClaudeRequest.MaxTokensToSample = nil
	awsClaudeRequest.AnthropicVersion = "bedrock-2023-05-31"

	// check header anthropic-beta
	anthropicBetaValues := requestHeader.Get("anthropic-beta")
	if len(anthropicBetaValues) > 0 {
		var tempArray []string
		tempArray = strings.Split(anthropicBetaValues, ",")
		if len(tempArray) > 0 {
			betaJson, err := common.Marshal(tempArray)
			if err != nil {
				return nil, err
			}
			awsClaudeRequest.AnthropicBeta = betaJson
		}
	}
	return &awsClaudeRequest, nil
}

// NovaMessage Nova模型使用messages-v1格式
type NovaMessage struct {
	Role    string        `json:"role"`
	Content []NovaContent `json:"content"`
}

type NovaContent struct {
	Text string `json:"text"`
}

type NovaRequest struct {
	SchemaVersion   string               `json:"schemaVersion"`             // 请求版本，例如 "1.0"
	System          []NovaContent        `json:"system,omitempty"`          // 系统提示
	Messages        []NovaMessage        `json:"messages"`                  // 对话消息列表
	InferenceConfig *NovaInferenceConfig `json:"inferenceConfig,omitempty"` // 推理配置，可选
}

type NovaInferenceConfig struct {
	MaxTokens     *int     `json:"maxTokens,omitempty"`     // 最大生成的 token 数
	Temperature   *float64 `json:"temperature,omitempty"`   // 随机性 (默认 0.7, 范围 0-1)
	TopP          *float64 `json:"topP,omitempty"`          // nucleus sampling (默认 0.9, 范围 0-1)
	TopK          *int     `json:"topK,omitempty"`          // 限制候选 token 数 (默认 50, 范围 0-128)
	StopSequences []string `json:"stopSequences,omitempty"` // 停止生成的序列
}

type NovaUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

type NovaResponseContent struct {
	Text string `json:"text,omitempty"`
}

type NovaResponse struct {
	Output struct {
		Message struct {
			Content []NovaResponseContent `json:"content"`
		} `json:"message"`
	} `json:"output"`
	StopReason string    `json:"stopReason"`
	Usage      NovaUsage `json:"usage"`
}

type NovaStreamEvent struct {
	ContentBlockDelta *struct {
		Delta struct {
			Text string `json:"text,omitempty"`
		} `json:"delta"`
	} `json:"contentBlockDelta,omitempty"`
	MessageStop *struct {
		StopReason string `json:"stopReason"`
	} `json:"messageStop,omitempty"`
	Metadata *struct {
		Usage NovaUsage `json:"usage"`
	} `json:"metadata,omitempty"`
}

// 转换OpenAI请求为Nova格式
func convertToNovaRequest(req *dto.GeneralOpenAIRequest) (*NovaRequest, error) {
	if len(req.Tools) > 0 {
		return nil, fmt.Errorf("AWS Nova tool calling is not supported by this adapter")
	}

	novaMessages := make([]NovaMessage, 0, len(req.Messages))
	system := make([]NovaContent, 0, 1)
	for _, msg := range req.Messages {
		if len(msg.ToolCalls) > 0 || msg.ToolCallId != "" {
			return nil, fmt.Errorf("AWS Nova tool messages are not supported by this adapter")
		}
		text := msg.StringContent()
		if !msg.IsStringContent() {
			var textBuilder strings.Builder
			for _, part := range msg.ParseContent() {
				if part.Type != dto.ContentTypeText {
					return nil, fmt.Errorf("AWS Nova %s content is not supported by this adapter", part.Type)
				}
				textBuilder.WriteString(part.Text)
			}
			text = textBuilder.String()
		}

		content := NovaContent{Text: text}
		switch strings.ToLower(msg.Role) {
		case "system", "developer":
			system = append(system, content)
		case "user", "assistant":
			novaMessages = append(novaMessages, NovaMessage{
				Role:    strings.ToLower(msg.Role),
				Content: []NovaContent{content},
			})
		default:
			return nil, fmt.Errorf("AWS Nova message role %q is not supported", msg.Role)
		}
	}

	novaReq := &NovaRequest{
		SchemaVersion: "messages-v1",
		System:        system,
		Messages:      novaMessages,
	}

	// 设置推理配置。指针字段保留客户端显式提供的 0；省略字段才使用
	// Bedrock 的默认值。
	if req.MaxCompletionTokens != nil || req.MaxTokens != nil || req.Temperature != nil || req.TopP != nil || req.TopK != nil || req.Stop != nil {
		novaReq.InferenceConfig = &NovaInferenceConfig{}
		maxTokens := req.MaxCompletionTokens
		if maxTokens == nil {
			maxTokens = req.MaxTokens
		}
		if maxTokens != nil {
			value := int(*maxTokens)
			novaReq.InferenceConfig.MaxTokens = &value
		}
		novaReq.InferenceConfig.Temperature = req.Temperature
		novaReq.InferenceConfig.TopP = req.TopP
		novaReq.InferenceConfig.TopK = req.TopK
		if req.Stop != nil {
			if stopSequences := parseStopSequences(req.Stop); len(stopSequences) > 0 {
				novaReq.InferenceConfig.StopSequences = stopSequences
			}
		}
	}

	return novaReq, nil
}

// parseStopSequences 解析停止序列，支持字符串或字符串数组
func parseStopSequences(stop any) []string {
	if stop == nil {
		return nil
	}

	switch v := stop.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []string:
		return v
	case []interface{}:
		var sequences []string
		for _, item := range v {
			if str, ok := item.(string); ok && str != "" {
				sequences = append(sequences, str)
			}
		}
		return sequences
	}
	return nil
}
