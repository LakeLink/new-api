package ratio_setting

import "strings"

// XAILongContextThreshold is the prompt-token boundary at which current Grok
// text models charge their doubled long-context rates for the whole request.
const XAILongContextThreshold = 200_000

func IsXAILongContextModel(model string) bool {
	return strings.HasPrefix(model, "grok-4.5") ||
		strings.HasPrefix(model, "grok-4.3") ||
		strings.HasPrefix(model, "grok-4.20") ||
		model == "grok-latest" ||
		model == "grok-build-0.1" ||
		model == "grok-build-latest" ||
		strings.HasPrefix(model, "grok-code-fast")
}
