package oaichat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaymedia "github.com/QuantumNous/new-api/service/relayconvert/internal/media"
	sharedclaude "github.com/QuantumNous/new-api/service/relayconvert/internal/shared/claude"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const (
	webSearchMaxUsesLow    = 1
	webSearchMaxUsesMedium = 5
	webSearchMaxUsesHigh   = 10
)

type openRouterRequestReasoning struct {
	Enabled   bool   `json:"enabled"`
	Effort    string `json:"effort,omitempty"`
	MaxTokens *int   `json:"max_tokens,omitempty"`
	Exclude   bool   `json:"exclude,omitempty"`
}

// openAIChatMaxTokens preserves which optional max-token field the client
// supplied, including an explicit zero. max_completion_tokens is the newer
// field and takes precedence when both are present.
func openAIChatMaxTokens(request dto.GeneralOpenAIRequest) *uint {
	if request.MaxCompletionTokens != nil {
		return request.MaxCompletionTokens
	}
	return request.MaxTokens
}

func OpenAIChatRequestToClaudeMessages(c *gin.Context, textRequest dto.GeneralOpenAIRequest) (*dto.ClaudeRequest, error) {
	claudeTools := make([]any, 0, len(textRequest.Tools))

	for index, tool := range textRequest.Tools {
		if tool.Type != "" && tool.Type != "function" {
			return nil, fmt.Errorf("tools[%d].type %q is not supported by Claude Messages", index, tool.Type)
		}
		if strings.TrimSpace(tool.Function.Name) == "" {
			return nil, fmt.Errorf("tools[%d].function.name is required", index)
		}
		inputSchema := map[string]any{"type": "object"}
		if tool.Function.Parameters != nil {
			var err error
			inputSchema, err = common.Any2Type[map[string]any](tool.Function.Parameters)
			if err != nil || inputSchema == nil {
				return nil, fmt.Errorf("tools[%d].function.parameters must be a JSON object", index)
			}
			schemaType, exists := inputSchema["type"]
			if !exists || schemaType == nil {
				inputSchema["type"] = "object"
			} else if schemaTypeString, ok := schemaType.(string); !ok || schemaTypeString != "object" {
				return nil, fmt.Errorf("tools[%d].function.parameters.type must be object", index)
			}
		}
		claudeTools = append(claudeTools, &dto.Tool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: inputSchema,
			Strict:      tool.Function.Strict,
		})
	}

	if textRequest.WebSearchOptions != nil {
		webSearchTool := dto.ClaudeWebSearchTool{
			Type: "web_search_20250305",
			Name: "web_search",
		}

		if textRequest.WebSearchOptions.UserLocation != nil {
			anthropicUserLocation := &dto.ClaudeWebSearchUserLocation{
				Type: "approximate",
			}

			var userLocationMap map[string]interface{}
			if err := common.Unmarshal(textRequest.WebSearchOptions.UserLocation, &userLocationMap); err == nil {
				if approximateData, ok := userLocationMap["approximate"].(map[string]interface{}); ok {
					if timezone, ok := approximateData["timezone"].(string); ok && timezone != "" {
						anthropicUserLocation.Timezone = timezone
					}
					if country, ok := approximateData["country"].(string); ok && country != "" {
						anthropicUserLocation.Country = country
					}
					if region, ok := approximateData["region"].(string); ok && region != "" {
						anthropicUserLocation.Region = region
					}
					if city, ok := approximateData["city"].(string); ok && city != "" {
						anthropicUserLocation.City = city
					}
				}
			}

			webSearchTool.UserLocation = anthropicUserLocation
		}

		switch textRequest.WebSearchOptions.SearchContextSize {
		case "low":
			webSearchTool.MaxUses = common.GetPointer(uint(webSearchMaxUsesLow))
		case "", "medium":
			webSearchTool.MaxUses = common.GetPointer(uint(webSearchMaxUsesMedium))
		case "high":
			webSearchTool.MaxUses = common.GetPointer(uint(webSearchMaxUsesHigh))
		default:
			return nil, fmt.Errorf("web_search_options.search_context_size %q is invalid", textRequest.WebSearchOptions.SearchContextSize)
		}

		claudeTools = append(claudeTools, &webSearchTool)
	}

	claudeRequest := dto.ClaudeRequest{
		Model:         textRequest.Model,
		StopSequences: nil,
		Temperature:   textRequest.Temperature,
		MaxTokens:     openAIChatMaxTokens(textRequest),
		TopP:          textRequest.TopP,
		Tools:         claudeTools,
	}
	if textRequest.TopK != nil {
		claudeRequest.TopK = common.GetPointer(*textRequest.TopK)
	}
	if textRequest.IsStream(nil) {
		claudeRequest.Stream = common.GetPointer(true)
	}

	if textRequest.ToolChoice != nil || textRequest.ParallelTooCalls != nil {
		claudeToolChoice := sharedclaude.MapOpenAIToolChoice(textRequest.ToolChoice, textRequest.ParallelTooCalls)
		if claudeToolChoice != nil {
			claudeRequest.ToolChoice = claudeToolChoice
		}
	}

	if claudeRequest.MaxTokens == nil {
		defaultMaxTokens := uint(model_setting.GetClaudeSettings().GetDefaultMaxTokens(textRequest.Model))
		claudeRequest.MaxTokens = &defaultMaxTokens
	}

	if baseModel, effortLevel, ok := reasoning.ParseClaudeEffortSuffix(textRequest.Model); ok &&
		(strings.HasPrefix(baseModel, "claude-opus-4-6") ||
			reasoning.IsClaudeAdaptiveThinkingOnlyModel(baseModel)) {
		claudeRequest.Model = baseModel
		claudeRequest.Thinking = &dto.Thinking{
			Type: "adaptive",
		}
		claudeRequest.OutputConfig = json.RawMessage(fmt.Sprintf(`{"effort":"%s"}`, effortLevel))
		if reasoning.IsClaudeSamplingRestrictedModel(baseModel) {
			claudeRequest.Thinking.Display = "summarized"
			claudeRequest.Temperature = nil
			claudeRequest.TopP = nil
			claudeRequest.TopK = nil
		} else {
			claudeRequest.TopP = nil
			claudeRequest.Temperature = common.GetPointer[float64](1.0)
		}
	} else if model_setting.GetClaudeSettings().ThinkingAdapterEnabled &&
		strings.HasSuffix(textRequest.Model, "-thinking") {

		trimmedModel := strings.TrimSuffix(textRequest.Model, "-thinking")
		if reasoning.IsClaudeAdaptiveThinkingOnlyModel(trimmedModel) {
			claudeRequest.Thinking = &dto.Thinking{Type: "adaptive", Display: "summarized"}
			claudeRequest.OutputConfig = json.RawMessage(`{"effort":"high"}`)
			claudeRequest.Temperature = nil
			claudeRequest.TopP = nil
			claudeRequest.TopK = nil
		} else {
			if claudeRequest.MaxTokens == nil || *claudeRequest.MaxTokens < 1280 {
				claudeRequest.MaxTokens = common.GetPointer[uint](1280)
			}

			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: common.GetPointer(model_setting.GetClaudeSettings().GetThinkingBudgetTokens(*claudeRequest.MaxTokens)),
			}
			claudeRequest.TopP = nil
			claudeRequest.Temperature = common.GetPointer[float64](1.0)
		}
		if !model_setting.ShouldPreserveThinkingSuffix(textRequest.Model) {
			claudeRequest.Model = trimmedModel
		}
	}

	if textRequest.ReasoningEffort != "" {
		if reasoning.IsClaudeAdaptiveThinkingOnlyModel(claudeRequest.Model) {
			if textRequest.ReasoningEffort == "none" && !reasoning.IsClaudeAlwaysThinkingModel(claudeRequest.Model) {
				claudeRequest.Thinking = &dto.Thinking{Type: "disabled"}
				claudeRequest.OutputConfig = nil
			} else if reasoning.IsClaudeEffortLevel(textRequest.ReasoningEffort) {
				claudeRequest.Thinking = &dto.Thinking{Type: "adaptive", Display: "summarized"}
				claudeRequest.OutputConfig = json.RawMessage(fmt.Sprintf(`{"effort":"%s"}`, textRequest.ReasoningEffort))
			}
		} else {
			switch textRequest.ReasoningEffort {
			case "low":
				claudeRequest.Thinking = &dto.Thinking{
					Type:         "enabled",
					BudgetTokens: common.GetPointer[int](1280),
				}
			case "medium":
				claudeRequest.Thinking = &dto.Thinking{
					Type:         "enabled",
					BudgetTokens: common.GetPointer[int](2048),
				}
			case "high":
				claudeRequest.Thinking = &dto.Thinking{
					Type:         "enabled",
					BudgetTokens: common.GetPointer[int](4096),
				}
			}
		}
	}

	if textRequest.Reasoning != nil {
		var reasoningConfig openRouterRequestReasoning
		if err := common.Unmarshal(textRequest.Reasoning, &reasoningConfig); err != nil {
			return nil, err
		}

		if reasoning.IsClaudeAdaptiveThinkingOnlyModel(claudeRequest.Model) &&
			(reasoning.IsClaudeEffortLevel(reasoningConfig.Effort) || reasoningConfig.MaxTokens != nil) {
			effort := reasoningConfig.Effort
			if !reasoning.IsClaudeEffortLevel(effort) {
				effort = "high"
			}
			claudeRequest.Thinking = &dto.Thinking{Type: "adaptive", Display: "summarized"}
			claudeRequest.OutputConfig = json.RawMessage(fmt.Sprintf(`{"effort":"%s"}`, effort))
		} else if reasoningConfig.MaxTokens != nil {
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: reasoningConfig.MaxTokens,
			}
		}
	}

	if reasoning.IsClaudeSamplingRestrictedModel(claudeRequest.Model) {
		claudeRequest.Temperature = nil
		claudeRequest.TopP = nil
		claudeRequest.TopK = nil
	}

	if textRequest.Stop != nil {
		switch stop := textRequest.Stop.(type) {
		case string:
			claudeRequest.StopSequences = []string{stop}
		case []string:
			if len(stop) > 4 {
				return nil, errors.New("stop must contain at most 4 sequences")
			}
			claudeRequest.StopSequences = append([]string(nil), stop...)
		case []any:
			if len(stop) > 4 {
				return nil, errors.New("stop must contain at most 4 sequences")
			}
			stopSequences := make([]string, 0, len(stop))
			for index, item := range stop {
				value, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("stop[%d] must be a string", index)
				}
				stopSequences = append(stopSequences, value)
			}
			claudeRequest.StopSequences = stopSequences
		default:
			return nil, errors.New("stop must be a string or an array of strings")
		}
	}

	formatMessages := make([]dto.Message, 0)
	lastMessage := dto.Message{
		Role: "tool",
	}
	for _, message := range textRequest.Messages {
		role := message.Role
		if role == "" {
			role = "user"
		}
		if role == "developer" {
			role = "system"
		}
		fmtMessage := dto.Message{
			Role:    role,
			Content: message.Content,
		}
		if role == "tool" {
			fmtMessage.ToolCallId = message.ToolCallId
		}
		if role == "assistant" && message.ToolCalls != nil {
			fmtMessage.ToolCalls = message.ToolCalls
		}
		if lastMessage.Role == role && lastMessage.Role != "tool" {
			if lastMessage.IsStringContent() && message.IsStringContent() {
				fmtMessage.SetStringContent(strings.Trim(fmt.Sprintf("%s %s", lastMessage.StringContent(), message.StringContent()), "\""))
				formatMessages = formatMessages[:len(formatMessages)-1]
			}
		}
		if fmtMessage.Content == nil || (fmtMessage.IsStringContent() && fmtMessage.StringContent() == "") {
			fmtMessage.SetStringContent("...")
		}
		formatMessages = append(formatMessages, fmtMessage)
		lastMessage = fmtMessage
	}

	claudeMessages := make([]dto.ClaudeMessage, 0)
	isFirstMessage := true
	var systemMessages []dto.ClaudeMediaMessage

	for _, message := range formatMessages {
		if message.Role == "system" {
			parts, err := openAIContentToClaudeMediaMessages(c, message)
			if err != nil {
				return nil, err
			}
			for _, part := range parts {
				if part.Type != "text" {
					return nil, fmt.Errorf("system content type %q is not supported by Anthropic Messages", part.Type)
				}
			}
			systemMessages = append(systemMessages, parts...)
			continue
		}

		if isFirstMessage {
			isFirstMessage = false
			if message.Role != "user" {
				claudeMessage := dto.ClaudeMessage{
					Role: "user",
					Content: []dto.ClaudeMediaMessage{
						{
							Type: "text",
							Text: common.GetPointer[string]("..."),
						},
					},
				}
				claudeMessages = append(claudeMessages, claudeMessage)
			}
		}

		claudeMessage := dto.ClaudeMessage{
			Role: message.Role,
		}
		if message.Role == "tool" {
			if message.ToolCallId == "" {
				return nil, errors.New("tool message is missing tool_call_id")
			}
			if len(claudeMessages) > 0 && claudeMessages[len(claudeMessages)-1].Role == "user" {
				lastClaudeMessage := claudeMessages[len(claudeMessages)-1]
				contentParts := claudeMessageContentParts(lastClaudeMessage.Content)
				toolResultParts, err := openAIToolResultToClaudeMediaMessages(c, message.Content)
				if err != nil {
					return nil, err
				}
				contentParts = append(contentParts, dto.ClaudeMediaMessage{
					Type:      "tool_result",
					ToolUseId: message.ToolCallId,
					Content:   claudeToolResultContent(toolResultParts),
				})
				lastClaudeMessage.Content = contentParts
				claudeMessages[len(claudeMessages)-1] = lastClaudeMessage
				continue
			}

			claudeMessage.Role = "user"
			toolResultParts, err := openAIToolResultToClaudeMediaMessages(c, message.Content)
			if err != nil {
				return nil, err
			}
			claudeMessage.Content = []dto.ClaudeMediaMessage{
				{
					Type:      "tool_result",
					ToolUseId: message.ToolCallId,
					Content:   claudeToolResultContent(toolResultParts),
				},
			}
		} else if message.IsStringContent() && message.ToolCalls == nil {
			text := message.StringContent()
			if text == "" {
				text = "..."
			}
			claudeMessage.Content = text
		} else {
			claudeMediaMessages, err := openAIContentToClaudeMediaMessages(c, message)
			if err != nil {
				return nil, err
			}

			if message.ToolCalls != nil {
				toolCalls, err := parseOpenAIToolCalls(message.ToolCalls)
				if err != nil {
					return nil, err
				}
				for index, toolCall := range toolCalls {
					if toolCall.Type != "" && toolCall.Type != "function" {
						return nil, fmt.Errorf("messages tool_calls[%d].type %q is not supported by Claude Messages", index, toolCall.Type)
					}
					if toolCall.ID == "" || toolCall.Function.Name == "" {
						return nil, fmt.Errorf("messages tool_calls[%d] requires id and function.name", index)
					}
					inputObj, err := openAIToolCallInput(toolCall.Function.Arguments)
					if err != nil {
						return nil, fmt.Errorf("tool call %q has invalid arguments: %w", toolCall.Function.Name, err)
					}
					claudeMediaMessages = append(claudeMediaMessages, dto.ClaudeMediaMessage{
						Type:  "tool_use",
						Id:    toolCall.ID,
						Name:  toolCall.Function.Name,
						Input: inputObj,
					})
				}
			}
			claudeMessage.Content = claudeMediaMessages
		}
		claudeMessages = append(claudeMessages, claudeMessage)
	}

	if len(systemMessages) > 0 {
		claudeRequest.System = systemMessages
	}
	if err := applyOpenAIResponseFormatToClaude(&claudeRequest, textRequest.ResponseFormat); err != nil {
		return nil, err
	}

	claudeRequest.Prompt = ""
	claudeRequest.Messages = claudeMessages
	return &claudeRequest, nil
}

