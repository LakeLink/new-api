package jina

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/common_handler"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

type rerankRequest struct {
	Model           string `json:"model"`
	Query           any    `json:"query"`
	Documents       []any  `json:"documents"`
	TopN            *int   `json:"top_n,omitempty"`
	ReturnDocuments *bool  `json:"return_documents,omitempty"`
}

type embeddingRequest struct {
	Model         string `json:"model"`
	Input         any    `json:"input"`
	EmbeddingType string `json:"embedding_type,omitempty"`
	Dimensions    *int   `json:"dimensions,omitempty"`
}

type embeddingResponseMeta struct {
	Error any `json:"error,omitempty"`
	Usage struct {
		TotalTokens  int `json:"total_tokens"`
		PromptTokens int `json:"prompt_tokens"`
		ImageTokens  int `json:"image_tokens"`
		AudioTokens  int `json:"audio_tokens"`
		VideoTokens  int `json:"video_tokens"`
	} `json:"usage"`
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode == constant.RelayModeRerank {
		return fmt.Sprintf("%s/v1/rerank", info.ChannelBaseUrl), nil
	} else if info.RelayMode == constant.RelayModeEmbeddings {
		return fmt.Sprintf("%s/v1/embeddings", info.ChannelBaseUrl), nil
	}
	return "", errors.New("invalid relay mode")
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", fmt.Sprintf("Bearer %s", info.ApiKey))
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	// TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	if request.TopN != nil && *request.TopN < 1 {
		return nil, errors.New("jina rerank top_n must be at least 1")
	}

	allowedMedia := map[string]bool{}
	if strings.EqualFold(request.Model, "jina-reranker-m0") {
		allowedMedia["image"] = true
	}
	if field := findUnsupportedJinaMedia(request.Query, allowedMedia); field != "" {
		return nil, fmt.Errorf("jina model %s does not support %s input", request.Model, field)
	}
	for _, document := range request.Documents {
		if field := findUnsupportedJinaMedia(document, allowedMedia); field != "" {
			return nil, fmt.Errorf("jina model %s does not support %s input", request.Model, field)
		}
	}

	_, textQuery := request.QueryString()
	imageQuery := hasNonEmptyStringField(request.Query, "image")
	if !textQuery && (!strings.EqualFold(request.Model, "jina-reranker-m0") || !imageQuery) {
		return nil, errors.New("jina rerank query must be a non-empty string; jina-reranker-m0 also accepts an image object")
	}

	allowImages := strings.EqualFold(request.Model, "jina-reranker-m0")
	for _, document := range request.Documents {
		_, isText := document.(string)
		if isText {
			if strings.TrimSpace(document.(string)) == "" {
				return nil, errors.New("jina rerank documents must not contain empty strings")
			}
			continue
		}
		if hasNonEmptyStringField(document, "text") {
			continue
		}
		if allowImages && hasNonEmptyStringField(document, "image") {
			continue
		}
		return nil, errors.New("jina rerank documents must contain text; jina-reranker-m0 also accepts image objects")
	}

	return rerankRequest{
		Model:           request.Model,
		Query:           request.Query,
		Documents:       request.Documents,
		TopN:            request.TopN,
		ReturnDocuments: request.ReturnDocuments,
	}, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	if !hasValidEmbeddingInput(request.Input) {
		return nil, errors.New("jina embedding input must contain non-empty text or supported media")
	}
	allowedMedia := map[string]bool{}
	switch strings.ToLower(request.Model) {
	case "jina-embeddings-v4":
		allowedMedia["image"] = true
		allowedMedia["pdf"] = true
	case "jina-clip-v1", "jina-clip-v2":
		allowedMedia["image"] = true
	case "jina-embeddings-v5-omni-nano", "jina-embeddings-v5-omni-small":
		allowedMedia["image"] = true
		allowedMedia["audio"] = true
		allowedMedia["video"] = true
		allowedMedia["pdf"] = true
	}
	if field := findUnsupportedJinaMedia(request.Input, allowedMedia); field != "" {
		return nil, fmt.Errorf("jina model %s does not support %s input", request.Model, field)
	}
	if request.Dimensions != nil && *request.Dimensions < 1 {
		return nil, errors.New("jina embedding dimensions must be at least 1")
	}
	if request.EncodingFormat != "" {
		switch strings.ToLower(request.EncodingFormat) {
		case "float", "base64", "binary", "ubinary":
		default:
			return nil, fmt.Errorf("unsupported jina embedding encoding format %q", request.EncodingFormat)
		}
	}

	return embeddingRequest{
		Model:         request.Model,
		Input:         request.Input,
		EmbeddingType: strings.ToLower(request.EncodingFormat),
		Dimensions:    request.Dimensions,
	}, nil
}

