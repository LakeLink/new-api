package jimeng

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/?Action=CVProcess&Version=2022-08-31", strings.TrimRight(info.ChannelBaseUrl, "/")), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	return errors.New("not implemented")
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return request, nil
}

type LogoInfo struct {
	AddLogo         *bool    `json:"add_logo,omitempty"`
	Position        *int     `json:"position,omitempty"`
	Language        *int     `json:"language,omitempty"`
	Opacity         *float64 `json:"opacity,omitempty"`
	LogoTextContent *string  `json:"logo_text_content,omitempty"`
}

type AIGCMeta struct {
	ContentProducer   *string `json:"content_producer,omitempty"`
	ProducerID        *string `json:"producer_id,omitempty"`
	ContentPropagator *string `json:"content_propagator,omitempty"`
	PropagateID       *string `json:"propagate_id,omitempty"`
}

type imageRequestPayload struct {
	ReqKey    string    `json:"req_key"`               // Service identifier, fixed value: jimeng_high_aes_general_v21_L
	Prompt    string    `json:"prompt"`                // Prompt for image generation, supports both Chinese and English
	Seed      *int64    `json:"seed,omitempty"`        // Random seed, default -1 (random)
	Width     *int      `json:"width,omitempty"`       // Image width, default 512, range [256, 768]
	Height    *int      `json:"height,omitempty"`      // Image height, default 512, range [256, 768]
	UsePreLLM *bool     `json:"use_pre_llm,omitempty"` // Enable text expansion, default true
	UseSR     *bool     `json:"use_sr,omitempty"`      // Enable super resolution, default true
	ReturnURL *bool     `json:"return_url,omitempty"`  // Whether to return image URL (valid for 24 hours)
	LogoInfo  *LogoInfo `json:"logo_info,omitempty"`   // Watermark information
	AIGCMeta  *AIGCMeta `json:"aigc_meta,omitempty"`   // Invisible AIGC provenance metadata
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if request.N != nil && *request.N != 1 {
		return nil, fmt.Errorf("Jimeng image generation supports exactly one image per request")
	}
	switch request.Model {
	case "jimeng_t2i_v30", "jimeng_t2i_v31", "jimeng_i2i_v30",
		"jimeng_t2i_v40", "jimeng_seedream46_cvtob":
		return nil, fmt.Errorf("%s requires Jimeng's asynchronous image protocol, which this adaptor does not support", request.Model)
	}
	payload := imageRequestPayload{
		ReqKey: request.Model,
		Prompt: request.Prompt,
	}

	if len(request.ExtraFields) > 0 {
		if err := common.Unmarshal(request.ExtraFields, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal extra fields: %w", err)
		}
	}
	// req_key selects the routed and priced model. Extra fields may only add
	// provider-native options, not replace standard OpenAI request fields.
	payload.ReqKey = request.Model
	payload.Prompt = request.Prompt
	switch request.ResponseFormat {
	case "", "url":
		payload.ReturnURL = common.GetPointer(true)
	case "b64_json":
		payload.ReturnURL = common.GetPointer(false)
	default:
		return nil, fmt.Errorf("unsupported response_format: %s", request.ResponseFormat)
	}
	if payload.Width != nil && (*payload.Width < 256 || *payload.Width > 768) {
		return nil, fmt.Errorf("width must be between 256 and 768")
	}
	if payload.Height != nil && (*payload.Height < 256 || *payload.Height > 768) {
		return nil, fmt.Errorf("height must be between 256 and 768")
	}
	if payload.LogoInfo != nil {
		if payload.LogoInfo.Position != nil && (*payload.LogoInfo.Position < 0 || *payload.LogoInfo.Position > 3) {
			return nil, fmt.Errorf("logo_info.position must be between 0 and 3")
		}
		if payload.LogoInfo.Language != nil && (*payload.LogoInfo.Language < 0 || *payload.LogoInfo.Language > 1) {
			return nil, fmt.Errorf("logo_info.language must be either 0 or 1")
		}
		if payload.LogoInfo.Opacity != nil && (*payload.LogoInfo.Opacity < 0 || *payload.LogoInfo.Opacity > 1) {
			return nil, fmt.Errorf("logo_info.opacity must be between 0 and 1")
		}
	}
	if payload.AIGCMeta != nil &&
		(payload.AIGCMeta.ProducerID == nil || strings.TrimSpace(*payload.AIGCMeta.ProducerID) == "") {
		return nil, fmt.Errorf("aigc_meta.producer_id is required when aigc_meta is provided")
	}

	return payload, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", err)
	}
	req, err := http.NewRequestWithContext(info.GetRelayContext(c.Request.Context()), c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", service.SanitizeNetworkError(err))
	}
	channel.ApplyUpstreamBodyMetadata(req, requestBody)
	err = Sign(c, req, info.ApiKey)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	resp, err := channel.DoRequest(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	return resp, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode == relayconstant.RelayModeImagesGenerations {
		usage, err = jimengImageHandler(c, resp, info)
	} else if info.IsStream {
		usage, err = openai.OaiStreamHandler(c, info, resp)
	} else {
		usage, err = openai.OpenaiHandler(c, info, resp)
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
