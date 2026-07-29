package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/QuantumNous/new-api/types"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

func GetAndValidateRequest(c *gin.Context, format types.RelayFormat) (request dto.Request, err error) {
	relayMode := relayconstant.Path2RelayMode(c.Request.URL.Path)

	switch format {
	case types.RelayFormatOpenAI:
		request, err = GetAndValidateTextRequest(c, relayMode)
	case types.RelayFormatGemini:
		if strings.Contains(c.Request.URL.Path, ":embedContent") {
			request, err = GetAndValidateGeminiEmbeddingRequest(c)
		} else if strings.Contains(c.Request.URL.Path, ":batchEmbedContents") {
			request, err = GetAndValidateGeminiBatchEmbeddingRequest(c)
		} else {
			request, err = GetAndValidateGeminiRequest(c)
		}
	case types.RelayFormatClaude:
		request, err = GetAndValidateClaudeRequest(c)
	case types.RelayFormatOpenAIResponses:
		request, err = GetAndValidateResponsesRequest(c)
	case types.RelayFormatOpenAIResponsesCompaction:
		request, err = GetAndValidateResponsesCompactionRequest(c)

	case types.RelayFormatOpenAIImage:
		request, err = GetAndValidOpenAIImageRequest(c, relayMode)
	case types.RelayFormatEmbedding:
		request, err = GetAndValidateEmbeddingRequest(c, relayMode)
	case types.RelayFormatRerank:
		request, err = GetAndValidateRerankRequest(c)
	case types.RelayFormatOpenAIAudio:
		request, err = GetAndValidAudioRequest(c, relayMode)
	case types.RelayFormatOpenAIRealtime:
		request = &dto.BaseRequest{}
	default:
		return nil, fmt.Errorf("unsupported relay format: %s", format)
	}
	return request, err
}

func GetAndValidAudioRequest(c *gin.Context, relayMode int) (*dto.AudioRequest, error) {
	audioRequest := &dto.AudioRequest{}
	err := common.UnmarshalBodyReusable(c, audioRequest)
	if err != nil {
		return nil, err
	}
	if exceedsMaxTokensLimit(audioRequest.MaxNewTokens) {
		return nil, errors.New("max_new_tokens is invalid")
	}
	switch relayMode {
	case relayconstant.RelayModeAudioSpeech:
		if audioRequest.Model == "" {
			return nil, errors.New("model is required")
		}
	default:
		if audioRequest.Model == "" {
			return nil, errors.New("model is required")
		}
		if audioRequest.ResponseFormat == "" {
			audioRequest.ResponseFormat = "json"
		}
	}
	return audioRequest, nil
}

func GetAndValidateRerankRequest(c *gin.Context) (*dto.RerankRequest, error) {
	var rerankRequest *dto.RerankRequest
	err := common.UnmarshalBodyReusable(c, &rerankRequest)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("getAndValidateTextRequest failed: %s", err.Error()))
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	if err := ValidateRerankRequest(rerankRequest, common.GetContextKeyInt(c, constant.ContextKeyChannelType) == constant.ChannelTypeCohere); err != nil {
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	return rerankRequest, nil
}

// ValidateRerankRequest validates a materialized rerank request. Relay
// handlers call it again after channel parameter overrides because document
// and chunk counts are billing multipliers for Cohere.
func ValidateRerankRequest(request *dto.RerankRequest, cohere bool) error {
	if request == nil {
		return errors.New("request is required")
	}
	if !request.HasValidQuery() {
		return errors.New("query is empty")
	}
	if len(request.Documents) == 0 {
		return errors.New("documents is empty")
	}
	if request.TopN != nil && *request.TopN <= 0 {
		return errors.New("top_n must be at least 1")
	}
	if request.MaxChunksPerDoc != nil && request.MaxChunkPerDoc != nil &&
		*request.MaxChunksPerDoc != *request.MaxChunkPerDoc {
		return errors.New("max_chunks_per_doc conflicts with legacy max_chunk_per_doc")
	}
	if maxChunksPerDoc := request.GetMaxChunksPerDoc(); maxChunksPerDoc != nil && *maxChunksPerDoc <= 0 {
		return errors.New("max_chunks_per_doc must be at least 1")
	}
	if cohere {
		if _, err := relaycommon.EstimateCohereRerankSearchUnits(request); err != nil {
			return err
		}
	}
	return nil
}

