package claudemessages

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relaymeta "github.com/QuantumNous/new-api/service/relayconvert/internal/meta"
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

func ClaudeMessagesRequestToOpenAIChat(claudeRequest dto.ClaudeRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	openAIRequest := dto.GeneralOpenAIRequest{
		Model:       claudeRequest.Model,
		Temperature: claudeRequest.Temperature,
	}
	if maxTokens := claudeRequest.GetMaxTokensPointer(); maxTokens != nil {
		value := *maxTokens
		openAIRequest.MaxTokens = &value
	}
	if claudeRequest.TopP != nil {
		openAIRequest.TopP = common.GetPointer(*claudeRequest.TopP)
	}
	if claudeRequest.TopK != nil {
		openAIRequest.TopK = common.GetPointer(*claudeRequest.TopK)
	}
	if claudeRequest.Stream != nil {
		openAIRequest.Stream = common.GetPointer(*claudeRequest.Stream)
	}
	if claudeRequest.ToolChoice != nil {
		toolChoice, err := common.Any2Type[dto.ClaudeToolChoice](claudeRequest.ToolChoice)
		if err != nil {
			return nil, fmt.Errorf("invalid Claude tool_choice: %w", err)
		}
		switch toolChoice.Type {
		case "auto":
			openAIRequest.ToolChoice = "auto"
		case "any":
			openAIRequest.ToolChoice = "required"
		case "none":
			openAIRequest.ToolChoice = "none"
		case "tool":
			if toolChoice.Name == "" {
				return nil, fmt.Errorf("invalid Claude tool_choice: name is required for type tool")
			}
			openAIRequest.ToolChoice = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": toolChoice.Name,
				},
			}
		default:
			return nil, fmt.Errorf("invalid Claude tool_choice type %q", toolChoice.Type)
		}
		if toolChoice.DisableParallelToolUse != nil && toolChoice.Type != "none" {
			parallelToolCalls := !*toolChoice.DisableParallelToolUse
			openAIRequest.ParallelTooCalls = &parallelToolCalls
		}
	}
	if err := applyClaudeStructuredOutputToOpenAI(&openAIRequest, claudeRequest); err != nil {
		return nil, err
	}

	isOpenRouter := relaymeta.RelayInfoChannelType(info) == constant.ChannelTypeOpenRouter
	if isOpenRouter {
		if effort := claudeRequest.GetEfforts(); effort != "" {
			effortBytes, _ := common.Marshal(effort)
			openAIRequest.Verbosity = effortBytes
		}
		if claudeRequest.Thinking != nil {
			var reasoningConfig openRouterRequestReasoning
			if claudeRequest.Thinking.Type == "enabled" {
				reasoningConfig = openRouterRequestReasoning{
					Enabled:   true,
					MaxTokens: claudeRequest.Thinking.BudgetTokens,
				}
			} else if claudeRequest.Thinking.Type == "adaptive" {
				reasoningConfig = openRouterRequestReasoning{
					Enabled: true,
				}
			}
			reasoningJSON, err := common.Marshal(reasoningConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal reasoning: %w", err)
			}
			openAIRequest.Reasoning = reasoningJSON
		}
	} else if info != nil {
		thinkingSuffix := "-thinking"
		if strings.HasSuffix(info.OriginModelName, thinkingSuffix) &&
			!strings.HasSuffix(openAIRequest.Model, thinkingSuffix) {
			openAIRequest.Model = openAIRequest.Model + thinkingSuffix
		}
	}

	if len(claudeRequest.StopSequences) == 1 {
		openAIRequest.Stop = claudeRequest.StopSequences[0]
	} else if len(claudeRequest.StopSequences) > 1 {
		if len(claudeRequest.StopSequences) > 4 {
			return nil, fmt.Errorf("stop_sequences must contain at most 4 sequences for OpenAI Chat")
		}
		openAIRequest.Stop = claudeRequest.StopSequences
	}

	openAITools := make([]dto.ToolCallRequest, 0)
	if claudeRequest.Tools != nil {
		encodedTools, err := common.Marshal(claudeRequest.Tools)
		if err != nil {
			return nil, fmt.Errorf("invalid Claude tools: %w", err)
		}
		var rawTools []map[string]any
		if err := common.Unmarshal(encodedTools, &rawTools); err != nil {
			return nil, fmt.Errorf("invalid Claude tools: %w", err)
		}
		for index, rawTool := range rawTools {
			toolType := common.Interface2String(rawTool["type"])
			if toolType == "web_search_20250305" && common.Interface2String(rawTool["name"]) == "web_search" {
				if openAIRequest.WebSearchOptions == nil {
					openAIRequest.WebSearchOptions = &dto.WebSearchOptions{}
				}
				maxUses, _ := strconv.Atoi(common.Interface2String(rawTool["max_uses"]))
				switch maxUses {
				case 1:
					openAIRequest.WebSearchOptions.SearchContextSize = "low"
				case 10:
					openAIRequest.WebSearchOptions.SearchContextSize = "high"
				case 0, 5:
					openAIRequest.WebSearchOptions.SearchContextSize = "medium"
				default:
					return nil, fmt.Errorf("Claude web_search max_uses %d cannot be represented by OpenAI Chat", maxUses)
				}
				if location, ok := rawTool["user_location"].(map[string]any); ok {
					locationRaw, err := common.Marshal(map[string]any{"approximate": location})
					if err != nil {
						return nil, fmt.Errorf("tools[%d] user_location: %w", index, err)
					}
					openAIRequest.WebSearchOptions.UserLocation = locationRaw
				}
				continue
			}
			if toolType != "" && toolType != "function" {
				return nil, fmt.Errorf("Claude tools[%d] type %q cannot be converted to OpenAI Chat", index, toolType)
			}
			var claudeTool dto.Tool
			rawToolJSON, err := common.Marshal(rawTool)
			if err != nil {
				return nil, fmt.Errorf("invalid Claude tools[%d]: %w", index, err)
			}
			if err := common.Unmarshal(rawToolJSON, &claudeTool); err != nil {
				return nil, fmt.Errorf("invalid Claude tools[%d]: %w", index, err)
			}
			if claudeTool.Name == "" {
				return nil, fmt.Errorf("Claude tools[%d] is missing name", index)
			}
			openAITool := dto.ToolCallRequest{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        claudeTool.Name,
					Description: claudeTool.Description,
					Parameters:  claudeTool.InputSchema,
					Strict:      claudeTool.Strict,
				},
			}
			openAITools = append(openAITools, openAITool)
		}
	}
	openAIRequest.Tools = openAITools

	openAIMessages := make([]dto.Message, 0)
	if claudeRequest.System != nil {
		if claudeRequest.IsStringSystem() && claudeRequest.GetStringSystem() != "" {
			openAIMessage := dto.Message{
				Role: "system",
			}
			openAIMessage.SetStringContent(claudeRequest.GetStringSystem())
			openAIMessages = append(openAIMessages, openAIMessage)
		} else {
			systems := claudeRequest.ParseSystem()
			if len(systems) > 0 {
				openAIMessage := dto.Message{
					Role: "system",
				}
				isOpenRouterClaude := isOpenRouter && strings.HasPrefix(relaymeta.RelayInfoUpstreamModelName(info), "anthropic/claude")
				if isOpenRouterClaude {
					systemMediaMessages := make([]dto.MediaContent, 0, len(systems))
					for _, system := range systems {
						message := dto.MediaContent{
							Type:         "text",
							Text:         system.GetText(),
							CacheControl: system.CacheControl,
						}
						systemMediaMessages = append(systemMediaMessages, message)
					}
					openAIMessage.SetMediaContent(systemMediaMessages)
				} else {
					systemStr := ""
					for _, system := range systems {
						if system.Text != nil {
							systemStr += *system.Text
						}
					}
					openAIMessage.SetStringContent(systemStr)
				}
				openAIMessages = append(openAIMessages, openAIMessage)
			}
		}
	}

	for _, claudeMessage := range claudeRequest.Messages {
		openAIMessage := dto.Message{
			Role: claudeMessage.Role,
		}
		var thinkingContent strings.Builder
		if claudeMessage.IsStringContent() {
			openAIMessage.SetStringContent(claudeMessage.GetStringContent())
			openAIMessages = append(openAIMessages, openAIMessage)
		} else {
			content, err := claudeMessage.ParseContent()
			if err != nil {
				return nil, err
			}
			var toolCalls []dto.ToolCallRequest
			mediaMessages := make([]dto.MediaContent, 0, len(content))
			appendCurrentMessage := func() {
				if len(toolCalls) > 0 {
					openAIMessage.SetToolCalls(toolCalls)
				}
				if len(mediaMessages) > 0 {
					openAIMessage.SetMediaContent(mediaMessages)
				}
				if len(openAIMessage.ParseContent()) > 0 || len(openAIMessage.ToolCalls) > 0 || thinkingContent.Len() > 0 {
					if thinkingContent.Len() > 0 {
						thinking := thinkingContent.String()
						openAIMessage.ReasoningContent = &thinking
					}
					openAIMessages = append(openAIMessages, openAIMessage)
				}
			}

			for _, mediaMsg := range content {
				switch mediaMsg.Type {
				case "text", "input_text":
					message := dto.MediaContent{
						Type:         "text",
						Text:         mediaMsg.GetText(),
						CacheControl: mediaMsg.CacheControl,
					}
					mediaMessages = append(mediaMessages, message)
				case "image":
					mediaMessage, err := claudeImageToOpenAIMedia(mediaMsg)
					if err != nil {
						return nil, err
					}
					mediaMessages = append(mediaMessages, mediaMessage)
				case "document":
					mediaMessage, err := claudeDocumentToOpenAIMedia(mediaMsg)
					if err != nil {
						return nil, err
					}
					mediaMessages = append(mediaMessages, mediaMessage)
				case "thinking":
					if mediaMsg.Thinking != nil {
						thinkingContent.WriteString(*mediaMsg.Thinking)
					}
				case "tool_use":
					if mediaMsg.Id == "" || mediaMsg.Name == "" {
						return nil, fmt.Errorf("Claude tool_use requires id and name")
					}
					toolCall := dto.ToolCallRequest{
						ID:   mediaMsg.Id,
						Type: "function",
						Function: dto.FunctionRequest{
							Name:      mediaMsg.Name,
							Arguments: requestToJSONString(mediaMsg.Input),
						},
					}
					toolCalls = append(toolCalls, toolCall)
				case "tool_result":
					if mediaMsg.ToolUseId == "" {
						return nil, fmt.Errorf("Claude tool_result is missing tool_use_id")
					}
					// OpenAI Chat represents a tool result as its own message. Flush
					// any user/assistant content that preceded it so block order is
					// not changed when an Anthropic user turn mixes text and results.
					appendCurrentMessage()
					openAIMessage = dto.Message{Role: claudeMessage.Role}
					mediaMessages = mediaMessages[:0]
					toolCalls = toolCalls[:0]
					thinkingContent.Reset()
					toolName := mediaMsg.Name
					if toolName == "" {
						toolName = claudeRequest.SearchToolNameByToolCallId(mediaMsg.ToolUseId)
					}
					oaiToolMessage := dto.Message{
						Role:       "tool",
						ToolCallId: mediaMsg.ToolUseId,
					}
					if toolName != "" {
						oaiToolMessage.Name = &toolName
					}
					toolContent, err := claudeToolResultToOpenAIContent(mediaMsg)
					if err != nil {
						return nil, err
					}
					oaiToolMessage.Content = toolContent
					openAIMessages = append(openAIMessages, oaiToolMessage)
				}
			}

			appendCurrentMessage()
		}
	}

	openAIRequest.Messages = openAIMessages
	return &openAIRequest, nil
}

