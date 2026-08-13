package reasoning

import (
	"strings"

	"github.com/samber/lo"
)

var EffortSuffixes = []string{"-max", "-xhigh", "-high", "-medium", "-low", "-minimal"}

var OpenAIEffortSuffixes = []string{"-high", "-minimal", "-low", "-medium", "-none", "-xhigh"}

var DeepSeekV4EffortSuffixes = []string{"-none", "-max", "-xhigh", "-high", "-medium", "-low"}

var claudeEffortSuffixes = []string{"-max", "-xhigh", "-high", "-medium", "-low"}

// TrimEffortSuffix -> modelName level(low) exists
func TrimEffortSuffix(modelName string) (string, string, bool) {
	return TrimEffortSuffixWithSuffixes(modelName, EffortSuffixes)
}

func TrimEffortSuffixWithSuffixes(modelName string, suffixes []string) (string, string, bool) {
	suffix, found := lo.Find(suffixes, func(s string) bool {
		return strings.HasSuffix(modelName, s)
	})
	if !found {
		return modelName, "", false
	}
	return strings.TrimSuffix(modelName, suffix), strings.TrimPrefix(suffix, "-"), true
}

func ParseOpenAIReasoningEffortFromModelSuffix(modelName string) (string, string) {
	baseModel, effort, ok := TrimEffortSuffixWithSuffixes(modelName, OpenAIEffortSuffixes)
	if !ok {
		return "", modelName
	}
	return effort, baseModel
}

// ParseClaudeEffortSuffix recognizes only effort values accepted by Claude's
// output_config. In particular, "minimal" is an OpenAI-only effort level and
// must not be forwarded to Anthropic.
func ParseClaudeEffortSuffix(modelName string) (baseModel string, effort string, ok bool) {
	return TrimEffortSuffixWithSuffixes(modelName, claudeEffortSuffixes)
}

func IsClaudeEffortLevel(effort string) bool {
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func normalizeClaudeCapabilityModel(modelName string) string {
	baseModel, _, ok := ParseClaudeEffortSuffix(modelName)
	if ok {
		modelName = baseModel
	}
	return strings.TrimSuffix(modelName, "-thinking")
}

// IsClaudeAdaptiveThinkingOnlyModel reports models that reject manual
// thinking budgets and require adaptive thinking when thinking is configured.
func IsClaudeAdaptiveThinkingOnlyModel(modelName string) bool {
	switch normalizeClaudeCapabilityModel(modelName) {
	case "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5",
		"claude-sonnet-5", "claude-fable-5", "claude-mythos-5":
		return true
	default:
		return false
	}
}

// IsClaudeSamplingRestrictedModel reports Claude models that reject
// non-default temperature, top_p, and top_k values.
func IsClaudeSamplingRestrictedModel(modelName string) bool {
	return IsClaudeAdaptiveThinkingOnlyModel(modelName)
}

// IsClaudeAlwaysThinkingModel reports models on which thinking cannot be
// disabled. Opus 5 and Sonnet 5 default to adaptive thinking but still allow
// callers to disable it within their documented constraints.
func IsClaudeAlwaysThinkingModel(modelName string) bool {
	modelName = normalizeClaudeCapabilityModel(modelName)
	return modelName == "claude-fable-5" || modelName == "claude-mythos-5"
}

func IsClaudeOpus5Model(modelName string) bool {
	return normalizeClaudeCapabilityModel(modelName) == "claude-opus-5"
}

func ParseDeepSeekV4ThinkingSuffix(modelName string) (baseModel string, thinkingType string, effort string, ok bool) {
	baseModel, suffix, ok := TrimEffortSuffixWithSuffixes(modelName, DeepSeekV4EffortSuffixes)
	if !ok || (baseModel != "deepseek-v4-flash" && baseModel != "deepseek-v4-pro") {
		return modelName, "", "", false
	}
	switch suffix {
	case "none":
		return baseModel, "disabled", "", true
	case "max", "xhigh":
		return baseModel, "enabled", "max", true
	case "high", "medium", "low":
		return baseModel, "enabled", "high", true
	default:
		return modelName, "", "", false
	}
}

func ResolveDeepSeekV4AliasOrSuffix(modelName string) (baseModel string, thinkingType string, effort string, ok bool) {
	switch modelName {
	case "deepseek-chat":
		return "deepseek-v4-flash", "disabled", "", true
	case "deepseek-reasoner":
		return "deepseek-v4-flash", "enabled", "", true
	default:
		return ParseDeepSeekV4ThinkingSuffix(modelName)
	}
}

func NormalizeDeepSeekV4PricingModel(modelName string) string {
	baseModel, _, _, ok := ParseDeepSeekV4ThinkingSuffix(modelName)
	if ok {
		return baseModel
	}
	return modelName
}
