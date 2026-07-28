package zhipu_4v

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

func requestOpenAI2Zhipu(request dto.GeneralOpenAIRequest) (*dto.GeneralOpenAIRequest, error) {
	messages := make([]dto.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		if !message.IsStringContent() {
			mediaMessages := message.ParseContent()
			for j, mediaMessage := range mediaMessages {
				if mediaMessage.Type == dto.ContentTypeImageURL {
					imageUrl := mediaMessage.GetImageMedia()
					// check if base64
					if strings.HasPrefix(imageUrl.Url, "data:image/") {
						// 去除base64数据的URL前缀（如果有）
						if idx := strings.Index(imageUrl.Url, ","); idx != -1 {
							imageUrl.Url = imageUrl.Url[idx+1:]
						}
					}
					mediaMessage.ImageUrl = imageUrl
					mediaMessages[j] = mediaMessage
				}
			}
			message.SetMediaContent(mediaMessages)
		}
		messages = append(messages, message)
	}
	var stop []string
	switch value := request.Stop.(type) {
	case string:
		if value != "" {
			stop = []string{value}
		}
	case []string:
		stop = value
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok && text != "" {
				stop = append(stop, text)
			}
		}
	}
	if request.ResponseFormat != nil && request.ResponseFormat.Type != "" &&
		request.ResponseFormat.Type != "text" && request.ResponseFormat.Type != "json_object" {
		return nil, fmt.Errorf("zhipu response_format type %q is unsupported", request.ResponseFormat.Type)
	}
	out := &dto.GeneralOpenAIRequest{
		Model:           request.Model,
		Stream:          request.Stream,
		Messages:        messages,
		ReasoningEffort: request.ReasoningEffort,
		Temperature:     request.Temperature,
		TopP:            request.TopP,
		Stop:            stop,
		ResponseFormat:  request.ResponseFormat,
		Tools:           request.Tools,
		ToolChoice:      request.ToolChoice,
		THINKING:        request.THINKING,
	}
	if requestedMaxTokens := request.GetMaxTokensPointer(); requestedMaxTokens != nil {
		maxTokens := *requestedMaxTokens
		out.MaxTokens = &maxTokens
	}
	return out, nil
}
