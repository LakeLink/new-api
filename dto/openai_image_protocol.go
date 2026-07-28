package dto

import (
	"strconv"
	"strings"
)

const (
	// MaxOpenAIImageN is the OpenAI Images API limit for one request.
	MaxOpenAIImageN uint = 10
	// MaxOpenAIImageEditInputs is the GPT Image edit input limit.
	MaxOpenAIImageEditInputs = 16
	// MaxOpenAIImagePartialImages is the maximum number of streamed previews.
	MaxOpenAIImagePartialImages = 3
	// OpenAIImagePartialOutputTokens is charged for every emitted preview.
	OpenAIImagePartialOutputTokens = 100
)

const (
	// OpenAIImageLowFidelityInputTokens is the conservative maximum after the
	// 512px-short-side tiling rule: 65 base tokens plus four 129-token tiles.
	OpenAIImageLowFidelityInputTokens = 581
	// OpenAIImageHighFidelityInputTokens adds the maximum 6,240-token
	// non-square preservation surcharge.
	OpenAIImageHighFidelityInputTokens = 6821
)

const (
	OpenAIImageModelUnknown = iota
	OpenAIImageModelDallE2
	OpenAIImageModelDallE3
	OpenAIImageModelGPT1
	OpenAIImageModelGPT1Mini
	OpenAIImageModelGPT15
	OpenAIImageModelGPT2
)

// OpenAIImageModelKind identifies models whose protocol is defined by the
// OpenAI Images API. Unknown names stay compatible with custom providers.
func OpenAIImageModelKind(model string) int {
	switch {
	case model == "dall-e" || model == "dall-e-2":
		return OpenAIImageModelDallE2
	case model == "dall-e-3":
		return OpenAIImageModelDallE3
	case model == "gpt-image-1-mini" || strings.HasPrefix(model, "gpt-image-1-mini-"):
		return OpenAIImageModelGPT1Mini
	case model == "gpt-image-1.5" || strings.HasPrefix(model, "gpt-image-1.5-") ||
		model == "chatgpt-image-latest":
		return OpenAIImageModelGPT15
	case model == "gpt-image-2" || strings.HasPrefix(model, "gpt-image-2-"):
		return OpenAIImageModelGPT2
	case model == "gpt-image-1" || strings.HasPrefix(model, "gpt-image-1-"):
		return OpenAIImageModelGPT1
	default:
		return OpenAIImageModelUnknown
	}
}

func IsOpenAIGPTImageModel(model string) bool {
	return OpenAIImageModelKind(model) >= OpenAIImageModelGPT1
}

func IsOpenAIResponsesImageModel(model string) bool {
	switch model {
	case "gpt-image-1", "gpt-image-1-mini", "gpt-image-1.5", "gpt-image-2",
		"gpt-image-2-2026-04-21", "chatgpt-image-latest":
		return true
	default:
		return false
	}
}

// OpenAIImageInputTokens returns a conservative per-reference reservation for
// GPT Image edits. The provider reports actual input image tokens for final
// settlement; this estimate prevents an edit from bypassing pre-consume.
func OpenAIImageInputTokens(model, inputFidelity string) (int, bool) {
	switch OpenAIImageModelKind(model) {
	case OpenAIImageModelGPT2:
		return OpenAIImageHighFidelityInputTokens, true
	case OpenAIImageModelGPT1, OpenAIImageModelGPT15:
		if inputFidelity == "high" {
			return OpenAIImageHighFidelityInputTokens, true
		}
		return OpenAIImageLowFidelityInputTokens, true
	case OpenAIImageModelGPT1Mini:
		return OpenAIImageLowFidelityInputTokens, true
	default:
		return 0, false
	}
}

// ParseOpenAIImageSize parses an explicit WxH value. "auto" and an omitted
// size are intentionally not dimensions.
func ParseOpenAIImageSize(size string) (int, int, bool) {
	parts := strings.Split(size, "x")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, 0, false
	}
	width, err := strconv.Atoi(parts[0])
	if err != nil || width <= 0 {
		return 0, 0, false
	}
	height, err := strconv.Atoi(parts[1])
	if err != nil || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

// ValidOpenAIGPTImage2Size implements the arbitrary-resolution constraints for
// GPT Image 2. Standard GPT Image sizes and "auto" are also accepted.
func ValidOpenAIGPTImage2Size(size string) bool {
	switch size {
	case "", "auto", "1024x1024", "1536x1024", "1024x1536":
		return true
	}
	width, height, ok := ParseOpenAIImageSize(size)
	if !ok || width%16 != 0 || height%16 != 0 || width > 3840 || height > 3840 {
		return false
	}
	longEdge := max(width, height)
	shortEdge := min(width, height)
	pixels := int64(width) * int64(height)
	return longEdge <= 3*shortEdge && pixels >= 655_360 && pixels <= 8_294_400
}

