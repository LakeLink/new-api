package baidu_v2

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.ClaudeRequest) (any, error) {
	adaptor := openai.Adaptor{}
	return adaptor.ConvertClaudeRequest(c, info, req)
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if request.ResponseFormat != "" && !strings.EqualFold(request.ResponseFormat, "url") {
		return nil, fmt.Errorf("baidu v2 image response_format %q is unsupported; use url", request.ResponseFormat)
	}
	if info.RelayMode == constant.RelayModeImagesEdits && c != nil && c.Request != nil &&
		strings.Contains(strings.ToLower(c.Request.Header.Get("Content-Type")), "multipart/form-data") {
		return nil, errors.New("baidu v2 image edits require a JSON image URL or data URL")
	}

	payload := map[string]any{
		"model":  request.Model,
		"prompt": request.Prompt,
	}
	if request.N != nil {
		if *request.N < 1 || *request.N > 4 {
			return nil, errors.New("baidu v2 image n must be between 1 and 4")
		}
		if strings.EqualFold(request.Model, "qwen-image") && *request.N != 1 {
			return nil, errors.New("baidu v2 qwen-image only supports n=1")
		}
		payload["n"] = *request.N
	}
	if request.Size != "" {
		payload["size"] = request.Size
	}
	if request.Watermark != nil {
		payload["watermark"] = request.Watermark
	}
	if request.Stream != nil {
		payload["stream"] = request.Stream
	}
	if len(request.User) > 0 {
		payload["user"] = request.User
	}
	if len(request.Image) > 0 {
		payload["image"] = request.Image
	}
	if info.RelayMode == constant.RelayModeImagesEdits {
		if len(request.Image) == 0 {
			return nil, errors.New("image is required for baidu v2 image edits")
		}
	}
	for _, field := range []string{"negative_prompt", "steps", "seed", "guidance", "prompt_extend"} {
		if value, ok := request.Extra[field]; ok {
			payload[field] = value
		}
	}

	// Materialize the raw JSON values once so malformed provider-native
	// extensions fail during conversion instead of reaching the upstream.
	encoded, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var converted map[string]any
	if err := common.Unmarshal(encoded, &converted); err != nil {
		return nil, err
	}
	return converted, nil
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	switch info.RelayMode {
	case constant.RelayModeChatCompletions:
		return fmt.Sprintf("%s/v2/chat/completions", info.ChannelBaseUrl), nil
	case constant.RelayModeEmbeddings:
		return fmt.Sprintf("%s/v2/embeddings", info.ChannelBaseUrl), nil
	case constant.RelayModeImagesGenerations:
		return fmt.Sprintf("%s/v2/images/generations", info.ChannelBaseUrl), nil
	case constant.RelayModeImagesEdits:
		return fmt.Sprintf("%s/v2/images/edits", info.ChannelBaseUrl), nil
	case constant.RelayModeRerank:
		return fmt.Sprintf("%s/v2/rerank", info.ChannelBaseUrl), nil
	default:
	}
	return "", fmt.Errorf("unsupported relay mode: %d", info.RelayMode)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	keyParts := strings.Split(info.ApiKey, "|")
	if len(keyParts) == 0 || keyParts[0] == "" {
		return errors.New("invalid API key: authorization token is required")
	}
	if len(keyParts) > 1 {
		if keyParts[1] != "" {
			req.Set("appid", keyParts[1])
		}
	}
	req.Set("Authorization", "Bearer "+keyParts[0])
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if strings.HasSuffix(info.UpstreamModelName, "-search") {
		info.UpstreamModelName = strings.TrimSuffix(info.UpstreamModelName, "-search")
		request.Model = info.UpstreamModelName
		if len(request.WebSearch) == 0 {
			toMap := request.ToMap()
			toMap["web_search"] = map[string]any{
				"enable":          true,
				"enable_citation": true,
				"enable_trace":    true,
				"enable_status":   false,
			}
			return toMap, nil
		}
		return request, nil
	}
	return request, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	query, ok := request.QueryString()
	if !ok {
		return nil, errors.New("baidu v2 rerank query must be a non-empty string")
	}
	request.Query = query
	return request, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	// TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	adaptor := openai.Adaptor{}
	usage, err = adaptor.DoResponse(c, resp, info)
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