func applyClaudeStructuredOutputToOpenAI(request *dto.GeneralOpenAIRequest, claudeRequest dto.ClaudeRequest) error {
	var format map[string]any
	for _, raw := range [][]byte{claudeRequest.OutputConfig, claudeRequest.OutputFormat} {
		if len(raw) == 0 {
			continue
		}
		var value map[string]any
		if err := common.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("invalid Claude structured output configuration: %w", err)
		}
		if nested, ok := value["format"].(map[string]any); ok {
			value = nested
		}
		if _, ok := value["type"]; ok {
			if format != nil {
				return fmt.Errorf("Claude request contains multiple structured output formats")
			}
			format = value
		}
	}
	if format == nil {
		return nil
	}
	if common.Interface2String(format["type"]) != "json_schema" {
		return fmt.Errorf("Claude output format %q cannot be converted to OpenAI Chat", common.Interface2String(format["type"]))
	}
	if format["schema"] == nil {
		return fmt.Errorf("Claude structured output format is missing schema")
	}
	jsonSchema := map[string]any{
		"name":   "response",
		"schema": format["schema"],
		"strict": true,
	}
	encoded, err := common.Marshal(jsonSchema)
	if err != nil {
		return fmt.Errorf("marshal OpenAI response_format: %w", err)
	}
	request.ResponseFormat = &dto.ResponseFormat{
		Type:       "json_schema",
		JsonSchema: encoded,
	}
	return nil
}

