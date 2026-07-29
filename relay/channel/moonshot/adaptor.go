package moonshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	channelconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
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
	adaptor := claude.Adaptor{}
	return adaptor.ConvertClaudeRequest(c, info, req)
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	//TODO implement me
	return nil, errors.New("not supported")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	adaptor := openai.Adaptor{}
	return adaptor.ConvertImageRequest(c, info, request)
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	baseURL := info.ChannelBaseUrl
	if specialPlan, ok := channelconstant.ChannelSpecialBases[baseURL]; ok {
		if info.RelayFormat == types.RelayFormatClaude {
			return fmt.Sprintf("%s/v1/messages", specialPlan.ClaudeBaseURL), nil
		}
		if info.RelayFormat == types.RelayFormatOpenAI {
			return fmt.Sprintf("%s/chat/completions", specialPlan.OpenAIBaseURL), nil
		}
	}

	switch info.RelayFormat {
	case types.RelayFormatClaude:
		return fmt.Sprintf("%s/anthropic/v1/messages", info.ChannelBaseUrl), nil
	default:
		if info.RelayMode == constant.RelayModeRerank {
			return fmt.Sprintf("%s/v1/rerank", info.ChannelBaseUrl), nil
		} else if info.RelayMode == constant.RelayModeEmbeddings {
			return fmt.Sprintf("%s/v1/embeddings", info.ChannelBaseUrl), nil
		} else if info.RelayMode == constant.RelayModeChatCompletions {
			return fmt.Sprintf("%s/v1/chat/completions", info.ChannelBaseUrl), nil
		} else if info.RelayMode == constant.RelayModeCompletions {
			return fmt.Sprintf("%s/v1/completions", info.ChannelBaseUrl), nil
		}
		return fmt.Sprintf("%s/v1/chat/completions", info.ChannelBaseUrl), nil
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", fmt.Sprintf("Bearer %s", info.ApiKey))
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	model := getUpstreamModelName(info, request.Model)
	if err := validateCurrentKimiRequest(model, request); err != nil {
		return nil, types.NewErrorWithStatusCode(
			err,
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	return request, nil
}

func getUpstreamModelName(info *relaycommon.RelayInfo, fallback string) string {
	if info != nil && info.ChannelMeta != nil && info.UpstreamModelName != "" {
		return info.UpstreamModelName
	}
	return fallback
}

func validateCurrentKimiRequest(model string, request *dto.GeneralOpenAIRequest) error {
	const maxKimiK3CompletionTokens = uint(1_048_576)

	isK3 := model == "kimi-k3"
	isK27 := model == "kimi-k2.7-code" || model == "kimi-k2.7-code-highspeed"
	isK26 := model == "kimi-k2.6"
	isK25 := model == "kimi-k2.5"
	if !isK3 && !isK27 && !isK26 && !isK25 {
		return nil
	}

	thinkingEnabled := true
	if len(request.THINKING) > 0 {
		if isK3 {
			return errors.New("kimi-k3 thinking is always enabled; use reasoning_effort to control inference depth")
		}
		if common.GetJsonType(request.THINKING) != "object" {
			return fmt.Errorf("%s thinking must be an object with type enabled or disabled", model)
		}
		var fields map[string]json.RawMessage
		if err := common.Unmarshal(request.THINKING, &fields); err != nil {
			return fmt.Errorf("%s thinking is invalid: %w", model, err)
		}
		for field := range fields {
			if field != "type" && field != "keep" {
				return fmt.Errorf("%s thinking contains unsupported field %q", model, field)
			}
			if isK25 && field == "keep" {
				return errors.New("kimi-k2.5 thinking.keep is not supported")
			}
		}
		var thinking struct {
			Type string          `json:"type"`
			Keep json.RawMessage `json:"keep"`
		}
		if err := common.Unmarshal(request.THINKING, &thinking); err != nil {
			return fmt.Errorf("%s thinking is invalid: %w", model, err)
		}
		switch {
		case thinking.Type == "enabled":
		case thinking.Type == "disabled" && !isK27:
			thinkingEnabled = false
		case isK27:
			return fmt.Errorf("%s thinking.type must be enabled", model)
		default:
			return fmt.Errorf("%s thinking.type must be enabled or disabled", model)
		}
		if len(thinking.Keep) > 0 && string(thinking.Keep) != "null" {
			var keep string
			if err := common.Unmarshal(thinking.Keep, &keep); err != nil || keep != "all" {
				return fmt.Errorf("%s thinking.keep must be all or null", model)
			}
		}
	}

	if isK3 {
		if request.ReasoningEffort != "" &&
			request.ReasoningEffort != "low" &&
			request.ReasoningEffort != "high" &&
			request.ReasoningEffort != "max" {
			return errors.New("kimi-k3 reasoning_effort must be low, high, or max")
		}
		if request.MaxTokens != nil && *request.MaxTokens > maxKimiK3CompletionTokens {
			return fmt.Errorf("kimi-k3 max_tokens must not exceed %d", maxKimiK3CompletionTokens)
		}
		if request.MaxCompletionTokens != nil && *request.MaxCompletionTokens > maxKimiK3CompletionTokens {
			return fmt.Errorf("kimi-k3 max_completion_tokens must not exceed %d", maxKimiK3CompletionTokens)
		}
	} else if request.ReasoningEffort != "" {
		return fmt.Errorf("%s does not support reasoning_effort; use thinking", model)
	}

	expectedTemperature := 1.0
	thinkingState := "enabled"
	if (isK26 || isK25) && !thinkingEnabled {
		expectedTemperature = 0.6
		thinkingState = "disabled"
	}
	if request.Temperature != nil && *request.Temperature != expectedTemperature {
		return fmt.Errorf("%s temperature must be %.1f when thinking is %s", model, expectedTemperature, thinkingState)
	}
	if request.TopP != nil && *request.TopP != 0.95 {
		return fmt.Errorf("%s top_p must be 0.95", model)
	}
	if request.N != nil && *request.N != 1 {
		return fmt.Errorf("%s n must be 1", model)
	}
	if request.PresencePenalty != nil && *request.PresencePenalty != 0 {
		return fmt.Errorf("%s presence_penalty must be 0", model)
	}
	if request.FrequencyPenalty != nil && *request.FrequencyPenalty != 0 {
		return fmt.Errorf("%s frequency_penalty must be 0", model)
	}
	if !isK3 && thinkingEnabled && len(request.Tools) > 0 && request.ToolChoice != nil {
		toolChoice, ok := request.ToolChoice.(string)
		if !ok || (toolChoice != "auto" && toolChoice != "none") {
			return fmt.Errorf("%s tool_choice must be auto or none when thinking is enabled", model)
		}
	}
	return nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	// TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		adaptor := claude.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	default:
		adaptor := openai.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	}
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
