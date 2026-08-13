// Package reasoning re-exports the pure model-name effort-suffix helpers,
// which moved to the conversion kit (service/relayconvert/reasoning) as part
// of the relaykit extraction. Host code keeps importing this path unchanged.
package reasoning

import kitreasoning "github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"

var (
	EffortSuffixes           = kitreasoning.EffortSuffixes
	OpenAIEffortSuffixes     = kitreasoning.OpenAIEffortSuffixes
	DeepSeekV4EffortSuffixes = kitreasoning.DeepSeekV4EffortSuffixes
)

var (
	TrimEffortSuffix                          = kitreasoning.TrimEffortSuffix
	TrimEffortSuffixWithSuffixes              = kitreasoning.TrimEffortSuffixWithSuffixes
	ParseOpenAIReasoningEffortFromModelSuffix = kitreasoning.ParseOpenAIReasoningEffortFromModelSuffix
	ParseClaudeEffortSuffix                   = kitreasoning.ParseClaudeEffortSuffix
	IsClaudeEffortLevel                       = kitreasoning.IsClaudeEffortLevel
	IsClaudeAdaptiveThinkingOnlyModel         = kitreasoning.IsClaudeAdaptiveThinkingOnlyModel
	IsClaudeSamplingRestrictedModel           = kitreasoning.IsClaudeSamplingRestrictedModel
	IsClaudeAlwaysThinkingModel               = kitreasoning.IsClaudeAlwaysThinkingModel
	IsClaudeOpus5Model                        = kitreasoning.IsClaudeOpus5Model
	ParseDeepSeekV4ThinkingSuffix             = kitreasoning.ParseDeepSeekV4ThinkingSuffix
	ResolveDeepSeekV4AliasOrSuffix            = kitreasoning.ResolveDeepSeekV4AliasOrSuffix
	NormalizeDeepSeekV4PricingModel           = kitreasoning.NormalizeDeepSeekV4PricingModel
)