func GetAndValidateEmbeddingRequest(c *gin.Context, relayMode int) (*dto.EmbeddingRequest, error) {
	var embeddingRequest *dto.EmbeddingRequest
	err := common.UnmarshalBodyReusable(c, &embeddingRequest)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("getAndValidateTextRequest failed: %s", err.Error()))
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	if embeddingRequest.Input == nil {
		return nil, fmt.Errorf("input is empty")
	}
	if relayMode == relayconstant.RelayModeModerations && embeddingRequest.Model == "" {
		embeddingRequest.Model = "omni-moderation-latest"
	}
	if relayMode == relayconstant.RelayModeEmbeddings && embeddingRequest.Model == "" {
		embeddingRequest.Model = c.Param("model")
	}
	return embeddingRequest, nil
}

// maxTokensLimit bounds user-supplied max token fields. These values feed
// pre-consume quota math (preConsumedTokens * ratio); an unbounded value can
// overflow the conversion and corrupt billing.
const maxTokensLimit = common.MaxTokensLimit

func exceedsMaxTokensLimit(values ...*uint) bool {
	for _, v := range values {
		if lo.FromPtrOr(v, uint(0)) > maxTokensLimit {
			return true
		}
	}
	return false
}

func exceedsMaxTokenProductLimit(maxTokens *uint, count int) bool {
	if maxTokens == nil || count <= 1 {
		return false
	}
	return *maxTokens > maxTokensLimit/uint(count)
}

func validateOptionalTokenBudget(budget *int, field string) error {
	if budget == nil {
		return nil
	}
	if *budget < 0 || uint64(*budget) > uint64(maxTokensLimit) {
		return fmt.Errorf("%s is invalid", field)
	}
	return nil
}

func GetAndValidateResponsesRequest(c *gin.Context) (*dto.OpenAIResponsesRequest, error) {
	request := &dto.OpenAIResponsesRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if err := ValidateResponsesRequest(request); err != nil {
		return nil, err
	}
	return request, nil
}

// ValidateResponsesRequest validates a fully materialized Responses request.
// In addition to initial client validation, callers use this after channel
// field filtering and parameter overrides so the exact outbound request stays
// within the same protocol and billing limits.
func ValidateResponsesRequest(request *dto.OpenAIResponsesRequest) error {
	if request == nil {
		return errors.New("request is required")
	}
	if request.Model == "" {
		return errors.New("model is required")
	}
	if request.Input == nil {
		return errors.New("input is required")
	}
	if exceedsMaxTokensLimit(request.MaxOutputTokens) {
		return errors.New("max_output_tokens is invalid")
	}
	if request.MaxToolCalls != nil && *request.MaxToolCalls > uint(common.MaxTextToolCallCount) {
		return fmt.Errorf("max_tool_calls must not exceed %d", common.MaxTextToolCallCount)
	}
	stream := request.Stream != nil && *request.Stream
	if err := validateResponsesTools(request.Tools, stream); err != nil {
		return err
	}
	return nil
}

func validateResponsesTools(tools json.RawMessage, stream bool) error {
	if len(tools) == 0 {
		return nil
	}
	if common.GetJsonType(tools) != "array" {
		return errors.New("tools must be an array of objects")
	}

	var definitions []json.RawMessage
	if err := common.Unmarshal(tools, &definitions); err != nil {
		return fmt.Errorf("tools must be an array of objects: %w", err)
	}
	configuredWebSearchType := ""
	imageGenerationConfigured := false
	for index, rawDefinition := range definitions {
		if common.GetJsonType(rawDefinition) != "object" {
			return fmt.Errorf("tools[%d] must be an object", index)
		}
		var definition map[string]any
		if err := common.Unmarshal(rawDefinition, &definition); err != nil {
			return fmt.Errorf("tools[%d] must be an object: %w", index, err)
		}
		typeValue, exists := definition["type"]
		if !exists {
			return fmt.Errorf("tools[%d].type is required", index)
		}
		toolType, ok := typeValue.(string)
		if !ok || toolType == "" || toolType != strings.TrimSpace(toolType) {
			return fmt.Errorf("tools[%d].type must be a non-empty string", index)
		}
		if toolType == "function" {
			name, ok := definition["name"].(string)
			if !ok || strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) {
				return fmt.Errorf("tools[%d].name must be a non-empty string for function tools", index)
			}
		}
		if toolType == "image_generation" {
			if imageGenerationConfigured {
				return errors.New("tools contain duplicate image_generation definitions")
			}
			imageGenerationConfigured = true
			if err := validateResponsesImageGenerationTool(definition, index, stream); err != nil {
				return err
			}
			continue
		}

		_, isWebSearch := dto.ResponsesWebSearchPricingKey(toolType)
		if !isWebSearch {
			if strings.HasPrefix(toolType, "web_search") {
				return fmt.Errorf("tools[%d].type %q is not a supported Responses web-search tool", index, toolType)
			}
			continue
		}
		if configuredWebSearchType != "" {
			return fmt.Errorf("tools contain ambiguous duplicate web-search definitions %q and %q", configuredWebSearchType, toolType)
		}
		configuredWebSearchType = toolType

		if contextSizeValue, exists := definition["search_context_size"]; exists {
			contextSize, ok := contextSizeValue.(string)
			if !ok || (contextSize != "low" && contextSize != "medium" && contextSize != "high") {
				return fmt.Errorf("tools[%d].search_context_size must be one of: high, medium, low", index)
			}
		}
	}
	return nil
}

