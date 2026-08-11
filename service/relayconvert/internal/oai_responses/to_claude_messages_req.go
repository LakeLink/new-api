package oairesponses

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relaymedia "github.com/QuantumNous/new-api/service/relayconvert/internal/media"
	sharedclaude "github.com/QuantumNous/new-api/service/relayconvert/internal/shared/claude"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
)

func convertOpenAIResponsesRequestToClaudeMessages(c *gin.Context, _ *relaycommon.RelayInfo, request any) (any, error) {
	responsesRequest, err := OpenAIResponsesRequestFromAny(request)
	if err != nil {
		return nil, err
	}
	return OpenAIResponsesRequestToClaudeMessages(c, responsesRequest)
}

func OpenAIResponsesRequestToClaudeMessages(c *gin.Context, req *dto.OpenAIResponsesRequest) (*dto.ClaudeRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if req.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if err := ValidateRequestChatUnsupportedFields(req); err != nil {
		return nil, err
	}
	if err := ValidateToolsForConversion(req.Tools, "Anthropic Messages"); err != nil {
		return nil, err
	}
	if req.TopLogProbs != nil {
		return nil, fmt.Errorf("top_logprobs cannot be converted to Anthropic Messages")
	}
	if req.MaxToolCalls != nil {
		return nil, fmt.Errorf("max_tool_calls cannot be converted to Anthropic Messages")
	}

	claudeRequest := &dto.ClaudeRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
		MaxTokens:   req.MaxOutputTokens,
	}
	if claudeRequest.MaxTokens == nil {
		defaultMaxTokens := uint(model_setting.GetClaudeSettings().GetDefaultMaxTokens(req.Model))
		claudeRequest.MaxTokens = &defaultMaxTokens
	}
	if baseModel, effort, ok := reasoning.ParseClaudeEffortSuffix(req.Model); ok &&
		(strings.HasPrefix(baseModel, "claude-opus-4-6") ||
			reasoning.IsClaudeAdaptiveThinkingOnlyModel(baseModel)) {
		claudeRequest.Model = baseModel
		claudeRequest.Thinking = &dto.Thinking{Type: "adaptive"}
		claudeRequest.OutputConfig = json.RawMessage(fmt.Sprintf(`{"effort":"%s"}`, effort))
		if reasoning.IsClaudeSamplingRestrictedModel(baseModel) {
			claudeRequest.Thinking.Display = "summarized"
		}
	} else if model_setting.GetClaudeSettings().ThinkingAdapterEnabled &&
		strings.HasSuffix(req.Model, "-thinking") {
		baseModel := strings.TrimSuffix(req.Model, "-thinking")
		if reasoning.IsClaudeAdaptiveThinkingOnlyModel(baseModel) {
			claudeRequest.Thinking = &dto.Thinking{Type: "adaptive", Display: "summarized"}
			claudeRequest.OutputConfig = json.RawMessage(`{"effort":"high"}`)
		} else {
			if *claudeRequest.MaxTokens < 1280 {
				claudeRequest.MaxTokens = common.GetPointer(uint(1280))
			}
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: common.GetPointer(model_setting.GetClaudeSettings().GetThinkingBudgetTokens(*claudeRequest.MaxTokens)),
			}
			claudeRequest.Temperature = common.GetPointer(1.0)
			claudeRequest.TopP = nil
		}
		if !model_setting.ShouldPreserveThinkingSuffix(req.Model) {
			claudeRequest.Model = baseModel
		}
	}

	functions, err := RequestFunctionDeclarations(req.Tools)
	if err != nil {
		return nil, err
	}
	if len(functions) > 0 {
		claudeRequest.Tools = responsesFunctionDeclarationsToClaudeTools(functions)
	}
	if err := applyResponsesTextToClaude(req.Text, claudeRequest); err != nil {
		return nil, err
	}

	toolChoice, err := RequestToolChoiceToChat(req.ToolChoice)
	if err != nil {
		return nil, err
	}
	if toolChoice != nil || RawJSONPresent(req.ParallelToolCalls) {
		claudeRequest.ToolChoice = sharedclaude.MapOpenAIToolChoice(toolChoice, ParallelToolCalls(req.ParallelToolCalls))
	}
	applyResponsesReasoningToClaude(req, claudeRequest)
	if reasoning.IsClaudeSamplingRestrictedModel(claudeRequest.Model) {
		claudeRequest.Temperature = nil
		claudeRequest.TopP = nil
		claudeRequest.TopK = nil
	}

	systemMessages := make([]dto.ClaudeMediaMessage, 0)
	if RawJSONPresent(req.Instructions) {
		instructions, err := responsesInstructionsToClaudeMediaMessages(c, req.Instructions)
		if err != nil {
			return nil, fmt.Errorf("invalid instructions: %w", err)
		}
		systemMessages = append(systemMessages, instructions...)
	}

	inputItems, err := InputItems(req.Input)
	if err != nil {
		return nil, err
	}
	for _, item := range inputItems {
		itemType := strings.TrimSpace(common.Interface2String(item["type"]))
		switch itemType {
		case ResponsesInputTypeFunctionCall:
			toolUse, err := responsesFunctionCallItemToClaudeToolUse(item, "arguments")
			if err != nil {
				return nil, err
			}
			claudeRequest.Messages = appendClaudeToolUse(claudeRequest.Messages, toolUse)
		case ResponsesInputTypeCustomToolCall:
			toolUse, err := responsesFunctionCallItemToClaudeToolUse(item, "input")
			if err != nil {
				return nil, err
			}
			claudeRequest.Messages = appendClaudeToolUse(claudeRequest.Messages, toolUse)
		case ResponsesInputTypeFunctionCallOutput, ResponsesInputTypeCustomToolOutput:
			toolResult, err := responsesFunctionOutputItemToClaudeToolResult(c, item)
			if err != nil {
				return nil, err
			}
			claudeRequest.Messages = appendClaudeToolResult(claudeRequest.Messages, toolResult)
		default:
			role := responsesClaudeRole(item)
			parts, err := responsesInputContentToClaudeMediaMessages(c, item["content"])
			if err != nil {
				return nil, err
			}
			if role == "system" {
				systemMessages = append(systemMessages, parts...)
				continue
			}
			if len(parts) == 0 {
				parts = []dto.ClaudeMediaMessage{
					{
						Type: "text",
						Text: common.GetPointer("..."),
					},
				}
			}
			claudeRequest.Messages = append(claudeRequest.Messages, dto.ClaudeMessage{
				Role:    role,
				Content: parts,
			})
		}
	}

	if len(systemMessages) > 0 {
		claudeRequest.System = systemMessages
	}
	claudeRequest.Messages = ensureClaudeMessagesStartWithUser(claudeRequest.Messages)
	return claudeRequest, nil
}

