package vertex

import "strings"

// vertexVeoAudioRatio returns the silent-video price multiplier relative to
// the audio-enabled rate for the selected model and resolution.
func vertexVeoAudioRatio(modelName, resolution string, generateAudio bool) float64 {
	if generateAudio {
		return 1
	}

	modelName = strings.ToLower(modelName)
	resolution = strings.ToLower(resolution)
	switch {
	case strings.Contains(modelName, "3.1-lite-generate"):
		if resolution == "1080p" {
			return 0.625 // $0.05 / $0.08
		}
		return 0.6 // $0.03 / $0.05
	case strings.Contains(modelName, "3.1-fast-generate"):
		if resolution == "1080p" || resolution == "4k" {
			return 5.0 / 6.0 // $0.10/$0.12 or $0.25/$0.30
		}
		return 0.8 // $0.08 / $0.10
	case strings.Contains(modelName, "3.1-generate"):
		if resolution == "4k" {
			return 2.0 / 3.0 // $0.40 / $0.60
		}
		return 0.5 // $0.20 / $0.40
	default:
		return 1
	}
}
