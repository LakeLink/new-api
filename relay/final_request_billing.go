package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/minimax"
	"github.com/QuantumNous/new-api/relay/channel/siliconflow"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// refreshFinalRequestBilling validates and prices the exact request body that
// is about to leave the gateway. Initial validation and pre-consume happen
// before channel conversion and ParamOverride, so they cannot be the final
// billing boundary.
func refreshFinalRequestBilling(c *gin.Context, info *relaycommon.RelayInfo, format types.RelayFormat, jsonData []byte) ([]byte, error) {
	if info == nil {
		return nil, errors.New("final request billing context is unavailable")
	}
	info.FinalRequestEstimateReady = false

	materializedJSON, err := materializeFinalProviderToolCaps(format, jsonData)
	if err != nil {
		return nil, err
	}
	jsonData = materializedJSON

	var request dto.Request
	switch format {
	case types.RelayFormatOpenAI:
		materialized := &dto.GeneralOpenAIRequest{}
		if err := common.Unmarshal(jsonData, materialized); err != nil {
			return nil, fmt.Errorf("decode final OpenAI-compatible request: %w", err)
		}
		if err := helper.ValidateTextRequest(materialized, info.RelayMode); err != nil {
			return nil, fmt.Errorf("final OpenAI-compatible request is invalid: %w", err)
		}
		request = materialized
	case types.RelayFormatOpenAIResponses:
		materialized := &dto.OpenAIResponsesRequest{}
		if err := common.Unmarshal(jsonData, materialized); err != nil {
			return nil, fmt.Errorf("decode final Responses request: %w", err)
		}
		if err := helper.ValidateResponsesRequest(materialized); err != nil {
			return nil, fmt.Errorf("final Responses request is invalid: %w", err)
		}
		request = materialized
	case types.RelayFormatClaude:
		materialized := &dto.ClaudeRequest{}
		if err := common.Unmarshal(jsonData, materialized); err != nil {
			return nil, fmt.Errorf("decode final Claude request: %w", err)
		}
		if err := helper.ValidateClaudeRequest(materialized); err != nil {
			return nil, fmt.Errorf("final Claude request is invalid: %w", err)
		}
		limit, configured, err := helper.ClaudeWebSearchLimit(materialized.Tools)
		if err != nil {
			return nil, err
		}
		info.EffectiveClaudeWebSearchMaxUses = nil
		if configured {
			info.EffectiveClaudeWebSearchMaxUses = common.GetPointer(limit)
		}
		request = materialized
	case types.RelayFormatGemini:
		materialized := &dto.GeminiChatRequest{}
		if err := common.Unmarshal(jsonData, materialized); err != nil {
			return nil, fmt.Errorf("decode final Gemini request: %w", err)
		}
		if err := helper.ValidateGeminiRequest(materialized); err != nil {
			return nil, fmt.Errorf("final Gemini request is invalid: %w", err)
		}
		request = materialized
	case types.RelayFormatRerank:
		if err := refreshFinalCohereRerankBilling(c, info, jsonData); err != nil {
			return nil, err
		}
		return jsonData, nil
	case types.RelayFormatOpenAIImage:
		if _, err := refreshFinalOpenAIImageBilling(c, info, jsonData); err != nil {
			return nil, err
		}
		return jsonData, nil
	default:
		if err := refreshUnknownFinalBilling(c, info, jsonData); err != nil {
			return nil, err
		}
		return jsonData, nil
	}

	if err := refreshFinalModelReservation(c, info, format, request); err != nil {
		return nil, err
	}
	return jsonData, nil
}

func materializeFinalProviderToolCaps(format types.RelayFormat, jsonData []byte) ([]byte, error) {
	switch format {
	case types.RelayFormatOpenAIResponses:
		return materializeFinalResponsesToolCap(jsonData)
	case types.RelayFormatClaude:
		return materializeFinalClaudeWebSearchCaps(jsonData)
	default:
		return jsonData, nil
	}
}

