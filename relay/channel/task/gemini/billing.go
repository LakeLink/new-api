package gemini

import (
	"math"
	"strconv"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const MaxVeoSampleCount = 4

func parseVeoDurationValue(value any) (int, bool) {
	switch duration := value.(type) {
	case float64:
		if math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 1 ||
			duration > float64(relaycommon.MaxTaskDurationSeconds) || math.Trunc(duration) != duration {
			return 0, false
		}
		return int(duration), true
	case int:
		if duration < 1 || duration > relaycommon.MaxTaskDurationSeconds {
			return 0, false
		}
		return duration, true
	default:
		return 0, false
	}
}

func parseVeoSeedValue(value any) (uint64, bool) {
	const maxVeoSeed = uint64(^uint32(0))
	switch seed := value.(type) {
	case float64:
		if math.IsNaN(seed) || math.IsInf(seed, 0) || seed < 0 || seed > float64(maxVeoSeed) || math.Trunc(seed) != seed {
			return 0, false
		}
		return uint64(seed), true
	case int:
		if seed < 0 || uint64(seed) > maxVeoSeed {
			return 0, false
		}
		return uint64(seed), true
	case uint:
		if uint64(seed) > maxVeoSeed {
			return 0, false
		}
		return uint64(seed), true
	default:
		return 0, false
	}
}

// ParseVeoDurationSeconds extracts durationSeconds from metadata.
// Returns 8 (Veo default) when not specified or invalid.
func ParseVeoDurationSeconds(metadata map[string]any) int {
	if metadata == nil {
		return 8
	}
	v, ok := metadata["durationSeconds"]
	if !ok {
		return 8
	}
	if duration, valid := parseVeoDurationValue(v); valid {
		return duration
	}
	return 8
}

// ParseVeoSampleCount returns a provider-safe output count. Provider-specific
// validation may impose a smaller maximum, but no caller may turn metadata
// into more than Vertex Veo's global four-output limit.
func ParseVeoSampleCount(metadata map[string]any) int {
	if metadata == nil {
		return 1
	}
	if sampleCount, valid := parseVeoDurationValue(metadata["sampleCount"]); valid && sampleCount <= MaxVeoSampleCount {
		return sampleCount
	}
	return 1
}

// ParseVeoResolution extracts resolution from metadata.
// Returns "720p" when not specified.
func ParseVeoResolution(metadata map[string]any) string {
	if metadata == nil {
		return "720p"
	}
	v, ok := metadata["resolution"]
	if !ok {
		return "720p"
	}
	if s, ok := v.(string); ok && s != "" {
		return strings.ToLower(s)
	}
	return "720p"
}

// ResolveVeoDuration returns the effective duration in seconds.
// Priority: metadata["durationSeconds"] > stdDuration > stdSeconds > default (8).
// The result is capped because it is used as a billing multiplier and the
// metadata path bypasses standard request validation.
func ResolveVeoDuration(metadata map[string]any, stdDuration int, stdSeconds string) int {
	if metadata != nil {
		if _, exists := metadata["durationSeconds"]; exists {
			if d := ParseVeoDurationSeconds(metadata); d > 0 {
				return min(d, relaycommon.MaxTaskDurationSeconds)
			}
		}
	}
	if stdDuration > 0 {
		return min(stdDuration, relaycommon.MaxTaskDurationSeconds)
	}
	if s, err := strconv.Atoi(stdSeconds); err == nil && s > 0 {
		return min(s, relaycommon.MaxTaskDurationSeconds)
	}
	return 8
}

// ResolveVeoResolution returns the effective resolution string (lowercase).
// Priority: metadata["resolution"] > SizeToVeoResolution(stdSize) > default ("720p").
func ResolveVeoResolution(metadata map[string]any, stdSize string) string {
	if metadata != nil {
		if _, exists := metadata["resolution"]; exists {
			if r := ParseVeoResolution(metadata); r != "" {
				return r
			}
		}
	}
	if stdSize != "" {
		return SizeToVeoResolution(stdSize)
	}
	return "720p"
}

// SizeToVeoResolution converts a "WxH" size string to a Veo resolution label.
func SizeToVeoResolution(size string) string {
	parts := strings.SplitN(strings.ToLower(size), "x", 2)
	if len(parts) != 2 {
		return "720p"
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	maxDim := w
	if h > maxDim {
		maxDim = h
	}
	if maxDim >= 3840 {
		return "4k"
	}
	if maxDim >= 1920 {
		return "1080p"
	}
	return "720p"
}

// SizeToVeoAspectRatio converts a "WxH" size string to a Veo aspect ratio.
func SizeToVeoAspectRatio(size string) string {
	parts := strings.SplitN(strings.ToLower(size), "x", 2)
	if len(parts) != 2 {
		return "16:9"
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	if w <= 0 || h <= 0 {
		return "16:9"
	}
	if h > w {
		return "9:16"
	}
	return "16:9"
}

// VeoResolutionRatio returns the standard-tier price multiplier relative to
// each model's 720p rate.
func VeoResolutionRatio(modelName, resolution string) float64 {
	resolution = strings.ToLower(resolution)
	switch {
	case strings.Contains(modelName, "3.1-lite-generate"):
		if resolution == "1080p" {
			return 1.6 // $0.08 / $0.05
		}
	case strings.Contains(modelName, "3.1-fast-generate"):
		switch resolution {
		case "1080p":
			return 1.2 // $0.12 / $0.10
		case "4k":
			return 3 // $0.30 / $0.10
		}
	case strings.Contains(modelName, "3.1-generate"):
		if resolution == "4k" {
			return 1.5 // $0.60 / $0.40
		}
	}
	return 1.0
}

// VeoSupports4K reports model-level support for direct 4K output. The Gemini
// API preview standard/fast models and Vertex stable standard model support
// 4K; Vertex stable Fast and Lite models currently support up to 1080p.
func VeoSupports4K(modelName string) bool {
	modelName = strings.ToLower(modelName)
	if strings.Contains(modelName, "lite-generate") || strings.Contains(modelName, "fast-generate-001") {
		return false
	}
	return strings.Contains(modelName, "3.1-generate") || strings.Contains(modelName, "3.1-fast-generate-preview")
}