func claudeImageToOpenAIMedia(mediaMsg dto.ClaudeMediaMessage) (dto.MediaContent, error) {
	if mediaMsg.Source == nil {
		return dto.MediaContent{}, fmt.Errorf("Claude image block is missing source")
	}
	imageURL := ""
	switch mediaMsg.Source.Type {
	case "url":
		imageURL = mediaMsg.Source.Url
	case "base64":
		if mediaMsg.Source.MediaType == "" {
			return dto.MediaContent{}, fmt.Errorf("Claude base64 image block is missing media_type")
		}
		imageURL = fmt.Sprintf("data:%s;base64,%s", mediaMsg.Source.MediaType, common.Interface2String(mediaMsg.Source.Data))
	default:
		return dto.MediaContent{}, fmt.Errorf("Claude image source type %q cannot be converted to OpenAI Chat", mediaMsg.Source.Type)
	}
	if imageURL == "" {
		return dto.MediaContent{}, fmt.Errorf("Claude image block is missing source data")
	}
	return dto.MediaContent{
		Type:     dto.ContentTypeImageURL,
		ImageUrl: &dto.MessageImageUrl{Url: imageURL},
	}, nil
}

func claudeDocumentToOpenAIMedia(mediaMsg dto.ClaudeMediaMessage) (dto.MediaContent, error) {
	if mediaMsg.Source == nil {
		return dto.MediaContent{}, fmt.Errorf("Claude document block is missing source")
	}
	if mediaMsg.Source.Type == "text" {
		return dto.MediaContent{
			Type: dto.ContentTypeText,
			Text: common.Interface2String(mediaMsg.Source.Data),
		}, nil
	}
	if mediaMsg.Source.Type != "base64" {
		return dto.MediaContent{}, fmt.Errorf("Claude document source type %q cannot be converted to OpenAI Chat", mediaMsg.Source.Type)
	}
	return dto.MediaContent{
		Type: dto.ContentTypeFile,
		File: &dto.MessageFile{
			FileName: "document.pdf",
			FileData: common.Interface2String(mediaMsg.Source.Data),
		},
	}, nil
}