func materializeFinalResponsesToolCap(jsonData []byte) ([]byte, error) {
	request := &dto.OpenAIResponsesRequest{}
	if err := common.Unmarshal(jsonData, request); err != nil {
		return nil, fmt.Errorf("decode final Responses request: %w", err)
	}
	if request.MaxToolCalls != nil && *request.MaxToolCalls > uint(common.MaxTextToolCallCount) {
		return nil, fmt.Errorf("max_tool_calls must not exceed %d", common.MaxTextToolCallCount)
	}

	allowedTools, toolsDisabled := responsesAllowedServerTools(request.ToolChoice)
	if toolsDisabled {
		return jsonData, nil
	}
	hasMeteredTool := false
	for _, tool := range request.GetToolsMap() {
		toolType := common.Interface2String(tool["type"])
		priceKey, webSearch := dto.ResponsesWebSearchPricingKey(toolType)
		if allowedTools != nil && !allowedTools[toolType] && !(webSearch && allowedTools[priceKey]) {
			continue
		}
		if webSearch || toolType == dto.BuildInToolFileSearch || toolType == dto.BuildInToolImageGeneration {
			hasMeteredTool = true
		}
	}
	if !hasMeteredTool || request.MaxToolCalls != nil {
		return jsonData, nil
	}

	var root map[string]json.RawMessage
	if err := common.Unmarshal(jsonData, &root); err != nil {
		return nil, fmt.Errorf("decode final Responses request: %w", err)
	}
	rawCap, err := common.Marshal(uint(common.MaxTextToolCallCount))
	if err != nil {
		return nil, fmt.Errorf("encode final Responses max_tool_calls: %w", err)
	}
	root["max_tool_calls"] = rawCap
	materialized, err := common.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode final Responses request: %w", err)
	}
	return materialized, nil
}

func materializeFinalClaudeWebSearchCaps(jsonData []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := common.Unmarshal(jsonData, &root); err != nil {
		return nil, fmt.Errorf("decode final Claude request: %w", err)
	}
	rawTools, exists := root["tools"]
	if !exists || len(rawTools) == 0 || strings.TrimSpace(string(rawTools)) == "null" {
		return jsonData, nil
	}
	var tools []map[string]json.RawMessage
	if err := common.Unmarshal(rawTools, &tools); err != nil {
		return nil, fmt.Errorf("Claude tools must be an array: %w", err)
	}

	explicitTotal := uint(0)
	omitted := make([]int, 0)
	for index, tool := range tools {
		var toolType string
		if rawType := tool["type"]; len(rawType) > 0 {
			if err := common.Unmarshal(rawType, &toolType); err != nil {
				return nil, fmt.Errorf("Claude tools[%d].type must be a string", index)
			}
		}
		if !strings.HasPrefix(toolType, "web_search") {
			continue
		}
		var name string
		if err := common.Unmarshal(tool["name"], &name); err != nil || name != "web_search" {
			return nil, fmt.Errorf("Claude tools[%d].name must be web_search for a web-search tool", index)
		}
		rawMaxUses, hasMaxUses := tool["max_uses"]
		if !hasMaxUses || strings.TrimSpace(string(rawMaxUses)) == "null" {
			omitted = append(omitted, index)
			continue
		}
		var maxUses uint
		if err := common.Unmarshal(rawMaxUses, &maxUses); err != nil || maxUses > uint(common.MaxTextToolCallCount)-explicitTotal {
			return nil, fmt.Errorf("Claude web search max_uses must not exceed %d per request", common.MaxTextToolCallCount)
		}
		explicitTotal += maxUses
	}
	if len(omitted) == 0 {
		return jsonData, nil
	}
	remaining := uint(common.MaxTextToolCallCount) - explicitTotal
	if remaining < uint(len(omitted)) {
		return nil, fmt.Errorf("Claude omitted web search max_uses cannot fit within request limit %d", common.MaxTextToolCallCount)
	}
	for position, toolIndex := range omitted {
		toolsLeft := uint(len(omitted) - position)
		maxUses := remaining / toolsLeft
		remaining -= maxUses
		rawMaxUses, err := common.Marshal(maxUses)
		if err != nil {
			return nil, fmt.Errorf("encode Claude web search max_uses: %w", err)
		}
		tools[toolIndex]["max_uses"] = rawMaxUses
	}
	rawTools, err := common.Marshal(tools)
	if err != nil {
		return nil, fmt.Errorf("encode final Claude tools: %w", err)
	}
	root["tools"] = rawTools
	materialized, err := common.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode final Claude request: %w", err)
	}
	return materialized, nil
}