func hasNonEmptyStringField(value any, field string) bool {
	var data string
	switch object := value.(type) {
	case map[string]any:
		data, _ = object[field].(string)
	case map[string]string:
		data = object[field]
	}
	return strings.TrimSpace(data) != ""
}

func hasValidEmbeddingInput(input any) bool {
	switch value := input.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case []any:
		if len(value) == 0 {
			return false
		}
		for _, item := range value {
			if !hasValidEmbeddingInput(item) {
				return false
			}
		}
		return true
	case []string:
		if len(value) == 0 {
			return false
		}
		for _, item := range value {
			if strings.TrimSpace(item) == "" {
				return false
			}
		}
		return true
	case map[string]any:
		if hasSupportedEmbeddingField(value) {
			return true
		}
		content, ok := value["content"].([]any)
		return ok && len(content) > 0 && hasValidEmbeddingInput(content)
	case map[string]string:
		converted := make(map[string]any, len(value))
		for key, item := range value {
			converted[key] = item
		}
		return hasSupportedEmbeddingField(converted)
	default:
		return false
	}
}

func hasSupportedEmbeddingField(input map[string]any) bool {
	for _, field := range []string{"text", "image", "audio", "video", "pdf"} {
		data, ok := input[field].(string)
		if ok && strings.TrimSpace(data) != "" {
			return true
		}
	}
	return false
}

func findUnsupportedJinaMedia(input any, allowed map[string]bool) string {
	switch value := input.(type) {
	case []any:
		for _, item := range value {
			if field := findUnsupportedJinaMedia(item, allowed); field != "" {
				return field
			}
		}
	case map[string]any:
		for _, field := range []string{"image", "audio", "video", "pdf"} {
			data, ok := value[field].(string)
			if ok && strings.TrimSpace(data) != "" && !allowed[field] {
				return field
			}
		}
		if content, ok := value["content"]; ok {
			return findUnsupportedJinaMedia(content, allowed)
		}
	case map[string]string:
		for _, field := range []string{"image", "audio", "video", "pdf"} {
			if strings.TrimSpace(value[field]) != "" && !allowed[field] {
				return field
			}
		}
	}
	return ""
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode == constant.RelayModeRerank {
		usage, err = common_handler.RerankHandler(c, info, resp)
		if err == nil {
			rerankUsage := usage.(*dto.Usage)
			if rerankUsage.TotalTokens <= 0 || rerankUsage.TotalTokens > common.MaxQuota {
				logger.LogWarn(c, fmt.Sprintf("invalid jina rerank usage total_tokens=%d; using the bounded pre-consume estimate", rerankUsage.TotalTokens))
				estimate := info.GetEstimatePromptTokens()
				rerankUsage.PromptTokens = estimate
				rerankUsage.CompletionTokens = 0
				rerankUsage.TotalTokens = estimate
			}
		}
	} else if info.RelayMode == constant.RelayModeEmbeddings {
		usage, err = jinaEmbeddingHandler(c, info, resp)
	}
	return
}

func jinaEmbeddingHandler(c *gin.Context, _ *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	logger.LogDebug(
		c,
		"jina embedding response received: status=%d bytes=%d",
		resp.StatusCode,
		len(responseBody),
	)

	var response embeddingResponseMeta
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	if openAIError := dto.GetOpenAIError(response.Error); openAIError != nil && openAIError.Type != "" {
		return nil, types.WithOpenAIError(*openAIError, resp.StatusCode)
	}

	providerUsage := response.Usage
	if providerUsage.TotalTokens <= 0 || providerUsage.TotalTokens > common.MaxQuota ||
		providerUsage.PromptTokens < 0 || providerUsage.PromptTokens > providerUsage.TotalTokens ||
		providerUsage.ImageTokens < 0 || providerUsage.ImageTokens > providerUsage.TotalTokens ||
		providerUsage.AudioTokens < 0 || providerUsage.AudioTokens > providerUsage.TotalTokens ||
		providerUsage.VideoTokens < 0 || providerUsage.VideoTokens > providerUsage.TotalTokens {
		return nil, types.NewOpenAIError(
			fmt.Errorf("invalid jina embedding usage: total=%d prompt=%d image=%d audio=%d video=%d",
				providerUsage.TotalTokens, providerUsage.PromptTokens, providerUsage.ImageTokens, providerUsage.AudioTokens, providerUsage.VideoTokens),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}

	billingUsage := &dto.Usage{
		PromptTokens: providerUsage.TotalTokens,
		TotalTokens:  providerUsage.TotalTokens,
		PromptTokensDetails: dto.InputTokenDetails{
			ImageTokens: providerUsage.ImageTokens,
			AudioTokens: providerUsage.AudioTokens,
		},
	}
	service.IOCopyBytesGracefully(c, resp, responseBody)
	return billingUsage, nil
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