func GetAndValidateResponsesCompactionRequest(c *gin.Context) (*dto.OpenAIResponsesCompactionRequest, error) {
	request := &dto.OpenAIResponsesCompactionRequest{}
	if err := common.UnmarshalBodyReusable(c, request); err != nil {
		return nil, err
	}
	if request.Model == "" {
		return nil, errors.New("model is required")
	}
	return request, nil
}

func GetAndValidOpenAIImageRequest(c *gin.Context, relayMode int) (*dto.ImageRequest, error) {
	imageRequest := &dto.ImageRequest{}

	switch relayMode {
	case relayconstant.RelayModeImagesEdits:
		if strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
			form, err := common.ParseMultipartFormReusable(c)
			if err != nil {
				return nil, fmt.Errorf("failed to parse image edit form request: %w", err)
			}
			formData := url.Values(form.Value)
			c.Request.MultipartForm = form
			c.Request.PostForm = formData
			singleValueFields := []string{
				"model", "prompt", "n", "quality", "size", "stream", "watermark",
				"background", "moderation", "output_format", "output_compression",
				"partial_images", "input_fidelity", "response_format", "style", "user",
			}
			for _, field := range singleValueFields {
				if len(form.Value[field]) > 1 {
					return nil, fmt.Errorf("%s must not be repeated", field)
				}
			}
			imageRequest.Prompt = formData.Get("prompt")
			imageRequest.Model = formData.Get("model")
			if formData.Has("n") {
				nValue := strings.TrimSpace(formData.Get("n"))
				n, err := strconv.ParseUint(nValue, 10, 64)
				if err != nil || n < 1 || n > uint64(dto.MaxImageN) {
					return nil, fmt.Errorf("n must be an integer between 1 and %d", dto.MaxImageN)
				}
				imageRequest.N = common.GetPointer(uint(n))
			}
			imageRequest.Quality = formData.Get("quality")
			imageRequest.Size = formData.Get("size")
			imageRequest.ResponseFormat = formData.Get("response_format")
			if formData.Has("stream") {
				streamValue := strings.TrimSpace(formData.Get("stream"))
				stream, err := strconv.ParseBool(streamValue)
				if err != nil {
					return nil, fmt.Errorf("invalid stream value: %w", err)
				}
				imageRequest.Stream = common.GetPointer(stream)
			}
			rawStringFields := []struct {
				name   string
				target *json.RawMessage
			}{
				{name: "background", target: &imageRequest.Background},
				{name: "moderation", target: &imageRequest.Moderation},
				{name: "output_format", target: &imageRequest.OutputFormat},
				{name: "input_fidelity", target: &imageRequest.InputFidelity},
				{name: "style", target: &imageRequest.Style},
				{name: "user", target: &imageRequest.User},
			}
			for _, field := range rawStringFields {
				if values, exists := form.Value[field.name]; exists {
					raw, err := common.Marshal(values[0])
					if err != nil {
						return nil, fmt.Errorf("encode %s: %w", field.name, err)
					}
					*field.target = raw
				}
			}
			rawIntegerFields := []struct {
				name   string
				target *json.RawMessage
				max    uint64
			}{
				{name: "output_compression", target: &imageRequest.OutputCompression, max: 100},
				{name: "partial_images", target: &imageRequest.PartialImages, max: dto.MaxOpenAIImagePartialImages},
			}
			for _, field := range rawIntegerFields {
				if values, exists := form.Value[field.name]; exists {
					rawValue := strings.TrimSpace(values[0])
					value, err := strconv.ParseUint(rawValue, 10, 64)
					if err != nil || value > field.max {
						return nil, fmt.Errorf("%s must be an integer between 0 and %d", field.name, field.max)
					}
					*field.target = json.RawMessage(strconv.FormatUint(value, 10))
				}
			}

			if formData.Has("watermark") {
				watermark, err := strconv.ParseBool(strings.TrimSpace(formData.Get("watermark")))
				if err != nil {
					return nil, fmt.Errorf("invalid watermark value: %w", err)
				}
				imageRequest.Watermark = &watermark
			}
			if err := ValidateOpenAIImageRequest(imageRequest, relayMode, false); err != nil {
				return nil, err
			}
			if dto.IsXAIImageModel(imageRequest.Model) {
				return nil, errors.New("xAI image edits require application/json")
			}
			if err := ValidateOpenAIImageMultipartFiles(imageRequest, form); err != nil {
				return nil, err
			}
			return imageRequest, nil
		}
		fallthrough
	default:
		err := common.UnmarshalBodyReusable(c, imageRequest)
		if err != nil {
			return nil, err
		}

		// Provider-native image count fields must be reconciled before pricing.
		// Leaving them independent from n would pre-consume quota for one image
		// while sending a larger native batch upstream.
		if rawBatchSize, ok := imageRequest.Extra["batch_size"]; ok {
			batchSize, err := validateNativeImageCount(rawBatchSize, "batch_size", dto.MaxSiliconFlowImageBatchSize)
			if err != nil {
				return nil, err
			}
			if batchSize != nil {
				imageRequest.N = common.GetPointer(*batchSize)
			}
		}

		var replicateCounts [][]byte
		if len(imageRequest.ExtraFields) > 0 {
			var fields map[string]json.RawMessage
			if err := common.Unmarshal(imageRequest.ExtraFields, &fields); err == nil {
				if raw, ok := fields["num_outputs"]; ok {
					replicateCounts = append(replicateCounts, raw)
				}
			}
		}
		if rawInput, ok := imageRequest.Extra["input"]; ok {
			var input map[string]json.RawMessage
			if err := common.Unmarshal(rawInput, &input); err == nil {
				if raw, ok := input["num_outputs"]; ok {
					replicateCounts = append(replicateCounts, raw)
				}
			}
		}
		if raw, ok := imageRequest.Extra["num_outputs"]; ok {
			replicateCounts = append(replicateCounts, raw)
		}
		for _, raw := range replicateCounts {
			numOutputs, err := validateNativeImageCount(raw, "num_outputs", dto.MaxImageN)
			if err != nil {
				return nil, err
			}
			if numOutputs != nil {
				imageRequest.N = common.GetPointer(*numOutputs)
			}
		}

	}
	jsonEdit := relayMode == relayconstant.RelayModeImagesEdits &&
		strings.HasPrefix(strings.ToLower(c.Request.Header.Get("Content-Type")), "application/json")
	if err := ValidateOpenAIImageRequest(imageRequest, relayMode, jsonEdit); err != nil {
		return nil, err
	}
	if dto.IsXAIImageModel(imageRequest.Model) {
		if err := ValidateXAIImageRequest(imageRequest, relayMode); err != nil {
			return nil, err
		}
	}
	return imageRequest, nil
}

