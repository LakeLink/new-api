package ratio_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/reasoning"
)

// GeminiLongContextThreshold is the input-token boundary after which supported
// Gemini Pro models use premium long-context pricing.
const GeminiLongContextThreshold = 200000

// ClaudeFastPriceRatios is the normalized input/output ratio pair for fast mode.
type ClaudeFastPriceRatios struct {
	ModelRatio      float64
	CompletionRatio float64
}

// GeminiServiceTierPriceRatios describes first-party interactive tier pricing.
// CacheRatio is relative to the tier's input price, not a multiplier over the
// standard cache ratio.
type GeminiServiceTierPriceRatios struct {
	ModelMultiplier float64
	CacheRatio      float64
}

// GetGeminiServiceTierPriceRatios returns current first-party Flex/Priority
// pricing for models exposed through GenerateContent usage metadata.
func GetGeminiServiceTierPriceRatios(model, serviceTier string) (GeminiServiceTierPriceRatios, bool) {
	serviceTier = strings.ToLower(serviceTier)
	is36Flash := model == "gemini-3.6-flash" || model == "gemini-flash-latest"
	is35Flash := model == "gemini-3.5-flash"
	is35FlashLite := model == "gemini-3.5-flash-lite" || model == "gemini-flash-lite-latest"
	is31FlashLite := model == "gemini-3.1-flash-lite"
	is3Flash := model == "gemini-3-flash-preview"
	is31Pro := model == "gemini-3.1-pro-preview" || model == "gemini-3.1-pro-preview-customtools" || model == "gemini-pro-latest"
	is25FlashImage := model == "gemini-2.5-flash-image"
	is3ProImage := model == "gemini-3-pro-image"
	isImageModel := is25FlashImage || is3ProImage
	is25 := model == "gemini-2.5-pro" || model == "gemini-2.5-flash" ||
		model == "gemini-2.5-flash-lite" || is25FlashImage
	if !is36Flash && !is35Flash && !is35FlashLite && !is31FlashLite && !is3Flash && !is31Pro && !is25 && !isImageModel {
		return GeminiServiceTierPriceRatios{}, false
	}

	switch serviceTier {
	case "priority":
		// The Developer API does not offer Priority for Gemini 2.5 Flash
		// Image; Gemini 3 Pro Image does.
		if is25FlashImage {
			return GeminiServiceTierPriceRatios{}, false
		}
		cacheRatio := 0.1
		if is35FlashLite {
			cacheRatio = 0.05 / 0.54
		} else if is3ProImage {
			cacheRatio = 1
		}
		return GeminiServiceTierPriceRatios{ModelMultiplier: 1.8, CacheRatio: cacheRatio}, true
	case "flex":
		cacheRatio := 0.2
		switch {
		case is36Flash:
			cacheRatio = 0.1
		case is35Flash:
			cacheRatio = 0.08 / 0.75
		case is35FlashLite:
			cacheRatio = 0.02 / 0.15
		case is31FlashLite:
			cacheRatio = 0.1
		case isImageModel:
			cacheRatio = 1
		}
		return GeminiServiceTierPriceRatios{ModelMultiplier: 0.5, CacheRatio: cacheRatio}, true
	default:
		return GeminiServiceTierPriceRatios{}, false
	}
}

// GetGeminiImageOutputRatio returns the image-output price relative to the
// model's standard input-token price. Gemini reports image output as tokens in
// candidatesTokensDetails, separately from text and thinking output.
func GetGeminiImageOutputRatio(model string) (float64, bool) {
	switch model {
	case "gemini-2.5-flash-image":
		return 100, true // $30 image output / $0.30 input
	case "gemini-3.1-flash-image", "gemini-3.1-flash-lite-image":
		return 120, true // $60/$0.50 and $30/$0.25
	case "gemini-3-pro-image":
		return 60, true // $120 image output / $2 input
	default:
		return 0, false
	}
}

// GetGeminiMaxImageOutputTokens returns the largest documented per-image token
// count for the model's supported output resolutions. It is used only for
// conservative pre-consume; settlement uses provider-reported modality counts.
func GetGeminiMaxImageOutputTokens(model string) (int, bool) {
	switch model {
	case "gemini-2.5-flash-image":
		return 1290, true
	case "gemini-3.1-flash-image":
		return 2520, true
	case "gemini-3.1-flash-lite-image":
		return 1120, true
	case "gemini-3-pro-image":
		return 2000, true
	default:
		return 0, false
	}
}

// IsGeminiLongContextModel reports whether the model has Gemini's 200K tier.
func IsGeminiLongContextModel(model string) bool {
	return model == "gemini-2.5-pro" ||
		model == "gemini-2.5-computer-use-preview-10-2025" ||
		model == "gemini-3.1-pro-preview" ||
		model == "gemini-3.1-pro-preview-customtools" ||
		model == "gemini-pro-latest"
}

// GetClaudeFastPriceRatios returns current first-party fast-mode ratios.
func GetClaudeFastPriceRatios(model string) (ClaudeFastPriceRatios, bool) {
	if baseModel, _, ok := reasoning.ParseClaudeEffortSuffix(model); ok {
		model = baseModel
	}
	model = strings.TrimSuffix(model, "-thinking")
	switch model {
	case "claude-opus-5", "claude-opus-4-8":
		return ClaudeFastPriceRatios{
			ModelRatio:      5,
			CompletionRatio: 5,
		}, true
	default:
		return ClaudeFastPriceRatios{}, false
	}
}

// IsClaudeInferenceGeoPricingModel reports whether first-party US-only
// inference carries Anthropic's data-residency premium.
func IsClaudeInferenceGeoPricingModel(model string) bool {
	if baseModel, _, ok := reasoning.ParseClaudeEffortSuffix(model); ok {
		model = baseModel
	}
	model = strings.TrimSuffix(model, "-thinking")
	switch model {
	case "claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5",
		"claude-sonnet-4-6", "claude-sonnet-5", "claude-fable-5", "claude-mythos-5":
		return true
	default:
		return false
	}
}
