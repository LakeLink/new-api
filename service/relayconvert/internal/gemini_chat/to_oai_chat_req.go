package geminichat

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/relayconvert/internal/jsonutil"
	relaymeta "github.com/QuantumNous/new-api/service/relayconvert/internal/meta"
)

func GeminiGenerateContentRequestToOpenAIChat(geminiRequest *dto.GeminiChatRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	modelName := ""
	isStream := false
	if info != nil {
		isStream = info.IsStream
	}
	modelName = relaymeta.RelayInfoUpstreamModelName(info)
	openaiRequest := &dto.GeneralOpenAIRequest{
		Model:  modelName,
		Stream: common.GetPointer(isStream),
	}
	if geminiRequest.ServiceTier != nil {
		serviceTier, ok := dto.NormalizeGeminiServiceTier(*geminiRequest.ServiceTier)
		if !ok {
			return nil, fmt.Errorf("invalid Gemini serviceTier %q", *geminiRequest.ServiceTier)
		}
		switch serviceTier {
		case dto.GeminiServiceTierUnspecified:
			serviceTier = "auto"
		case dto.GeminiServiceTierStandard:
			serviceTier = "default"
		}
		encodedServiceTier, err := common.Marshal(serviceTier)
		if err != nil {
			return nil, fmt.Errorf("encode OpenAI service_tier: %w", err)
		}
		openaiRequest.ServiceTier = encodedServiceTier
	}
	if geminiRequest.Store != nil {
		encodedStore, err := common.Marshal(*geminiRequest.Store)
		if err != nil {
			return nil, fmt.Errorf("encode OpenAI store: %w", err)
		}
		openaiRequest.Store = encodedStore
	}

	var messages []dto.Message
	for _, content := range geminiRequest.Contents {
		message := dto.Message{
			Role: convertGeminiRoleToOpenAI(content.Role),
		}

		var mediaContents []dto.MediaContent
		var toolCalls []dto.ToolCallRequest
		for _, part := range content.Parts {
			if part.Text != "" {
				mediaContent := dto.MediaContent{
					Type: "text",
					Text: part.Text,
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.InlineData != nil {
				mediaContent := dto.MediaContent{
					Type: "image_url",
					ImageUrl: &dto.MessageImageUrl{
						Url:      fmt.Sprintf("data:%s;base64,%s", part.InlineData.MimeType, part.InlineData.Data),
						Detail:   "auto",
						MimeType: part.InlineData.MimeType,
					},
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.FileData != nil {
				mediaContent := dto.MediaContent{
					Type: "image_url",
					ImageUrl: &dto.MessageImageUrl{
						Url:      part.FileData.FileUri,
						Detail:   "auto",
						MimeType: part.FileData.MimeType,
					},
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.FunctionCall != nil {
				toolCall := dto.ToolCallRequest{
					ID:   fmt.Sprintf("call_%d", len(toolCalls)+1),
					Type: "function",
					Function: dto.FunctionRequest{
						Name:      part.FunctionCall.FunctionName,
						Arguments: jsonutil.ToJSONString(part.FunctionCall.Arguments),
					},
				}
				toolCalls = append(toolCalls, toolCall)
			} else if part.FunctionResponse != nil {
				toolMessage := dto.Message{
					Role:       "tool",
					ToolCallId: fmt.Sprintf("call_%d", len(toolCalls)),
				}
				toolMessage.SetStringContent(jsonutil.ToJSONString(part.FunctionResponse.Response))
				messages = append(messages, toolMessage)
			}
		}

		if len(toolCalls) > 0 {
			message.SetToolCalls(toolCalls)
		} else if len(mediaContents) == 1 && mediaContents[0].Type == "text" {
			message.Content = mediaContents[0].Text
		} else if len(mediaContents) > 0 {
			message.SetMediaContent(mediaContents)
		}

		if len(message.ParseContent()) > 0 || len(message.ToolCalls) > 0 {
			messages = append(messages, message)
		}
	}

	openaiRequest.Messages = messages

	openaiRequest.Temperature = geminiRequest.GenerationConfig.Temperature
	openaiRequest.TopP = geminiRequest.GenerationConfig.TopP
	openaiRequest.TopK = geminiRequest.GenerationConfig.TopK
	openaiRequest.MaxTokens = geminiRequest.GenerationConfig.MaxOutputTokens
	openaiRequest.N = geminiRequest.GenerationConfig.CandidateCount
	openaiRequest.LogProbs = geminiRequest.GenerationConfig.ResponseLogprobs
	openaiRequest.Seed = geminiRequest.GenerationConfig.Seed
	if geminiRequest.GenerationConfig.Logprobs != nil {
		topLogProbs := int(*geminiRequest.GenerationConfig.Logprobs)
		openaiRequest.TopLogProbs = &topLogProbs
	}
	openaiRequest.PresencePenalty = geminiRequest.GenerationConfig.PresencePenalty
	openaiRequest.FrequencyPenalty = geminiRequest.GenerationConfig.FrequencyPenalty
	if len(geminiRequest.GenerationConfig.StopSequences) > 0 {
		openaiRequest.Stop = geminiRequest.GenerationConfig.StopSequences[:min(len(geminiRequest.GenerationConfig.StopSequences), 4)]
	}

	if len(geminiRequest.GetTools()) > 0 {
		var tools []dto.ToolCallRequest
		for _, tool := range geminiRequest.GetTools() {
			if tool.FunctionDeclarations == nil {
				continue
			}
			functionDeclarations, err := common.Any2Type[[]dto.FunctionRequest](tool.FunctionDeclarations)
			if err != nil {
				common.SysError(fmt.Sprintf("failed to parse gemini function declarations: %v (type=%T)", err, tool.FunctionDeclarations))
				continue
			}
			for _, function := range functionDeclarations {
				openAITool := dto.ToolCallRequest{
					Type: "function",
					Function: dto.FunctionRequest{
						Name:        function.Name,
						Description: function.Description,
						Parameters:  function.Parameters,
					},
				}
				tools = append(tools, openAITool)
			}
		}
		if len(tools) > 0 {
			openaiRequest.Tools = tools
		}
	}

	if toolConfig := geminiRequest.ToolConfig; toolConfig != nil && toolConfig.FunctionCallingConfig != nil {
		functionConfig := toolConfig.FunctionCallingConfig
		mode := strings.ToUpper(strings.TrimSpace(string(functionConfig.Mode)))
		declaredFunctions := make(map[string]struct{}, len(openaiRequest.Tools))
		for _, tool := range openaiRequest.Tools {
			if tool.Type == "function" && tool.Function.Name != "" {
				declaredFunctions[tool.Function.Name] = struct{}{}
			}
		}

		allowedNames := make([]string, 0, len(functionConfig.AllowedFunctionNames))
		for index, name := range functionConfig.AllowedFunctionNames {
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, fmt.Errorf("toolConfig.functionCallingConfig.allowedFunctionNames[%d] is empty", index)
			}
			if _, exists := declaredFunctions[name]; !exists {
				return nil, fmt.Errorf(
					"toolConfig.functionCallingConfig.allowedFunctionNames[%d] references undeclared function %q",
					index,
					name,
				)
			}
			allowedNames = append(allowedNames, name)
		}

		if len(allowedNames) > 0 && mode != "ANY" && mode != "VALIDATED" {
			return nil, errors.New("allowedFunctionNames is only valid for ANY or VALIDATED function calling mode")
		}

		switch mode {
		case "":
		case "AUTO":
			openaiRequest.ToolChoice = "auto"
		case "NONE":
			openaiRequest.ToolChoice = "none"
		case "ANY", "VALIDATED":
			if len(declaredFunctions) == 0 {
				return nil, fmt.Errorf("function calling mode %s requires at least one function declaration", mode)
			}
			openAIMode := "required"
			if mode == "VALIDATED" {
				openAIMode = "auto"
			}
			if len(allowedNames) == 0 {
				openaiRequest.ToolChoice = openAIMode
				break
			}
			allowedTools := make([]map[string]any, 0, len(allowedNames))
			for _, name := range allowedNames {
				allowedTools = append(allowedTools, map[string]any{
					"type": "function",
					"name": name,
				})
			}
			openaiRequest.ToolChoice = map[string]any{
				"type":  "allowed_tools",
				"mode":  openAIMode,
				"tools": allowedTools,
			}
		default:
			return nil, fmt.Errorf("unsupported Gemini function calling mode %q", functionConfig.Mode)
		}
	}

	if geminiRequest.SystemInstructions != nil {
		systemMessage := dto.Message{
			Role:    "system",
			Content: extractTextFromGeminiParts(geminiRequest.SystemInstructions.Parts),
		}
		openaiRequest.Messages = append([]dto.Message{systemMessage}, openaiRequest.Messages...)
	}

	return openaiRequest, nil
}

func convertGeminiRoleToOpenAI(geminiRole string) string {
	switch geminiRole {
	case "user":
		return "user"
	case "model":
		return "assistant"
	case "function":
		return "function"
	default:
		return "user"
	}
}

func extractTextFromGeminiParts(parts []dto.GeminiPart) string {
	texts := make([]string, 0)
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}