func claudeMessageContentParts(content any) []dto.ClaudeMediaMessage {
	switch typed := content.(type) {
	case []dto.ClaudeMediaMessage:
		return append([]dto.ClaudeMediaMessage(nil), typed...)
	case string:
		if typed == "" {
			return nil
		}
		return []dto.ClaudeMediaMessage{{
			Type: "text",
			Text: common.GetPointer(typed),
		}}
	default:
		parts, _ := common.Any2Type[[]dto.ClaudeMediaMessage](content)
		return parts
	}
}

func claudeToolResultContent(parts []dto.ClaudeMediaMessage) any {
	if len(parts) == 1 && parts[0].Type == "text" {
		return parts[0].GetText()
	}
	return parts
}

func openAIToolResultToClaudeMediaMessages(c *gin.Context, content any) ([]dto.ClaudeMediaMessage, error) {
	if content == nil {
		return nil, nil
	}
	if text, ok := content.(string); ok {
		return []dto.ClaudeMediaMessage{{Type: "text", Text: common.GetPointer(text)}}, nil
	}

	message := dto.Message{Content: content}
	parts := message.ParseContent()
	if len(parts) == 0 {
		return nil, fmt.Errorf("tool result content must be a string or an array of content parts")
	}
	return openAIContentPartsToClaudeMediaMessages(c, parts)
}