func responsesFunctionDeclarationsToClaudeTools(functions []dto.FunctionRequest) []any {
	tools := make([]any, 0, len(functions))
	for _, function := range functions {
		tools = append(tools, &dto.Tool{
			Name:        function.Name,
			Description: function.Description,
			InputSchema: responsesFunctionParametersToClaudeInputSchema(function.Parameters),
			Strict:      function.Strict,
		})
	}
	return tools
}

func responsesFunctionParametersToClaudeInputSchema(parameters any) map[string]interface{} {
	if params, ok := parameters.(map[string]any); ok {
		schema := make(map[string]interface{}, len(params))
		for key, value := range params {
			schema[key] = value
		}
		if schema["type"] == nil {
			schema["type"] = "object"
		}
		if schema["properties"] == nil {
			schema["properties"] = map[string]interface{}{}
		}
		return schema
	}
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func applyResponsesReasoningToClaude(req *dto.OpenAIResponsesRequest, claudeRequest *dto.ClaudeRequest) {
	effort := ReasoningEffort(req)
	if reasoning.IsClaudeAdaptiveThinkingOnlyModel(claudeRequest.Model) {
		if effort == "none" && !reasoning.IsClaudeAlwaysThinkingModel(claudeRequest.Model) {
			claudeRequest.Thinking = &dto.Thinking{Type: "disabled"}
			claudeRequest.OutputConfig = nil
			return
		}
		if reasoning.IsClaudeEffortLevel(effort) {
			claudeRequest.Thinking = &dto.Thinking{Type: "adaptive", Display: "summarized"}
			claudeRequest.OutputConfig = json.RawMessage(fmt.Sprintf(`{"effort":"%s"}`, effort))
			return
		}
	}
	switch effort {
	case "low":
		claudeRequest.Thinking = &dto.Thinking{
			Type:         "enabled",
			BudgetTokens: common.GetPointer(1280),
		}
	case "medium":
		claudeRequest.Thinking = &dto.Thinking{
			Type:         "enabled",
			BudgetTokens: common.GetPointer(2048),
		}
	case "high":
		claudeRequest.Thinking = &dto.Thinking{
			Type:         "enabled",
			BudgetTokens: common.GetPointer(4096),
		}
	}
}

func responsesInputContentToClaudeMediaMessages(c *gin.Context, content any) ([]dto.ClaudeMediaMessage, error) {
	contentParts, err := ContentParts(content)
	if err != nil {
		return nil, err
	}

	parts := make([]dto.ClaudeMediaMessage, 0, len(contentParts))
	for _, contentPart := range contentParts {
		partType := strings.TrimSpace(common.Interface2String(contentPart["type"]))
		switch partType {
		case "input_text", "output_text", "text":
			text := common.Interface2String(contentPart["text"])
			if text != "" {
				parts = append(parts, dto.ClaudeMediaMessage{
					Type: "text",
					Text: common.GetPointer(text),
				})
			}
		case "input_audio", "input_video":
			return nil, fmt.Errorf("Responses content type %q cannot be converted to Anthropic Messages", partType)
		case "input_image", "input_file":
			if partType == "input_image" {
				if imageURL := responsesInputImageURL(contentPart); imageURL != "" &&
					(strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://")) {
					parts = append(parts, dto.ClaudeMediaMessage{
						Type: "image",
						Source: &dto.ClaudeMessageSource{
							Type: "url",
							Url:  imageURL,
						},
					})
					continue
				}
			}
			source := ContentPartToFileSource(contentPart)
			if source == nil {
				return nil, fmt.Errorf("Responses content type %q is missing its source", partType)
			}
			base64Data, mimeType, err := relaymedia.ResolveBase64Data(c, source, "formatting Responses input for Claude")
			if err != nil {
				return nil, fmt.Errorf("get file data failed: %s", err.Error())
			}
			claudePart := dto.ClaudeMediaMessage{
				Source: &dto.ClaudeMessageSource{
					Type:      "base64",
					MediaType: mimeType,
					Data:      base64Data,
				},
			}
			if strings.HasPrefix(mimeType, "text/") {
				decodedData, err := decodeBase64ResponseData(base64Data)
				if err != nil {
					return nil, fmt.Errorf("decode text file data failed: %w", err)
				}
				parts = append(parts, dto.ClaudeMediaMessage{Type: "text", Text: common.GetPointer(string(decodedData))})
				continue
			}
			if strings.HasPrefix(mimeType, "application/pdf") {
				claudePart.Type = "document"
			} else if strings.HasPrefix(mimeType, "image/") {
				claudePart.Type = "image"
			} else {
				return nil, fmt.Errorf("Responses content type %q resolved to unsupported media type %q", partType, mimeType)
			}
			parts = append(parts, claudePart)
		default:
			return nil, fmt.Errorf("Responses content type %q cannot be converted to Anthropic Messages", partType)
		}
	}
	return parts, nil
}

func responsesInputImageURL(contentPart map[string]any) string {
	value, ok := contentPart["image_url"]
	if !ok {
		return ""
	}
	if imageURL, ok := value.(string); ok {
		return imageURL
	}
	if imageMap, ok := value.(map[string]any); ok {
		return common.Interface2String(imageMap["url"])
	}
	return ""
}

func decodeBase64ResponseData(data string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func responsesInstructionsToClaudeMediaMessages(c *gin.Context, raw json.RawMessage) ([]dto.ClaudeMediaMessage, error) {
	if common.GetJsonType(raw) == "string" {
		value, err := JSONString(raw)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(value) == "" {
			return nil, nil
		}
		return []dto.ClaudeMediaMessage{{Type: "text", Text: common.GetPointer(value)}}, nil
	}
	var items []map[string]any
	if err := common.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	parts := make([]dto.ClaudeMediaMessage, 0)
	for index, item := range items {
		content, ok := item["content"]
		if !ok {
			return nil, fmt.Errorf("instructions[%d] is missing content", index)
		}
		converted, err := responsesInputContentToClaudeMediaMessages(c, content)
		if err != nil {
			return nil, err
		}
		for _, part := range converted {
			if part.Type != "text" {
				return nil, fmt.Errorf("instructions[%d] contains unsupported %q content", index, part.Type)
			}
		}
		parts = append(parts, converted...)
	}
	return parts, nil
}

func applyResponsesTextToClaude(raw json.RawMessage, request *dto.ClaudeRequest) error {
	responseFormat, err := RequestTextToChatResponseFormat(raw)
	if err != nil {
		return err
	}
	if responseFormat == nil || responseFormat.Type == "" || responseFormat.Type == "text" {
		return nil
	}
	if responseFormat.Type != "json_schema" {
		return fmt.Errorf("Responses text format %q cannot be converted to Anthropic Messages", responseFormat.Type)
	}
	var format dto.FormatJsonSchema
	if err := common.Unmarshal(responseFormat.JsonSchema, &format); err != nil {
		return fmt.Errorf("invalid Responses text format: %w", err)
	}
	if format.Schema == nil {
		return fmt.Errorf("Responses json_schema format is missing schema")
	}
	outputFormat := map[string]any{
		"type":   "json_schema",
		"schema": format.Schema,
	}
	outputConfig := make(map[string]any)
	if len(request.OutputConfig) > 0 {
		if err := common.Unmarshal(request.OutputConfig, &outputConfig); err != nil {
			return fmt.Errorf("invalid existing Anthropic output_config: %w", err)
		}
	}
	outputConfig["format"] = outputFormat
	encoded, err := common.Marshal(outputConfig)
	if err != nil {
		return fmt.Errorf("marshal Anthropic output_config: %w", err)
	}
	request.OutputConfig = encoded
	return nil
}

func responsesFunctionCallItemToClaudeToolUse(item map[string]any, inputKey string) (dto.ClaudeMediaMessage, error) {
	input, err := responsesFunctionCallInput(item[inputKey], inputKey)
	if err != nil {
		return dto.ClaudeMediaMessage{}, err
	}
	id := CallID(item)
	name := strings.TrimSpace(common.Interface2String(item["name"]))
	if id == "" || name == "" {
		return dto.ClaudeMediaMessage{}, fmt.Errorf("%s item requires call_id and name", inputKey)
	}
	return dto.ClaudeMediaMessage{
		Type:  "tool_use",
		Id:    id,
		Name:  name,
		Input: input,
	}, nil
}

func responsesFunctionCallInput(value any, key string) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	if object, ok := value.(map[string]any); ok {
		return object, nil
	}
	if text, ok := value.(string); ok {
		var object map[string]any
		if err := common.Unmarshal([]byte(text), &object); err != nil || object == nil {
			return nil, fmt.Errorf("%s must contain a JSON object", key)
		}
		return object, nil
	}
	return nil, fmt.Errorf("%s must contain a JSON object", key)
}

func responsesFunctionOutputItemToClaudeToolResult(c *gin.Context, item map[string]any) (dto.ClaudeMediaMessage, error) {
	content := responsesToolOutputValue(item["output"])
	_, isArray := content.([]any)
	if !isArray {
		_, isArray = content.([]map[string]any)
	}
	if isArray {
		converted, err := responsesInputContentToClaudeMediaMessages(c, content)
		if err != nil {
			return dto.ClaudeMediaMessage{}, err
		}
		if len(converted) == 1 && converted[0].Type == "text" {
			content = converted[0].GetText()
		} else {
			content = converted
		}
	}
	return dto.ClaudeMediaMessage{
		Type:      "tool_result",
		ToolUseId: CallID(item),
		Content:   content,
	}, nil
}

func responsesToolOutputValue(value any) any {
	if value == nil {
		return ""
	}
	return value
}

func appendClaudeToolUse(messages []dto.ClaudeMessage, toolUse dto.ClaudeMediaMessage) []dto.ClaudeMessage {
	if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
		last := messages[len(messages)-1]
		parts := claudeMessageContentParts(last.Content)
		parts = append(parts, toolUse)
		last.Content = parts
		messages[len(messages)-1] = last
		return messages
	}
	return append(messages, dto.ClaudeMessage{
		Role:    "assistant",
		Content: []dto.ClaudeMediaMessage{toolUse},
	})
}