// OpenAIImageOutputTokens returns the output-image token count used by OpenAI
// pricing. Empty/auto selections use a conservative maximum for reservation.
func OpenAIImageOutputTokens(model, quality, size string) (int, bool) {
	kind := OpenAIImageModelKind(model)
	if kind < OpenAIImageModelGPT1 {
		return 0, false
	}

	if quality == "" || quality == "auto" {
		quality = "high"
	}
	if size == "" || size == "auto" {
		if kind == OpenAIImageModelGPT2 {
			// 2880² is the largest allowed square (8,294,400 pixels), which
			// maximizes the GPT Image 2 output-token formula.
			size = "2880x2880"
		} else {
			size = "1024x1536"
		}
	}

	if kind == OpenAIImageModelGPT2 {
		width, height, ok := ParseOpenAIImageSize(size)
		if !ok || !ValidOpenAIGPTImage2Size(size) {
			return 0, false
		}
		qualityAxis := 0
		switch quality {
		case "low":
			qualityAxis = 16
		case "medium":
			qualityAxis = 48
		case "high":
			qualityAxis = 96
		default:
			return 0, false
		}
		longEdge := int64(max(width, height))
		shortEdge := int64(min(width, height))
		q := int64(qualityAxis)
		// Round half up q*short/long using integer arithmetic.
		shortAxis := (2*q*shortEdge + longEdge) / (2 * longEdge)
		numerator := q * shortAxis * (2_000_000 + int64(width)*int64(height))
		return int((numerator + 4_000_000 - 1) / 4_000_000), true
	}

	orientation := ""
	switch size {
	case "1024x1024":
		orientation = "square"
	case "1024x1536":
		orientation = "portrait"
	case "1536x1024":
		orientation = "landscape"
	default:
		return 0, false
	}
	if kind == OpenAIImageModelGPT1Mini {
		// GPT Image 1 Mini publishes a distinct per-image output price table.
		// Express those prices as output-token equivalents at its $8/M image
		// output rate so ratio billing and fixed-USD tool billing agree.
		tokens := map[string]map[string]int{
			"low": {
				"square": 625, "portrait": 750, "landscape": 750,
			},
			"medium": {
				"square": 1375, "portrait": 1875, "landscape": 1875,
			},
			"high": {
				"square": 4500, "portrait": 6500, "landscape": 6500,
			},
		}
		qualityTokens, ok := tokens[quality]
		if !ok {
			return 0, false
		}
		return qualityTokens[orientation], true
	}
	tokens := map[string]map[string]int{
		"low": {
			"square": 272, "portrait": 408, "landscape": 400,
		},
		"medium": {
			"square": 1056, "portrait": 1584, "landscape": 1568,
		},
		"high": {
			"square": 4160, "portrait": 6240, "landscape": 6208,
		},
	}
	qualityTokens, ok := tokens[quality]
	if !ok {
		return 0, false
	}
	return qualityTokens[orientation], true
}

// OpenAIImageOutputPricePerMillionTokens returns the current output-image
// token price in US dollars per million tokens.
func OpenAIImageOutputPricePerMillionTokens(model string) (float64, bool) {
	switch OpenAIImageModelKind(model) {
	case OpenAIImageModelGPT1:
		return 40, true
	case OpenAIImageModelGPT1Mini:
		return 8, true
	case OpenAIImageModelGPT15:
		return 32, true
	case OpenAIImageModelGPT2:
		return 30, true
	default:
		return 0, false
	}
}

// OpenAIImageOutputCostUSD returns the exact output-image token cost for one
// completed image.
func OpenAIImageOutputCostUSD(model, quality, size string) (float64, bool) {
	tokens, ok := OpenAIImageOutputTokens(model, quality, size)
	if !ok {
		return 0, false
	}
	pricePerMillion, ok := OpenAIImageOutputPricePerMillionTokens(model)
	if !ok {
		return 0, false
	}
	return float64(tokens) * pricePerMillion / 1_000_000, true
}

// OpenAIImagePartialOutputCostUSD returns the cost of streamed preview image
// output. The caller is responsible for applying the per-call configured cap.
func OpenAIImagePartialOutputCostUSD(model string, partialImages int) (float64, bool) {
	if partialImages < 0 {
		return 0, false
	}
	pricePerMillion, ok := OpenAIImageOutputPricePerMillionTokens(model)
	if !ok {
		return 0, false
	}
	return float64(partialImages) * OpenAIImagePartialOutputTokens * pricePerMillion / 1_000_000, true
}