func openAIContentToClaudeMediaMessages(c *gin.Context, message dto.Message) ([]dto.ClaudeMediaMessage, error) {
	if message.Content == nil {
		return nil, nil
	}
	if message.IsStringContent() {
		return []dto.ClaudeMediaMessage{{
			Type: "text",
			Text: common.GetPointer(message.StringContent()),
		}}, nil
	}
	return openAIContentPartsToClaudeMediaMessages(c, message.ParseContent())
}

func openAIContentPartsToClaudeMediaMessages(c *gin.Context, parts []dto.MediaContent) ([]dto.ClaudeMediaMessage, error) {
	claudeParts := make([]dto.ClaudeMediaMessage, 0, len(parts))
	for index, part := range parts {
		switch part.Type {
		case dto.ContentTypeText:
			claudeParts = append(claudeParts, dto.ClaudeMediaMessage{
				Type: "text",
				Text: common.GetPointer(part.Text),
			})
		case dto.ContentTypeImageURL:
			image := part.GetImageMedia()
			if image == nil || strings.TrimSpace(image.Url) == "" {
				return nil, fmt.Errorf("messages content[%d] image_url is missing a URL", index)
			}
			if strings.HasPrefix(image.Url, "http://") || strings.HasPrefix(image.Url, "https://") {
				claudeParts = append(claudeParts, dto.ClaudeMediaMessage{
					Type: "image",
					Source: &dto.ClaudeMessageSource{
						Type: "url",
						Url:  image.Url,
					},
				})
				continue
			}
			source := part.ToFileSource()
			if source == nil {
				return nil, fmt.Errorf("messages content[%d] image_url is missing a URL", index)
			}
			base64Data, mimeType, err := relaymedia.ResolveBase64Data(c, source, "formatting image for Claude")
			if err != nil {
				return nil, fmt.Errorf("messages content[%d] image_url: %w", index, err)
			}
			if !strings.HasPrefix(mimeType, "image/") {
				return nil, fmt.Errorf("messages content[%d] image_url resolved to unsupported media type %q", index, mimeType)
			}
			claudeParts = append(claudeParts, dto.ClaudeMediaMessage{
				Type: "image",
				Source: &dto.ClaudeMessageSource{
					Type:      "base64",
					MediaType: mimeType,
					Data:      base64Data,
				},
			})
		case dto.ContentTypeFile:
			file := part.GetFile()
			if file == nil || strings.TrimSpace(file.FileData) == "" {
				return nil, fmt.Errorf("messages content[%d] file must contain inline file_data", index)
			}
			mimeType := ""
			if extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(file.FileName)), "."); extension != "" {
				mimeType = relaymedia.ResolveMimeTypeByExtension(extension)
				if mimeType == "application/octet-stream" {
					mimeType = ""
				}
			}
			base64Data, resolvedMimeType, err := relaymedia.ResolveBase64Data(
				c,
				types.NewFileSourceFromData(file.FileData, mimeType),
				"formatting file for Claude",
			)
			if err != nil {
				return nil, fmt.Errorf("messages content[%d] file: %w", index, err)
			}
			if strings.HasPrefix(resolvedMimeType, "text/") {
				decodedData, err := base64.StdEncoding.DecodeString(base64Data)
				if err != nil {
					return nil, fmt.Errorf("messages content[%d] text file: %w", index, err)
				}
				claudeParts = append(claudeParts, dto.ClaudeMediaMessage{
					Type: "text",
					Text: common.GetPointer(string(decodedData)),
				})
				continue
			}
			claudeType := ""
			if strings.HasPrefix(resolvedMimeType, "application/pdf") {
				claudeType = "document"
			} else if strings.HasPrefix(resolvedMimeType, "image/") {
				claudeType = "image"
			}
			if claudeType == "" {
				return nil, fmt.Errorf("messages content[%d] file resolved to unsupported media type %q", index, resolvedMimeType)
			}
			claudeParts = append(claudeParts, dto.ClaudeMediaMessage{
				Type: claudeType,
				Source: &dto.ClaudeMessageSource{
					Type:      "base64",
					MediaType: resolvedMimeType,
					Data:      base64Data,
				},
			})
		case dto.ContentTypeInputAudio, dto.ContentTypeVideoUrl:
			return nil, fmt.Errorf("messages content[%d] type %q is not supported by Anthropic Messages", index, part.Type)
		default:
			return nil, fmt.Errorf("messages content[%d] type %q is not supported by Anthropic Messages", index, part.Type)
		}
	}
	return claudeParts, nil
}