func refreshFinalCohereRerankBilling(c *gin.Context, info *relaycommon.RelayInfo, jsonData []byte) error {
	request := &dto.RerankRequest{}
	if err := common.Unmarshal(jsonData, request); err != nil {
		return fmt.Errorf("decode final Cohere rerank request: %w", err)
	}
	if err := helper.ValidateRerankRequest(request, true); err != nil {
		return fmt.Errorf("final Cohere rerank request is invalid: %w", err)
	}
	if info.RerankerInfo == nil {
		info.RerankerInfo = &relaycommon.RerankerInfo{}
	}
	info.RerankerInfo.Documents = request.Documents
	info.RerankerInfo.ReturnDocuments = request.GetReturnDocuments()
	return refreshFinalModelReservation(c, info, types.RelayFormatRerank, request)
}

func refreshFinalOpenAIImageBilling(c *gin.Context, info *relaycommon.RelayInfo, jsonData []byte) (*dto.ImageRequest, error) {
	if info != nil {
		info.FinalRequestEstimateReady = false
	}
	request := &dto.ImageRequest{}
	if err := common.Unmarshal(jsonData, request); err != nil {
		return nil, fmt.Errorf("decode final image request: %w", err)
	}
	relayMode := relayconstant.RelayModeImagesGenerations
	if info != nil {
		relayMode = info.RelayMode
	}
	if err := helper.ValidateOpenAIImageRequest(request, relayMode, relayMode == relayconstant.RelayModeImagesEdits); err != nil {
		return nil, fmt.Errorf("final image request is invalid: %w", err)
	}
	if (info != nil && (info.ApiType == constant.APITypeXai || info.ChannelType == constant.ChannelTypeXai)) ||
		dto.IsXAIImageModel(request.Model) {
		if err := helper.ValidateXAIImageRequest(request, relayMode); err != nil {
			return nil, fmt.Errorf("final xAI image request is invalid: %w", err)
		}
	}
	if err := refreshFinalModelReservation(c, info, types.RelayFormatOpenAIImage, request); err != nil {
		return nil, err
	}
	return request, nil
}

func refreshFinalModelReservation(c *gin.Context, info *relaycommon.RelayInfo, format types.RelayFormat, request dto.Request) error {
	if info == nil || request == nil {
		return errors.New("final request billing context is unavailable")
	}
	meta := request.GetTokenCountMeta()
	if meta == nil {
		return errors.New("final request token metadata is unavailable")
	}

	originalFormat := info.RelayFormat
	info.RelayFormat = format
	promptTokens, err := service.EstimateRequestToken(c, meta, info)
	info.RelayFormat = originalFormat
	if err != nil {
		return fmt.Errorf("estimate final request tokens: %w", err)
	}

	priceData, err := helper.RefreshModelPriceForFinalRequest(c, info, request, promptTokens, meta)
	if err != nil {
		return fmt.Errorf("price final outbound request: %w", err)
	}
	info.SetEstimatePromptTokens(promptTokens)

	toolQuota, err := finalServerToolReservationQuota(info, request)
	if err != nil {
		return err
	}
	if toolQuota > info.MaxServerToolReservationQuota {
		info.MaxServerToolReservationQuota = toolQuota
	}
	target, clamp := common.QuotaFromDecimalChecked(
		decimal.NewFromInt(int64(priceData.QuotaToPreConsume)).Add(decimal.NewFromInt(int64(toolQuota))),
	)
	if clamp != nil {
		if info.QuotaClamp == nil {
			info.QuotaClamp = clamp
		}
		return clamp
	}
	if target < 0 {
		return fmt.Errorf("final request reservation cannot be negative: %d", target)
	}
	if err := reserveFinalBillingTarget(c, info, target); err != nil {
		return err
	}
	info.PriceData = priceData
	info.FinalRequestEstimateReady = true
	return nil
}