func appendClaudeToolResult(messages []dto.ClaudeMessage, toolResult dto.ClaudeMediaMessage) []dto.ClaudeMessage {
	if len(messages) > 0 && messages[len(messages)-1].Role == "user" {
		last := messages[len(messages)-1]
		parts := claudeMessageContentParts(last.Content)
		parts = append(parts, toolResult)
		last.Content = parts
		messages[len(messages)-1] = last
		return messages
	}
	return append(messages, dto.ClaudeMessage{
		Role:    "user",
		Content: []dto.ClaudeMediaMessage{toolResult},
	})
}

func claudeMessageContentParts(content any) []dto.ClaudeMediaMessage {
	switch typed := content.(type) {
	case []dto.ClaudeMediaMessage:
		return typed
	case string:
		if typed == "" {
			return nil
		}
		return []dto.ClaudeMediaMessage{
			{
				Type: "text",
				Text: common.GetPointer(typed),
			},
		}
	default:
		parts, _ := common.Any2Type[[]dto.ClaudeMediaMessage](content)
		return parts
	}
}

func responsesClaudeRole(item map[string]any) string {
	switch strings.TrimSpace(common.Interface2String(item["role"])) {
	case "assistant":
		return "assistant"
	case "system", "developer":
		return "system"
	default:
		return "user"
	}
}

func ensureClaudeMessagesStartWithUser(messages []dto.ClaudeMessage) []dto.ClaudeMessage {
	if len(messages) == 0 || messages[0].Role == "user" {
		return messages
	}
	return append([]dto.ClaudeMessage{
		{
			Role: "user",
			Content: []dto.ClaudeMediaMessage{
				{
					Type: "text",
					Text: common.GetPointer("..."),
				},
			},
		},
	}, messages...)
}
