package operation_setting

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/setting/config"
)

// ---------------------------------------------------------------------------
// Tool call prices ($/1K calls, admin-configurable)
// DB key: tool_price_setting.prices
//
// Key format:
//   - "tool_name"              → default price for all models
//   - "tool_name:model_prefix*" → override for models matching the prefix
//
// Lookup order: longest prefix match → default → hardcoded fallback → 0
// ---------------------------------------------------------------------------

var defaultToolPrices = map[string]float64{
	"web_search":         10.0, // OpenAI web search (all models) / Claude web search
	"web_search_preview": 10.0, // OpenAI web search preview (default: reasoning models)
	"file_search":        2.5,  // OpenAI file search (Responses API)
	"google_search":      14.0, // Gemini Grounding with Google Search
	"google_maps":        14.0, // Gemini 3 Grounding with Google Maps
}

var defaultToolPriceOverrides = map[string]float64{
	"web_search_preview:gpt-4o*":       25.0, // non-reasoning models
	"web_search_preview:gpt-4.1*":      25.0,
	"web_search_preview:gpt-4o-mini*":  25.0,
	"web_search_preview:gpt-4.1-mini*": 25.0,
	"google_search:gemini-2.5*":        35.0, // Gemini 2.5 bills per grounded prompt
	"google_maps:gemini-2.5*":          25.0, // Gemini 2.5 bills per grounded prompt
}

// ToolPriceSetting is managed by config.GlobalConfig.Register.
type ToolPriceSetting struct {
	Prices map[string]float64 `json:"prices"`
}

func (s ToolPriceSetting) Validate() error {
	return ValidateToolPriceMap(s.Prices)
}

var toolPriceSetting = ToolPriceSetting{
	Prices: func() map[string]float64 {
		m := make(map[string]float64, len(defaultToolPrices)+len(defaultToolPriceOverrides))
		for k, v := range defaultToolPrices {
			m[k] = v
		}
		for k, v := range defaultToolPriceOverrides {
			m[k] = v
		}
		return m
	}(),
}

func init() {
	config.GlobalConfig.Register("tool_price_setting", &toolPriceSetting)
	RebuildToolPriceIndex()
}

// ---------------------------------------------------------------------------
// Precomputed price index (atomic, lock-free on read path)
// ---------------------------------------------------------------------------

type prefixEntry struct {
	prefix string
	price  float64
}

type toolPriceIndex struct {
	defaults map[string]float64
	prefixes map[string][]prefixEntry
}

var currentIndex atomic.Pointer[toolPriceIndex]

// RebuildToolPriceIndex rebuilds the lookup index from the current config.
// Called on init and after config updates. Not on the billing hot path.
func RebuildToolPriceIndex() {
	prices := config.Snapshot[ToolPriceSetting]("tool_price_setting").Prices
	merged := make(map[string]float64, len(defaultToolPrices)+len(defaultToolPriceOverrides)+len(prices))
	for k, v := range defaultToolPrices {
		merged[k] = v
	}
	for k, v := range defaultToolPriceOverrides {
		merged[k] = v
	}
	for k, v := range prices {
		if strings.TrimSpace(k) == "" || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		merged[k] = v
	}

	idx := &toolPriceIndex{
		defaults: make(map[string]float64),
		prefixes: make(map[string][]prefixEntry),
	}

	for key, price := range merged {
		colonIdx := strings.IndexByte(key, ':')
		if colonIdx < 0 {
			idx.defaults[key] = price
			continue
		}
		toolName := key[:colonIdx]
		modelPart := key[colonIdx+1:]
		prefix := strings.TrimSuffix(modelPart, "*")
		idx.prefixes[toolName] = append(idx.prefixes[toolName], prefixEntry{prefix: prefix, price: price})
	}

	for tool := range idx.prefixes {
		entries := idx.prefixes[tool]
		sort.Slice(entries, func(i, j int) bool {
			return len(entries[i].prefix) > len(entries[j].prefix)
		})
		idx.prefixes[tool] = entries
	}

	currentIndex.Store(idx)
}

// ValidateToolPriceMap rejects values that could turn a server-tool charge
// into a credit or poison quota arithmetic. Zero is allowed as an explicit
// administrator choice to disable billing for a tool.
func ValidateToolPriceMap(prices map[string]float64) error {
	if prices == nil {
		return fmt.Errorf("tool prices must be a JSON object")
	}
	for key, price := range prices {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("tool price key must not be empty")
		}
		if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return fmt.Errorf("tool price %q must be a finite non-negative number", key)
		}
	}
	return nil
}

// UpdateToolPrices validates and atomically publishes a replacement tool
// price index. The copied map prevents callers from mutating billing config
// after validation.
func UpdateToolPrices(prices map[string]float64) error {
	if err := ValidateToolPriceMap(prices); err != nil {
		return err
	}
	validated := make(map[string]float64, len(prices))
	for key, price := range prices {
		validated[key] = price
	}
	if err := config.Mutate(&toolPriceSetting, func() {
		toolPriceSetting.Prices = validated
	}); err != nil {
		return err
	}
	RebuildToolPriceIndex()
	return nil
}