func reserveFinalBillingTarget(c *gin.Context, info *relaycommon.RelayInfo, target int) error {
	if target <= 0 {
		return nil
	}
	if info.Billing != nil {
		if err := info.Billing.Reserve(target); err != nil {
			return err
		}
		return nil
	}
	if apiErr := service.PreConsumeBilling(c, target, info); apiErr != nil {
		return apiErr
	}
	return nil
}

func finalServerToolReservationQuota(info *relaycommon.RelayInfo, request dto.Request) (int, error) {
	if info == nil || request == nil {
		return 0, nil
	}
	groupRatio := info.PriceData.GroupRatioInfo.GroupRatio
	if groupRatio < 0 || math.IsNaN(groupRatio) || math.IsInf(groupRatio, 0) {
		return 0, errors.New("server-tool reservation has an invalid group ratio")
	}
	if groupRatio == 0 {
		return 0, nil
	}

	callPricePer1K := 0.0
	callLimit := uint(0)
	switch materialized := request.(type) {
	case *dto.OpenAIResponsesRequest:
		callLimit = uint(common.MaxTextToolCallCount)
		if materialized.MaxToolCalls != nil {
			callLimit = *materialized.MaxToolCalls
		}
		allowedTools, toolsDisabled := responsesAllowedServerTools(materialized.ToolChoice)
		if toolsDisabled {
			callLimit = 0
		}
		for _, tool := range materialized.GetToolsMap() {
			toolType := common.Interface2String(tool["type"])
			priceKey, webSearch := dto.ResponsesWebSearchPricingKey(toolType)
			if allowedTools != nil && !allowedTools[toolType] && !(webSearch && allowedTools[priceKey]) {
				continue
			}
			switch {
			case webSearch:
				callPricePer1K = math.Max(callPricePer1K, operation_setting.GetToolPriceForModel(priceKey, info.OriginModelName))
			case toolType == dto.BuildInToolFileSearch:
				callPricePer1K = math.Max(callPricePer1K, operation_setting.GetToolPriceForModel(dto.BuildInToolFileSearch, info.OriginModelName))
			case toolType == dto.BuildInToolImageGeneration:
				imageModel := common.Interface2String(tool["model"])
				if imageModel == "" {
					imageModel = "gpt-image-1"
				}
				quality := common.Interface2String(tool["quality"])
				if quality == "" {
					quality = "auto"
				}
				size := common.Interface2String(tool["size"])
				if size == "" {
					size = "auto"
				}
				imagePrice, ok := dto.OpenAIImageOutputCostUSD(imageModel, quality, size)
				if !ok {
					return 0, fmt.Errorf("server-tool reservation has unsupported image pricing for model %s", imageModel)
				}
				partialImages := 0
				if value, exists := tool["partial_images"].(float64); exists {
					partialImages = int(value)
				}
				partialPrice, ok := dto.OpenAIImagePartialOutputCostUSD(imageModel, partialImages)
				if !ok {
					return 0, fmt.Errorf("server-tool reservation has invalid partial image pricing for model %s", imageModel)
				}
				callPricePer1K = math.Max(callPricePer1K, (imagePrice+partialPrice)*1000)
			}
		}
	case *dto.ClaudeRequest:
		limit, configured, err := helper.ClaudeWebSearchLimit(materialized.Tools)
		if err != nil {
			return 0, err
		}
		if configured {
			callLimit = limit
			callPricePer1K = operation_setting.GetToolPrice("web_search")
		}
	case *dto.GeminiChatRequest:
		toolNames, err := geminiGroundingReservationTools(materialized)
		if err != nil {
			return 0, err
		}
		for _, toolName := range toolNames {
			callPricePer1K = math.Max(callPricePer1K, operation_setting.GetToolPriceForModel(toolName, info.OriginModelName))
		}
		if callPricePer1K > 0 {
			callLimit = 1
		}
	case *dto.GeneralOpenAIRequest:
		if strings.HasSuffix(info.OriginModelName, "search-preview") {
			callLimit = 1
			callPricePer1K = operation_setting.GetToolPriceForModel("web_search_preview", info.OriginModelName)
		}
	}

	quota := decimal.Zero
	if callLimit > 0 && callPricePer1K > 0 {
		quota = decimal.NewFromFloat(callPricePer1K).
			Mul(decimal.NewFromInt(int64(callLimit))).
			Div(decimal.NewFromInt(1000)).
			Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())).
			Mul(decimal.NewFromFloat(groupRatio))
	}
	withRatios := info.PriceData.ApplyOtherRatiosToDecimal(quota)
	if withRatios.GreaterThan(quota) {
		quota = withRatios
	}
	result, clamp := common.QuotaFromDecimalChecked(quota)
	if clamp != nil {
		if info.QuotaClamp == nil {
			info.QuotaClamp = clamp
		}
		return 0, clamp
	}
	return result, nil
}