func validateNativeImageCount(raw []byte, field string, max uint) (*uint, error) {
	var value *uint
	if err := common.Unmarshal(raw, &value); err != nil || (value != nil && (*value < 1 || *value > max || *value > dto.MaxImageN)) {
		return nil, fmt.Errorf("%s must be an integer between 1 and %d", field, max)
	}
	return value, nil
}

func GetAndValidateClaudeRequest(c *gin.Context) (textRequest *dto.ClaudeRequest, err error) {
	textRequest = &dto.ClaudeRequest{}
	err = common.UnmarshalBodyReusable(c, textRequest)
	if err != nil {
		return nil, err
	}
	if err := ValidateClaudeRequest(textRequest); err != nil {
		return nil, err
	}

	//if textRequest.Stream {
	//	relayInfo.IsStream = true
	//}

	return textRequest, nil
}

// ValidateClaudeRequest validates both an incoming request and the exact
// materialized request produced after conversion/parameter overrides.
func ValidateClaudeRequest(request *dto.ClaudeRequest) error {
	if request == nil {
		return errors.New("request is required")
	}
	if len(request.Messages) == 0 {
		return errors.New("field messages is required")
	}
	if request.Model == "" {
		return errors.New("field model is required")
	}
	if exceedsMaxTokensLimit(request.MaxTokens, request.MaxTokensToSample) {
		return errors.New("max_tokens is invalid")
	}
	if request.Thinking != nil {
		if err := validateOptionalTokenBudget(request.Thinking.BudgetTokens, "thinking.budget_tokens"); err != nil {
			return err
		}
	}
	if request.ToolChoice != nil {
		toolChoice, err := common.Any2Type[dto.ClaudeToolChoice](request.ToolChoice)
		if err != nil {
			return fmt.Errorf("tool_choice is invalid: %w", err)
		}
		switch toolChoice.Type {
		case "auto", "any":
			if toolChoice.Name != "" {
				return errors.New("tool_choice.name is only valid when tool_choice.type is tool")
			}
		case "tool":
			if toolChoice.Name == "" {
				return errors.New("tool_choice.name is required when tool_choice.type is tool")
			}
		case "none":
			if toolChoice.Name != "" || toolChoice.DisableParallelToolUse != nil {
				return errors.New("tool_choice type none does not accept name or disable_parallel_tool_use")
			}
		default:
			return errors.New("tool_choice.type must be one of: auto, any, tool, none")
		}
	}
	_, _, claudeEffortAlias := reasoning.ParseClaudeEffortSuffix(request.Model)
	if reasoning.IsClaudeSamplingRestrictedModel(request.Model) && !claudeEffortAlias {
		if request.Temperature != nil && *request.Temperature != 1 {
			return errors.New("temperature is not supported for this Claude model")
		}
		if request.TopP != nil && (*request.TopP < 0.99 || *request.TopP > 1) {
			return errors.New("top_p is not supported for this Claude model")
		}
		if request.TopK != nil {
			return errors.New("top_k is not supported for this Claude model")
		}
	}
	if reasoning.IsClaudeAdaptiveThinkingOnlyModel(request.Model) && request.Thinking != nil && !claudeEffortAlias {
		switch request.Thinking.Type {
		case "enabled":
			return errors.New("manual thinking budgets are not supported for this Claude model")
		case "disabled":
			if reasoning.IsClaudeAlwaysThinkingModel(request.Model) {
				return errors.New("thinking cannot be disabled for this Claude model")
			}
		}
	}
	effort := ""
	if len(request.OutputConfig) > 0 {
		var outputConfig dto.OutputConfigForEffort
		if err := common.Unmarshal(request.OutputConfig, &outputConfig); err != nil {
			return fmt.Errorf("output_config is invalid: %w", err)
		}
		effort = outputConfig.Effort
		if effort != "" && !reasoning.IsClaudeEffortLevel(effort) {
			return errors.New("output_config.effort is invalid")
		}
	}
	if reasoning.IsClaudeOpus5Model(request.Model) && !claudeEffortAlias && request.Thinking != nil &&
		request.Thinking.Type == "disabled" && (effort == "xhigh" || effort == "max") {
		return errors.New("Claude Opus 5 cannot disable thinking at xhigh or max effort")
	}
	return validateClaudeWebSearchToolLimits(request.Tools)
}