func openAIToolCallInput(arguments string) (map[string]any, error) {
	if strings.TrimSpace(arguments) == "" || strings.TrimSpace(arguments) == "null" {
		return map[string]any{}, nil
	}
	var input map[string]any
	if err := common.Unmarshal([]byte(arguments), &input); err != nil {
		return nil, err
	}
	if input == nil {
		return map[string]any{}, nil
	}
	return input, nil
}

func parseOpenAIToolCalls(raw []byte) ([]dto.ToolCallRequest, error) {
	var toolCalls []dto.ToolCallRequest
	if err := common.Unmarshal(raw, &toolCalls); err != nil {
		return nil, fmt.Errorf("invalid messages.tool_calls: %w", err)
	}
	return toolCalls, nil
}

func applyOpenAIResponseFormatToClaude(request *dto.ClaudeRequest, responseFormat *dto.ResponseFormat) error {
	if request == nil || responseFormat == nil || responseFormat.Type == "" || responseFormat.Type == "text" {
		return nil
	}
	if responseFormat.Type != "json_schema" {
		return fmt.Errorf("response_format type %q cannot be converted to Anthropic Messages without changing semantics", responseFormat.Type)
	}

	var schema dto.FormatJsonSchema
	if err := common.Unmarshal(responseFormat.JsonSchema, &schema); err != nil {
		return fmt.Errorf("invalid response_format.json_schema: %w", err)
	}
	if schema.Schema == nil {
		return errors.New("response_format.json_schema.schema is required for Anthropic Messages")
	}

	outputConfig := make(map[string]any)
	if len(request.OutputConfig) > 0 {
		if err := common.Unmarshal(request.OutputConfig, &outputConfig); err != nil {
			return fmt.Errorf("invalid Claude output_config: %w", err)
		}
	}
	outputConfig["format"] = map[string]any{
		"type":   "json_schema",
		"schema": schema.Schema,
	}
	encoded, err := common.Marshal(outputConfig)
	if err != nil {
		return fmt.Errorf("marshal Claude output_config: %w", err)
	}
	request.OutputConfig = encoded
	return nil
}
