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
	for i, message := range textRequest.Messages {
		if message.Role == "" {
			textRequest.Messages[i].Role = "user"
		}
		fmtMessage := dto.Message{
			Role:    message.Role,
			Content: message.Content,
		}
		if message.Role == "tool" {
			fmtMessage.ToolCallId = message.ToolCallId
		}
		if message.Role == "assistant" && message.ToolCalls != nil {
			fmtMessage.ToolCalls = message.ToolCalls
		}
		if lastMessage.Role == message.Role && lastMessage.Role != "tool" {
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
						Text: common.GetPointer[string](text),
					})
				}
			} else {
				for _, ctx := range message.ParseContent() {
					if ctx.Type == "text" && ctx.Text != "" {
						systemMessages = append(systemMessages, dto.ClaudeMediaMessage{
							Type: "text",
							Text: common.GetPointer[string](ctx.Text),
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
			if len(claudeMessages) > 0 && claudeMessages[len(claudeMessages)-1].Role == "user" {
				lastClaudeMessage := claudeMessages[len(claudeMessages)-1]
				if content, ok := lastClaudeMessage.Content.(string); ok {
					lastClaudeMessage.Content = []dto.ClaudeMediaMessage{
						{
							Type: "text",
							Text: common.GetPointer[string](content),
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
							Text: common.GetPointer[string](mediaMessage.Text),
						})
					}
				default:
					source := mediaMessage.ToFileSource()
					if mediaMessage.Type == dto.ContentTypeFile {
						file := mediaMessage.GetFile()
						if file == nil || strings.TrimSpace(file.FileData) == "" {
							continue
						}
						mimeType := ""
						if extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(file.FileName)), "."); extension != "" {
							mimeType = relaymedia.ResolveMimeTypeByExtension(extension)
							if mimeType == "application/octet-stream" {
								mimeType = ""
							}
						}
						source = types.NewFileSourceFromData(file.FileData, mimeType)
					}
					if source == nil {
						continue
					}
					base64Data, mimeType, err := relaymedia.ResolveBase64Data(c, source, "formatting file for Claude")
					if err != nil {
						return nil, fmt.Errorf("get file data failed: %s", err.Error())
					}
					if strings.HasPrefix(mimeType, "text/") {
						decodedData, err := base64.StdEncoding.DecodeString(base64Data)
						if err != nil {
							return nil, fmt.Errorf("decode text file data failed: %s", err.Error())
						}
						claudeMediaMessages = append(claudeMediaMessages, dto.ClaudeMediaMessage{
							Type: "text",
							Text: common.GetPointer(string(decodedData)),
						})
						continue
					}
					claudeMediaMessage := dto.ClaudeMediaMessage{
						Source: &dto.ClaudeMessageSource{
							Type: "base64",
						},
					}
					if strings.HasPrefix(mimeType, "application/pdf") {
						claudeMediaMessage.Type = "document"
					} else if strings.HasPrefix(mimeType, "image/") {
						claudeMediaMessage.Type = "image"
					} else {
						continue
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
						if err := common.Unmarshal([]byte(args), &inputObj); err != nil {
							common.SysLog(fmt.Sprintf(
								"failed to decode tool call arguments as an object: bytes=%d, error=%v",
								len(args),
								err,
							))
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
	return &claudeRequest, nil
}