func validateClaudeWebSearchToolLimits(tools any) error {
	_, _, err := ClaudeWebSearchLimit(tools)
	return err
}

// ClaudeWebSearchLimit returns the effective request-wide cap for configured
// Claude web-search tools. An omitted max_uses is conservatively bounded by
// the gateway's global tool-call limit.
func ClaudeWebSearchLimit(tools any) (uint, bool, error) {
	if tools == nil {
		return 0, false, nil
	}
	encoded, err := common.Marshal(tools)
	if err != nil {
		return 0, false, fmt.Errorf("encode Claude tools: %w", err)
	}
	var definitions []struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		MaxUses *uint  `json:"max_uses,omitempty"`
	}
	if err := common.Unmarshal(encoded, &definitions); err != nil {
		return 0, false, fmt.Errorf("Claude tools must be an array: %w", err)
	}
	configured := false
	unbounded := false
	total := uint(0)
	for index, definition := range definitions {
		if !strings.HasPrefix(definition.Type, "web_search") {
			continue
		}
		configured = true
		if definition.Name != "web_search" {
			return 0, false, fmt.Errorf("Claude tools[%d].name must be web_search for a web-search tool", index)
		}
		if definition.MaxUses == nil {
			unbounded = true
			continue
		}
		if *definition.MaxUses > uint(common.MaxTextToolCallCount)-total {
			return 0, false, fmt.Errorf("Claude web search max_uses must not exceed %d per request", common.MaxTextToolCallCount)
		}
		total += *definition.MaxUses
	}
	if unbounded {
		return uint(common.MaxTextToolCallCount), configured, nil
	}
	return total, configured, nil
}

