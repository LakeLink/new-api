package oaichat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"context"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	relaymedia "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/media"
	sharedclaude "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/shared/claude"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/relaykit/types"
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

func OpenAIChatRequestToClaudeMessages(c context.Context, info convmeta.Meta, textRequest dto.GeneralOpenAIRequest) (*dto.ClaudeRequest, error) {
	opts := convmeta.OptionsOf(info)
	claudeTools := make([]any, 0, len(textRequest.Tools))

	for _, tool := range textRequest.Tools {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		claudeTool := dto.Tool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Strict:      tool.Function.Strict,
			InputSchema: map[string]interface{}{"type": "object"},
		}
		if tool.Function.Parameters != nil {
			params, ok := tool.Function.Parameters.(map[string]any)
			if !ok {
				return nil, errors.New("parameters must be a JSON object")
			}
			if schemaType, exists := params["type"]; exists && schemaType != nil {
				schemaTypeValue, ok := schemaType.(string)
				if !ok || schemaTypeValue != "object" {
					return nil, errors.New("parameters.type must be object")
				}
			}
			claudeTool.InputSchema = make(map[string]interface{}, len(params)+1)
			claudeTool.InputSchema["type"] = "object"
			for key, value := range params {
				claudeTool.InputSchema[key] = value
			}
		}
		claudeTools = append(claudeTools, &claudeTool)
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
			if err := kitutil.Unmarshal(textRequest.WebSearchOptions.UserLocation, &userLocationMap); err == nil {
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
			webSearchTool.MaxUses = kitutil.GetPointer(uint(webSearchMaxUsesLow))
		case "", "medium":
			webSearchTool.MaxUses = kitutil.GetPointer(uint(webSearchMaxUsesMedium))
		case "high":
			webSearchTool.MaxUses = kitutil.GetPointer(uint(webSearchMaxUsesHigh))
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
		claudeRequest.TopK = kitutil.GetPointer(*textRequest.TopK)
	}
	if textRequest.IsStream(nil) {
		claudeRequest.Stream = kitutil.GetPointer(true)
	}

	if textRequest.ToolChoice != nil || textRequest.ParallelTooCalls != nil {
		claudeToolChoice := sharedclaude.MapOpenAIToolChoice(textRequest.ToolChoice, textRequest.ParallelTooCalls)
		if claudeToolChoice != nil {
			claudeRequest.ToolChoice = claudeToolChoice
		}
	}

	if claudeRequest.MaxTokens == nil {
		if defaultMaxTokens, configured := opts.Claude.DefaultMaxTokensFor(textRequest.Model); configured {
			value := uint(defaultMaxTokens)
			claudeRequest.MaxTokens = &value
		}
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
			claudeRequest.Temperature = kitutil.GetPointer[float64](1.0)
		}
	} else if opts.Claude.ThinkingAdapterEnabled &&
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
				claudeRequest.MaxTokens = kitutil.GetPointer[uint](1280)
			}

			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: kitutil.GetPointer[int](int(float64(*claudeRequest.MaxTokens) * opts.Claude.ThinkingAdapterBudgetTokensPercentage)),
			}
			claudeRequest.TopP = nil
			claudeRequest.Temperature = kitutil.GetPointer[float64](1.0)
		}
		if !opts.ShouldPreserveThinkingSuffix(textRequest.Model) {
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
					BudgetTokens: kitutil.GetPointer[int](1280),
				}
			case "medium":
				claudeRequest.Thinking = &dto.Thinking{
					Type:         "enabled",
					BudgetTokens: kitutil.GetPointer[int](2048),
				}
			case "high":
				claudeRequest.Thinking = &dto.Thinking{
					Type:         "enabled",
					BudgetTokens: kitutil.GetPointer[int](4096),
				}
			}
		}
	}

	if textRequest.Reasoning != nil {
		var reasoningConfig openRouterRequestReasoning
		if err := kitutil.Unmarshal(textRequest.Reasoning, &reasoningConfig); err != nil {
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

	if err := applyOpenAIResponseFormatToClaude(&claudeRequest, textRequest.ResponseFormat); err != nil {
		return nil, err
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
			if message.IsStringContent() {
				if text := message.StringContent(); text != "" {
					systemMessages = append(systemMessages, dto.ClaudeMediaMessage{
						Type: "text",
						Text: kitutil.GetPointer[string](text),
					})
				}
			} else {
				for _, ctx := range message.ParseContent() {
					if ctx.Type == "text" && ctx.Text != "" {
						systemMessages = append(systemMessages, dto.ClaudeMediaMessage{
							Type: "text",
							Text: kitutil.GetPointer[string](ctx.Text),
						})
					}
				}
			}
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
							Text: kitutil.GetPointer[string]("..."),
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
			if len(claudeMessages) > 0 && claudeMessages[len(claudeMessages)-1].Role == "user" {
				lastClaudeMessage := claudeMessages[len(claudeMessages)-1]
				if content, ok := lastClaudeMessage.Content.(string); ok {
					lastClaudeMessage.Content = []dto.ClaudeMediaMessage{
						{
							Type: "text",
							Text: kitutil.GetPointer[string](content),
						},
					}
				}
				lastClaudeMessage.Content = append(lastClaudeMessage.Content.([]dto.ClaudeMediaMessage), dto.ClaudeMediaMessage{
					Type:      "tool_result",
					ToolUseId: message.ToolCallId,
					Content:   message.Content,
				})
				claudeMessages[len(claudeMessages)-1] = lastClaudeMessage
				continue
			}

			claudeMessage.Role = "user"
			claudeMessage.Content = []dto.ClaudeMediaMessage{
				{
					Type:      "tool_result",
					ToolUseId: message.ToolCallId,
					Content:   message.Content,
				},
			}
		} else if message.IsStringContent() && message.ToolCalls == nil {
			text := message.StringContent()
			if text == "" {
				text = "..."
			}
			claudeMessage.Content = text
		} else {
			claudeMediaMessages := make([]dto.ClaudeMediaMessage, 0)
			for _, mediaMessage := range message.ParseContent() {
				switch mediaMessage.Type {
				case "text":
					if mediaMessage.Text != "" {
						claudeMediaMessages = append(claudeMediaMessages, dto.ClaudeMediaMessage{
							Type: "text",
							Text: kitutil.GetPointer[string](mediaMessage.Text),
						})
					}
				case dto.ContentTypeFile:
					file := mediaMessage.GetFile()
					if file == nil || strings.TrimSpace(file.FileData) == "" {
						return nil, fmt.Errorf("messages content file must contain inline file_data")
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
						return nil, fmt.Errorf("get file data failed: %w", err)
					}
					if resolvedMimeType == "" {
						resolvedMimeType = mimeType
					}
					if strings.HasPrefix(resolvedMimeType, "text/") {
						decodedData, err := base64.StdEncoding.DecodeString(base64Data)
						if err != nil {
							return nil, fmt.Errorf("decode text file data failed: %w", err)
						}
						claudeMediaMessages = append(claudeMediaMessages, dto.ClaudeMediaMessage{
							Type: "text",
							Text: kitutil.GetPointer(string(decodedData)),
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
						return nil, fmt.Errorf("file resolved to unsupported media type %q", resolvedMimeType)
					}
					claudeMediaMessages = append(claudeMediaMessages, dto.ClaudeMediaMessage{
						Type: claudeType,
						Source: &dto.ClaudeMessageSource{
							Type:      "base64",
							MediaType: resolvedMimeType,
							Data:      base64Data,
						},
					})
					continue
				default:
					source := mediaMessage.ToFileSource()
					if source == nil {
						continue
					}
					base64Data, mimeType, err := relaymedia.ResolveBase64Data(c, source, "formatting image for Claude")
					if err != nil {
						return nil, fmt.Errorf("get file data failed: %s", err.Error())
					}
					claudeMediaMessage := dto.ClaudeMediaMessage{
						Source: &dto.ClaudeMessageSource{
							Type: "base64",
						},
					}
					if strings.HasPrefix(mimeType, "application/pdf") {
						claudeMediaMessage.Type = "document"
					} else {
						claudeMediaMessage.Type = "image"
					}

					claudeMediaMessage.Source.MediaType = mimeType
					claudeMediaMessage.Source.Data = base64Data
					claudeMediaMessages = append(claudeMediaMessages, claudeMediaMessage)
					continue
				}
			}

			if message.ToolCalls != nil {
				for _, toolCall := range message.ParseToolCalls() {
					inputObj := make(map[string]any)
					if args := toolCall.Function.Arguments; args != "" {
						if err := kitutil.Unmarshal([]byte(args), &inputObj); err != nil {
							kitutil.LogInfo("tool call function arguments is not a map[string]any: " + fmt.Sprintf("%v", toolCall.Function.Arguments))
						}
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

	claudeRequest.Prompt = ""
	claudeRequest.Messages = claudeMessages
	// Checked last so every injection path (default hook, thinking adapter
	// floor) has had its chance to satisfy the required field.
	if claudeRequest.MaxTokens == nil {
		return nil, sharedclaude.ErrMissingMaxTokens
	}
	return &claudeRequest, nil
}

func applyOpenAIResponseFormatToClaude(request *dto.ClaudeRequest, responseFormat *dto.ResponseFormat) error {
	if request == nil || responseFormat == nil || responseFormat.Type == "" || responseFormat.Type == "text" {
		return nil
	}
	if responseFormat.Type != "json_schema" {
		return fmt.Errorf("response_format type %q cannot be converted to Anthropic Messages without changing semantics", responseFormat.Type)
	}

	var schema dto.FormatJsonSchema
	if err := kitutil.Unmarshal(responseFormat.JsonSchema, &schema); err != nil {
		return fmt.Errorf("invalid response_format.json_schema: %w", err)
	}
	if schema.Schema == nil {
		return errors.New("response_format.json_schema.schema is required for Anthropic Messages")
	}

	outputConfig := make(map[string]any)
	if len(request.OutputConfig) > 0 {
		if err := kitutil.Unmarshal(request.OutputConfig, &outputConfig); err != nil {
			return fmt.Errorf("invalid Claude output_config: %w", err)
		}
	}
	outputConfig["format"] = map[string]any{
		"type":   "json_schema",
		"schema": schema.Schema,
	}
	encoded, err := kitutil.Marshal(outputConfig)
	if err != nil {
		return fmt.Errorf("marshal Claude output_config: %w", err)
	}
	request.OutputConfig = encoded
	return nil
}