func responsesAllowedServerTools(rawChoice json.RawMessage) (map[string]bool, bool) {
	if len(rawChoice) == 0 {
		return nil, false
	}
	var choice string
	if common.Unmarshal(rawChoice, &choice) == nil {
		return nil, strings.EqualFold(choice, "none")
	}
	var choiceObject struct {
		Type  string `json:"type"`
		Tools []struct {
			Type string `json:"type"`
		} `json:"tools,omitempty"`
	}
	if common.Unmarshal(rawChoice, &choiceObject) != nil {
		return nil, false
	}
	switch strings.ToLower(choiceObject.Type) {
	case "none":
		return nil, true
	case "allowed_tools":
		allowed := make(map[string]bool, len(choiceObject.Tools))
		for _, tool := range choiceObject.Tools {
			allowed[tool.Type] = true
		}
		return allowed, len(allowed) == 0
	case dto.BuildInToolWebSearch, dto.BuildInToolWebSearch20250826,
		dto.BuildInToolWebSearchPreview, dto.BuildInToolWebSearchPreview20250311,
		dto.BuildInToolFileSearch, dto.BuildInToolImageGeneration:
		return map[string]bool{choiceObject.Type: true}, false
	default:
		if choiceObject.Type != "" && !strings.EqualFold(choiceObject.Type, "auto") && !strings.EqualFold(choiceObject.Type, "required") {
			return map[string]bool{choiceObject.Type: true}, false
		}
		return nil, false
	}
}

func geminiGroundingReservationTools(request *dto.GeminiChatRequest) ([]string, error) {
	if request == nil || len(request.Tools) == 0 {
		return nil, nil
	}
	tools := request.GetTools()
	if tools == nil {
		return nil, errors.New("decode final Gemini tools")
	}
	configured := make(map[string]bool)
	for _, tool := range tools {
		if tool.GoogleSearch != nil || tool.GoogleSearchRetrieval != nil {
			configured["google_search"] = true
		}
		if tool.GoogleMaps != nil {
			configured["google_maps"] = true
		}
	}
	result := make([]string, 0, len(configured))
	for _, toolName := range []string{"google_search", "google_maps"} {
		if configured[toolName] {
			result = append(result, toolName)
		}
	}
	return result, nil
}