func GetAndValidateTextRequest(c *gin.Context, relayMode int) (*dto.GeneralOpenAIRequest, error) {
	textRequest := &dto.GeneralOpenAIRequest{}
	err := common.UnmarshalBodyReusable(c, textRequest)
	if err != nil {
		return nil, err
	}

	if relayMode == relayconstant.RelayModeModerations && textRequest.Model == "" {
		textRequest.Model = "omni-moderation-latest"
	}
	if relayMode == relayconstant.RelayModeEmbeddings && textRequest.Model == "" {
		textRequest.Model = c.Param("model")
	}

	if err := ValidateTextRequest(textRequest, relayMode); err != nil {
		return nil, err
	}
	return textRequest, nil
}

// ValidateTextRequest validates a materialized OpenAI-compatible request.
// It is intentionally reusable after parameter overrides so optional zero
// values stay distinguishable while unsafe billing multipliers are rejected.
func ValidateTextRequest(textRequest *dto.GeneralOpenAIRequest, relayMode int) error {
	if textRequest == nil {
		return errors.New("request is required")
	}
	if exceedsMaxTokensLimit(textRequest.MaxTokens, textRequest.MaxCompletionTokens) {
		return errors.New("max_tokens is invalid")
	}
	if len(textRequest.THINKING) > 0 && common.GetJsonType(textRequest.THINKING) == "object" {
		var thinking dto.Thinking
		if err := common.Unmarshal(textRequest.THINKING, &thinking); err != nil {
			return fmt.Errorf("thinking is invalid: %w", err)
		}
		if err := validateOptionalTokenBudget(thinking.BudgetTokens, "thinking.budget_tokens"); err != nil {
			return err
		}
	}
	if len(textRequest.Reasoning) > 0 && common.GetJsonType(textRequest.Reasoning) == "object" {
		var reasoning struct {
			MaxTokens *int `json:"max_tokens,omitempty"`
		}
		if err := common.Unmarshal(textRequest.Reasoning, &reasoning); err != nil {
			return fmt.Errorf("reasoning is invalid: %w", err)
		}
		if err := validateOptionalTokenBudget(reasoning.MaxTokens, "reasoning.max_tokens"); err != nil {
			return err
		}
	}
	if textRequest.N != nil && (*textRequest.N < 1 || *textRequest.N > dto.MaxChatCompletionsN) {
		return fmt.Errorf("n must be an integer between 1 and %d", dto.MaxChatCompletionsN)
	}
	if textRequest.N != nil &&
		(exceedsMaxTokenProductLimit(textRequest.MaxTokens, *textRequest.N) ||
			exceedsMaxTokenProductLimit(textRequest.MaxCompletionTokens, *textRequest.N)) {
		return fmt.Errorf("max_tokens multiplied by n must not exceed %d", maxTokensLimit)
	}
	if textRequest.TopLogProbs != nil {
		if *textRequest.TopLogProbs < 0 || *textRequest.TopLogProbs > 20 {
			return errors.New("top_logprobs must be an integer between 0 and 20")
		}
		if textRequest.LogProbs == nil || !*textRequest.LogProbs {
			return errors.New("logprobs must be true when top_logprobs is set")
		}
	}
	if textRequest.Stop != nil {
		switch stop := textRequest.Stop.(type) {
		case string:
		case []string:
			if len(stop) > 4 {
				return errors.New("stop must contain at most 4 sequences")
			}
		case []any:
			if len(stop) > 4 {
				return errors.New("stop must contain at most 4 sequences")
			}
			for index, item := range stop {
				if _, ok := item.(string); !ok {
					return fmt.Errorf("stop[%d] must be a string", index)
				}
			}
		default:
			return errors.New("stop must be a string or an array of strings")
		}
	}
	for index, tool := range textRequest.Tools {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		if tool.Function.Parameters == nil {
			continue
		}
		schema, err := common.Any2Type[map[string]any](tool.Function.Parameters)
		if err != nil || schema == nil {
			return fmt.Errorf("tools[%d].function.parameters must be a JSON object", index)
		}
		if schemaType, exists := schema["type"]; exists && schemaType != nil {
			schemaTypeString, ok := schemaType.(string)
			if !ok || schemaTypeString != "object" {
				return fmt.Errorf("tools[%d].function.parameters.type must be object", index)
			}
		}
	}
	if textRequest.Model == "" {
		return errors.New("model is required")
	}
	if textRequest.WebSearchOptions != nil {
		if textRequest.WebSearchOptions.SearchContextSize != "" {
			validSizes := map[string]bool{
				"high":   true,
				"medium": true,
				"low":    true,
			}
			if !validSizes[textRequest.WebSearchOptions.SearchContextSize] {
				return errors.New("invalid search_context_size, must be one of: high, medium, low")
			}
		}
	}
	switch relayMode {
	case relayconstant.RelayModeCompletions:
		if textRequest.Prompt == "" {
			return errors.New("field prompt is required")
		}
	case relayconstant.RelayModeChatCompletions:
		// For FIM (Fill-in-the-middle) requests with prefix/suffix, messages is optional
		// It will be filled by provider-specific adaptors if needed (e.g., SiliconFlow)。Or it is allowed by model vendor(s) (e.g., DeepSeek)
		if len(textRequest.Messages) == 0 && textRequest.Prefix == nil && textRequest.Suffix == nil {
			return errors.New("field messages is required")
		}
	case relayconstant.RelayModeEmbeddings:
	case relayconstant.RelayModeModerations:
		if textRequest.Input == nil || textRequest.Input == "" {
			return errors.New("field input is required")
		}
	case relayconstant.RelayModeEdits:
		if textRequest.Instruction == "" {
			return errors.New("field instruction is required")
		}
	}
	return nil
}