func claudeToolResultToOpenAIContent(mediaMsg dto.ClaudeMediaMessage) (any, error) {
	if mediaMsg.IsError != nil && *mediaMsg.IsError {
		return nil, fmt.Errorf("Claude tool_result with is_error=true cannot be represented by OpenAI Chat")
	}
	if mediaMsg.IsStringContent() {
		return mediaMsg.GetStringContent(), nil
	}

	mediaContents := mediaMsg.ParseMediaContent()
	if len(mediaContents) == 0 {
		if mediaMsg.Content == nil {
			return "", nil
		}
		return nil, fmt.Errorf("Claude tool_result content has an unsupported shape")
	}
	oaiContents := make([]dto.MediaContent, 0, len(mediaContents))
	for _, content := range mediaContents {
		switch content.Type {
		case "text":
			oaiContents = append(oaiContents, dto.MediaContent{Type: dto.ContentTypeText, Text: content.GetText()})
		case "image":
			converted, err := claudeImageToOpenAIMedia(content)
			if err != nil {
				return nil, err
			}
			oaiContents = append(oaiContents, converted)
		case "document":
			converted, err := claudeDocumentToOpenAIMedia(content)
			if err != nil {
				return nil, err
			}
			oaiContents = append(oaiContents, converted)
		default:
			return nil, fmt.Errorf("Claude tool_result content block type %q cannot be converted to OpenAI Chat", content.Type)
		}
	}
	if len(oaiContents) == 1 && oaiContents[0].Type == dto.ContentTypeText {
		return oaiContents[0].Text, nil
	}
	return oaiContents, nil
}

func requestToJSONString(v interface{}) string {
	b, err := common.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