type finalBillableScalars struct {
	maxTokens            uint
	hasMaxTokens         bool
	count                uint
	hasCount             bool
	aliCount             uint
	hasAliCount          bool
	siliconFlowBatchSize uint
	hasSiliconFlowBatch  bool
	replicateOutputCount uint
	hasReplicateOutputs  bool
	imagenCount          uint
	hasImagenCount       bool
	finalModel           string
}

func parseFinalBillableScalars(info *relaycommon.RelayInfo, jsonData []byte) (finalBillableScalars, error) {
	var result finalBillableScalars
	var root map[string]json.RawMessage
	if err := common.Unmarshal(jsonData, &root); err != nil {
		return result, fmt.Errorf("decode final request: %w", err)
	}
	if rawModel, exists := root["model"]; exists && strings.TrimSpace(string(rawModel)) != "null" {
		_ = common.Unmarshal(rawModel, &result.finalModel)
	}
	for _, field := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		value, exists, err := parseFinalBoundedUint(root[field], field, 0, common.MaxTokensLimit)
		if err != nil {
			return result, err
		}
		if exists && (!result.hasMaxTokens || value > result.maxTokens) {
			result.maxTokens = value
			result.hasMaxTokens = true
		}
	}
	count, exists, err := parseFinalBoundedUint(root["n"], "n", 1, dto.MaxChatCompletionsN)
	if err != nil {
		return result, err
	}
	if exists {
		result.count = count
		result.hasCount = true
	}

	apiType := 0
	if info != nil && info.ChannelMeta != nil {
		apiType = info.ApiType
	}
	switch apiType {
	case constant.APITypeSiliconFlow:
		batchSize, exists, err := parseFinalBoundedUint(root["batch_size"], "batch_size", 1, dto.MaxSiliconFlowImageBatchSize)
		if err != nil {
			return result, err
		}
		result.siliconFlowBatchSize = batchSize
		result.hasSiliconFlowBatch = exists
	case constant.APITypeReplicate:
		if rawInput, exists := root["input"]; exists && strings.TrimSpace(string(rawInput)) != "null" {
			var input map[string]json.RawMessage
			if err := common.Unmarshal(rawInput, &input); err != nil {
				return result, fmt.Errorf("final Replicate input must be an object: %w", err)
			}
			value, exists, err := parseFinalBoundedUint(input["num_outputs"], "input.num_outputs", 1, dto.MaxImageN)
			if err != nil {
				return result, err
			}
			result.replicateOutputCount = value
			result.hasReplicateOutputs = exists
		}
	}

	parseAliParameters := apiType == constant.APITypeAli
	parseImagenParameters := isFinalImagenRequest(info)
	if parseAliParameters || parseImagenParameters {
		if rawParameters, exists := root["parameters"]; exists && strings.TrimSpace(string(rawParameters)) != "null" {
			var parameters map[string]json.RawMessage
			if err := common.Unmarshal(rawParameters, &parameters); err != nil {
				return result, fmt.Errorf("final parameters must be an object: %w", err)
			}
			if parseAliParameters {
				value, exists, err := parseFinalBoundedUint(parameters["n"], "parameters.n", 1, dto.MaxImageN)
				if err != nil {
					return result, err
				}
				result.aliCount = value
				result.hasAliCount = exists
			}
			if parseImagenParameters {
				value, exists, err := parseFinalBoundedUint(parameters["sampleCount"], "parameters.sampleCount", 1, relaycommon.MaxImagenImageCount)
				if err != nil {
					return result, err
				}
				result.imagenCount = value
				result.hasImagenCount = exists
			}
		}
	}
	return result, nil
}

func parseFinalBoundedUint(rawValue json.RawMessage, field string, minimum, maximum uint) (uint, bool, error) {
	if len(rawValue) == 0 || strings.TrimSpace(string(rawValue)) == "null" {
		return 0, false, nil
	}
	var value uint
	if err := common.Unmarshal(rawValue, &value); err != nil || value < minimum || value > maximum {
		return 0, false, fmt.Errorf("final %s must be an integer between %d and %d", field, minimum, maximum)
	}
	return value, true, nil
}