// GetToolPriceForModel returns the price ($/1K calls) for a tool given a model name.
// Lookup: longest prefix match → tool default → 0.
func GetToolPriceForModel(toolName, modelName string) float64 {
	idx := currentIndex.Load()
	if idx == nil {
		if v, ok := defaultToolPrices[toolName]; ok {
			return v
		}
		return 0
	}

	if entries, ok := idx.prefixes[toolName]; ok && modelName != "" {
		for _, e := range entries {
			if strings.HasPrefix(modelName, e.prefix) {
				return e.price
			}
		}
	}

	if p, ok := idx.defaults[toolName]; ok {
		return p
	}
	return 0
}

// GetToolPrice is a convenience wrapper when no model name is needed.
func GetToolPrice(toolName string) float64 {
	return GetToolPriceForModel(toolName, "")
}

// ---------------------------------------------------------------------------
// GPT Image 1 per-call pricing (special: depends on quality + size)
// ---------------------------------------------------------------------------

const (
	GPTImage1Low1024x1024    = 0.011
	GPTImage1Low1024x1536    = 0.016
	GPTImage1Low1536x1024    = 0.016
	GPTImage1Medium1024x1024 = 0.042
	GPTImage1Medium1024x1536 = 0.063
	GPTImage1Medium1536x1024 = 0.063
	GPTImage1High1024x1024   = 0.167
	GPTImage1High1024x1536   = 0.25
	GPTImage1High1536x1024   = 0.25
)

func GetGPTImage1PriceOnceCall(quality string, size string) float64 {
	prices := map[string]map[string]float64{
		"low": {
			"1024x1024": GPTImage1Low1024x1024,
			"1024x1536": GPTImage1Low1024x1536,
			"1536x1024": GPTImage1Low1536x1024,
		},
		"medium": {
			"1024x1024": GPTImage1Medium1024x1024,
			"1024x1536": GPTImage1Medium1024x1536,
			"1536x1024": GPTImage1Medium1536x1024,
		},
		"high": {
			"1024x1024": GPTImage1High1024x1024,
			"1024x1536": GPTImage1High1024x1536,
			"1536x1024": GPTImage1High1536x1024,
		},
	}

	if qualityMap, exists := prices[quality]; exists {
		if price, exists := qualityMap[size]; exists {
			return price
		}
	}

	return GPTImage1High1024x1024
}

// ---------------------------------------------------------------------------
// Gemini audio input pricing (per-million tokens, model-specific)
// ---------------------------------------------------------------------------

const (
	Gemini25FlashPreviewInputAudioPrice     = 1.00
	Gemini25FlashProductionInputAudioPrice  = 1.00
	Gemini25FlashLitePreviewInputAudioPrice = 0.50
	Gemini25FlashNativeAudioInputAudioPrice = 3.00
	Gemini20FlashInputAudioPrice            = 0.70
	GeminiRoboticsER15InputAudioPrice       = 1.00
	Gemini31FlashLiteInputAudioPrice        = 0.50
	Gemini3FlashPreviewInputAudioPrice      = 1.00
	GeminiRoboticsER16InputAudioPrice       = 2.00
)

func GetGeminiInputAudioPricePerMillionTokens(modelName string) float64 {
	if strings.HasPrefix(modelName, "gemini-2.5-flash-native-audio") ||
		strings.HasPrefix(modelName, "gemini-2.5-flash-preview-native-audio") {
		return Gemini25FlashNativeAudioInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.5-flash-lite") {
		return 0.30
	} else if strings.HasPrefix(modelName, "gemini-2.5-flash-preview-lite") {
		return Gemini25FlashLitePreviewInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.5-flash-preview") {
		return Gemini25FlashPreviewInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.5-flash") {
		return Gemini25FlashProductionInputAudioPrice
	} else if modelName == "gemini-3.1-flash-lite" || modelName == "gemini-flash-lite-latest" {
		return Gemini31FlashLiteInputAudioPrice
	} else if modelName == "gemini-3-flash-preview" {
		return Gemini3FlashPreviewInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.0-flash") {
		return Gemini20FlashInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-robotics-er-1.5") {
		return GeminiRoboticsER15InputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-robotics-er-1.6") {
		return GeminiRoboticsER16InputAudioPrice
	}
	return 0
}

// GetGeminiInputAudioPriceForServiceTier returns the actual interactive-tier
// audio price. Only models with first-party Flex/Priority audio rows are
// adjusted; models such as Robotics and Live remain on their documented rate.
func GetGeminiInputAudioPriceForServiceTier(modelName, serviceTier string) float64 {
	price := GetGeminiInputAudioPricePerMillionTokens(modelName)
	if price == 0 {
		return 0
	}

	supportsTierPricing := modelName == "gemini-2.5-flash" || modelName == "gemini-2.5-flash-lite" ||
		modelName == "gemini-3.1-flash-lite" || modelName == "gemini-flash-lite-latest" ||
		modelName == "gemini-3-flash-preview"
	if !supportsTierPricing {
		return price
	}
	switch strings.ToLower(serviceTier) {
	case "flex":
		return price * 0.5
	case "priority":
		return price * 1.8
	default:
		return price
	}
}