func GetAndValidateGeminiRequest(c *gin.Context) (*dto.GeminiChatRequest, error) {
	request := &dto.GeminiChatRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if err := ValidateGeminiRequest(request); err != nil {
		return nil, err
	}

	//if c.Query("alt") == "sse" {
	//	relayInfo.IsStream = true
	//}

	return request, nil
}

// ValidateGeminiRequest validates a native Gemini request after all outbound
// mutations. candidateCount is a completion-token multiplier and therefore
// shares the same bounded choice-count policy as OpenAI n.
func ValidateGeminiRequest(request *dto.GeminiChatRequest) error {
	if request == nil {
		return errors.New("request is required")
	}
	if len(request.Requests) != 0 {
		// Gemini batch embedding has its own DTO, endpoint, token aggregation, and
		// validator. Treating a generateContent payload as a batch would price only
		// the empty outer request while forwarding multiple billable requests.
		return errors.New("batch requests are not supported for generateContent")
	}
	if len(request.Contents) == 0 {
		return errors.New("contents is required")
	}
	if exceedsMaxTokensLimit(request.GenerationConfig.MaxOutputTokens) {
		return errors.New("maxOutputTokens is invalid")
	}
	if thinkingConfig := request.GenerationConfig.ThinkingConfig; thinkingConfig != nil &&
		thinkingConfig.ThinkingBudget != nil &&
		(*thinkingConfig.ThinkingBudget < -1 || *thinkingConfig.ThinkingBudget > dto.MaxGeminiThinkingBudget) {
		return fmt.Errorf("thinkingBudget must be between -1 and %d", dto.MaxGeminiThinkingBudget)
	}
	if candidateCount := request.GenerationConfig.CandidateCount; candidateCount != nil &&
		(*candidateCount < 1 || *candidateCount > dto.MaxChatCompletionsN) {
		return fmt.Errorf("candidateCount must be an integer between 1 and %d", dto.MaxChatCompletionsN)
	}
	if candidateCount := request.GenerationConfig.CandidateCount; candidateCount != nil &&
		exceedsMaxTokenProductLimit(request.GenerationConfig.MaxOutputTokens, *candidateCount) {
		return fmt.Errorf("maxOutputTokens multiplied by candidateCount must not exceed %d", maxTokensLimit)
	}
	if logprobs := request.GenerationConfig.Logprobs; logprobs != nil {
		if *logprobs < 0 || *logprobs > 20 {
			return errors.New("generationConfig.logprobs must be an integer between 0 and 20")
		}
		if responseLogprobs := request.GenerationConfig.ResponseLogprobs; responseLogprobs == nil || !*responseLogprobs {
			return errors.New("generationConfig.responseLogprobs must be true when logprobs is set")
		}
	}
	if request.ServiceTier != nil {
		serviceTier, ok := dto.NormalizeGeminiServiceTier(*request.ServiceTier)
		if !ok {
			return errors.New("serviceTier must be one of: unspecified, standard, flex, priority")
		}
		request.ServiceTier = common.GetPointer(serviceTier)
	}
	if len(request.Tools) > 0 {
		jsonType := common.GetJsonType(request.Tools)
		if jsonType != "array" && jsonType != "object" {
			return errors.New("tools must be an object or an array of objects")
		}
		var definitions []json.RawMessage
		if jsonType == "array" {
			if err := common.Unmarshal(request.Tools, &definitions); err != nil {
				return fmt.Errorf("tools must be an array of objects: %w", err)
			}
		} else {
			definitions = []json.RawMessage{request.Tools}
		}
		hasGoogleSearch := false
		hasGoogleMaps := false
		for index, definition := range definitions {
			if common.GetJsonType(definition) != "object" {
				return fmt.Errorf("tools[%d] must be an object", index)
			}
			var tool dto.GeminiChatTool
			if err := common.Unmarshal(definition, &tool); err != nil {
				return fmt.Errorf("tools[%d] must be an object: %w", index, err)
			}
			if tool.GoogleSearch != nil && tool.GoogleSearchRetrieval != nil {
				return fmt.Errorf("tools[%d] cannot enable both googleSearch and googleSearchRetrieval", index)
			}
			hasGoogleSearch = hasGoogleSearch || tool.GoogleSearch != nil || tool.GoogleSearchRetrieval != nil
			hasGoogleMaps = hasGoogleMaps || tool.GoogleMaps != nil
		}
		if hasGoogleSearch && hasGoogleMaps {
			// Legacy generateContent grounding metadata exposes one combined
			// webSearchQueries list. When Search and Maps are both enabled it
			// does not attribute each query to a priced tool, so the gateway
			// cannot settle the two distinct provider prices exactly.
			return errors.New("tools cannot combine Google Search and Google Maps in one generateContent request")
		}
	}
	return nil
}