func refreshUnknownFinalBilling(c *gin.Context, info *relaycommon.RelayInfo, jsonData []byte) error {
	if info != nil {
		info.FinalRequestEstimateReady = false
	}
	scalars, err := parseFinalBillableScalars(info, jsonData)
	if err != nil {
		return err
	}
	if info == nil {
		return nil
	}
	if info.ChannelMeta != nil && info.ApiType == constant.APITypeOllama && scalars.hasCount && scalars.count != 1 {
		return errors.New("final Ollama native request supports exactly one choice")
	}
	if info.RelayFormat == types.RelayFormatOpenAIImage {
		return refreshUnknownFinalImageBilling(c, info, scalars)
	}
	return nil
}

func refreshUnknownFinalImageBilling(c *gin.Context, info *relaycommon.RelayInfo, scalars finalBillableScalars) error {
	if info == nil || isFinalImagenRequest(info) {
		return nil
	}
	if info.ApiType == constant.APITypeSiliconFlow && scalars.hasSiliconFlowBatch {
		batchSize := scalars.siliconFlowBatchSize
		if err := siliconflow.ValidateImageBatchSize(info.UpstreamModelName, &batchSize); err != nil {
			return err
		}
	}
	imageCount, hasImageCount := scalars.imageCountFor(info)
	if !hasImageCount {
		imageCount = 1
	}
	if info.ChannelMeta != nil && info.ApiType == constant.APITypeMiniMax && imageCount > minimax.MaxMiniMaxImageN {
		return fmt.Errorf("final MiniMax image n must be an integer between 1 and %d", minimax.MaxMiniMaxImageN)
	}

	priceData := info.PriceData
	priceData.ReplaceOtherRatios(info.PriceData.OtherRatios())
	priceData.RemoveOtherRatio("n")
	priceData.AddOtherRatio("n", float64(imageCount))

	targetDecimal := decimal.NewFromInt(int64(info.PriceData.QuotaToPreConsume)).Mul(decimal.NewFromInt(int64(imageCount)))
	if priceData.UsePrice {
		targetDecimal = decimal.NewFromFloat(priceData.ModelPrice).
			Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())).
			Mul(decimal.NewFromFloat(priceData.GroupRatioInfo.GroupRatio))
		targetDecimal = priceData.ApplyOtherRatiosToDecimal(targetDecimal)
	}
	target, clamp := common.QuotaFromDecimalChecked(targetDecimal)
	if clamp != nil {
		if info.QuotaClamp == nil {
			info.QuotaClamp = clamp
		}
		return clamp
	}
	if err := reserveFinalBillingTarget(c, info, target); err != nil {
		return err
	}
	priceData.QuotaToPreConsume = target
	info.PriceData = priceData
	return nil
}

func (s finalBillableScalars) imageCountFor(info *relaycommon.RelayInfo) (uint, bool) {
	if info != nil {
		switch info.ApiType {
		case constant.APITypeAli:
			return s.aliCount, s.hasAliCount
		case constant.APITypeSiliconFlow:
			return s.siliconFlowBatchSize, s.hasSiliconFlowBatch
		case constant.APITypeReplicate:
			return s.replicateOutputCount, s.hasReplicateOutputs
		case constant.APITypeGemini, constant.APITypeVertexAi:
			if isFinalImagenRequest(info) {
				return s.imagenCount, s.hasImagenCount
			}
		}
	}
	return s.count, s.hasCount
}

func finalRequestBillingAPIError(err error, paramOverride bool) *types.NewAPIError {
	if err == nil {
		return nil
	}
	var apiErr *types.NewAPIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	errorCode := types.ErrorCodeConvertRequestFailed
	if paramOverride {
		errorCode = types.ErrorCodeChannelParamOverrideInvalid
	}
	return types.NewErrorWithStatusCode(err, errorCode, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}