func GetAndValidateGeminiEmbeddingRequest(c *gin.Context) (*dto.GeminiEmbeddingRequest, error) {
	request := &dto.GeminiEmbeddingRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if err := validateGeminiEmbeddingRequest(request, "request"); err != nil {
		return nil, err
	}
	return request, nil
}

func GetAndValidateGeminiBatchEmbeddingRequest(c *gin.Context) (*dto.GeminiBatchEmbeddingRequest, error) {
	request := &dto.GeminiBatchEmbeddingRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if len(request.Requests) == 0 {
		return nil, errors.New("requests is required")
	}
	for index, embeddingRequest := range request.Requests {
		if err := validateGeminiEmbeddingRequest(embeddingRequest, fmt.Sprintf("requests[%d]", index)); err != nil {
			return nil, err
		}
	}
	return request, nil
}

func validateGeminiEmbeddingRequest(request *dto.GeminiEmbeddingRequest, field string) error {
	if request == nil {
		return fmt.Errorf("%s is required", field)
	}
	if len(request.Content.Parts) == 0 {
		return fmt.Errorf("%s.content.parts is required", field)
	}
	if dimensions := request.OutputDimensionality; dimensions != nil &&
		(*dimensions < 1 || *dimensions > dto.MaxGeminiEmbeddingDimensions) {
		return fmt.Errorf(
			"%s.outputDimensionality must be an integer between 1 and %d",
			field,
			dto.MaxGeminiEmbeddingDimensions,
		)
	}
	return nil
}
