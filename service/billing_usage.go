package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

const (
	usageBillingPathLocal              = "local"
	usageBillingPathUpstream           = "upstream"
	usageBillingPathOpenAI             = "billing-usage-openai"
	usageBillingPathOpenAIEstimated    = "billing-usage-openai-estimated"
	usageBillingPathAnthropic          = "billing-usage-anthropic"
	usageBillingPathAnthropicEstimated = "billing-usage-anthropic-estimated"
	usageBillingPathGemini             = "billing-usage-gemini"
	usageBillingPathGeminiEstimated    = "billing-usage-gemini-estimated"
)

// effectiveBillingUsage validates every upstream-controlled counter
// before converting provider-specific billing metadata. The conversion helpers
// intentionally use int fields to match dto.Usage, so validation must happen
// first to keep malformed negative or overflowing values out of quota math.
func effectiveBillingUsage(usage *dto.Usage) (*dto.Usage, error) {
	if err := validateBillingUsageCounters(usage); err != nil {
		return nil, err
	}
	if usage == nil {
		return nil, nil
	}
	effective := usage
	if billingUsage, ok := usageFromBillingUsage(usage); ok {
		effective = billingUsage
	}

	// Responses-style payloads use input_tokens/output_tokens while Chat
	// Completions uses prompt_tokens/completion_tokens. Normalize the aliases
	// before deciding whether the upstream supplied usable billing data and
	// before quota calculation. Work on a copy so billing never rewrites the
	// response object that may still be used for downstream serialization.
	normalized := *effective
	if normalized.PromptTokens == 0 && normalized.InputTokens > 0 {
		normalized.PromptTokens = normalized.InputTokens
	}
	if normalized.CompletionTokens == 0 && normalized.OutputTokens > 0 {
		normalized.CompletionTokens = normalized.OutputTokens
	}
	if normalized.TotalTokens == 0 {
		normalized.TotalTokens = normalized.PromptTokens + normalized.CompletionTokens
	}
	if normalized.InputTokensDetails != nil {
		inputDetails := normalized.InputTokensDetails
		if normalized.PromptTokensDetails.CachedTokens == 0 {
			normalized.PromptTokensDetails.CachedTokens = inputDetails.CachedTokens
		}
		if normalized.PromptTokensDetails.CachedCreationTokens == 0 {
			normalized.PromptTokensDetails.CachedCreationTokens = inputDetails.CachedCreationTokens
		}
		if normalized.PromptTokensDetails.CacheWriteTokens == 0 {
			normalized.PromptTokensDetails.CacheWriteTokens = inputDetails.CacheWriteTokens
		}
		if normalized.PromptTokensDetails.TextTokens == 0 {
			normalized.PromptTokensDetails.TextTokens = inputDetails.TextTokens
		}
		if normalized.PromptTokensDetails.AudioTokens == 0 {
			normalized.PromptTokensDetails.AudioTokens = inputDetails.AudioTokens
		}
		if normalized.PromptTokensDetails.ImageTokens == 0 {
			normalized.PromptTokensDetails.ImageTokens = inputDetails.ImageTokens
		}
	}
	if normalized.PromptTokensDetails.CachedTokens == 0 && normalized.PromptCacheHitTokens > 0 {
		normalized.PromptTokensDetails.CachedTokens = normalized.PromptCacheHitTokens
	}
	if err := validateNormalizedUsageCounters("effective_usage", &normalized); err != nil {
		return nil, err
	}
	if err := validateBillingUsageDetailConsistency(&normalized); err != nil {
		return nil, err
	}
	return &normalized, nil
}

func validateBillingUsageCounters(usage *dto.Usage) error {
	if usage == nil {
		return nil
	}
	if err := validateNormalizedUsageCounters("usage", usage); err != nil {
		return err
	}

	billingUsage := usage.BillingUsage
	if billingUsage == nil {
		return nil
	}
	source := strings.TrimSpace(billingUsage.Source)
	semantic := strings.TrimSpace(billingUsage.Semantic)

	if billingUsage.OpenAIUsage != nil &&
		(strings.EqualFold(source, dto.BillingUsageSourceOAIChat) ||
			strings.EqualFold(source, dto.BillingUsageSourceOAIResponses) ||
			strings.EqualFold(semantic, dto.BillingUsageSemanticOpenAI)) {
		return validateNormalizedUsageCounters("billing_usage.openai_usage", billingUsage.OpenAIUsage)
	}

	if billingUsage.ClaudeUsage != nil &&
		(strings.EqualFold(source, dto.BillingUsageSourceClaudeMessages) ||
			strings.EqualFold(semantic, dto.BillingUsageSemanticAnthropic)) {
		return validateClaudeUsageCounters(billingUsage.ClaudeUsage)
	}

	if billingUsage.GeminiUsageMetadata != nil &&
		(strings.EqualFold(source, dto.BillingUsageSourceGeminiChat) ||
			strings.EqualFold(semantic, dto.BillingUsageSemanticGemini)) {
		return validateGeminiUsageCounters(billingUsage.GeminiUsageMetadata)
	}

	return nil
}

func validateNormalizedUsageCounters(prefix string, usage *dto.Usage) error {
	if usage == nil {
		return nil
	}
	counters := []struct {
		name  string
		value int
	}{
		{"prompt_tokens", usage.PromptTokens},
		{"completion_tokens", usage.CompletionTokens},
		{"total_tokens", usage.TotalTokens},
		{"prompt_cache_hit_tokens", usage.PromptCacheHitTokens},
		{"input_tokens", usage.InputTokens},
		{"output_tokens", usage.OutputTokens},
		{"prompt_tokens_details.cached_tokens", usage.PromptTokensDetails.CachedTokens},
		{"prompt_tokens_details.cached_creation_tokens", usage.PromptTokensDetails.CachedCreationTokens},
		{"prompt_tokens_details.cache_write_tokens", usage.PromptTokensDetails.CacheWriteTokens},
		{"prompt_tokens_details.text_tokens", usage.PromptTokensDetails.TextTokens},
		{"prompt_tokens_details.audio_tokens", usage.PromptTokensDetails.AudioTokens},
		{"prompt_tokens_details.image_tokens", usage.PromptTokensDetails.ImageTokens},
		{"completion_tokens_details.text_tokens", usage.CompletionTokenDetails.TextTokens},
		{"completion_tokens_details.audio_tokens", usage.CompletionTokenDetails.AudioTokens},
		{"completion_tokens_details.image_tokens", usage.CompletionTokenDetails.ImageTokens},
		{"completion_tokens_details.reasoning_tokens", usage.CompletionTokenDetails.ReasoningTokens},
		{"claude_cache_creation_5_m_tokens", usage.ClaudeCacheCreation5mTokens},
		{"claude_cache_creation_1_h_tokens", usage.ClaudeCacheCreation1hTokens},
		{"gemini_cached_audio_input_tokens", usage.GeminiCachedAudioInputTokens},
		{"gemini_cached_image_input_tokens", usage.GeminiCachedImageInputTokens},
	}
	for _, counter := range counters {
		if err := validateUsageCounter(prefix+"."+counter.name, counter.value); err != nil {
			return err
		}
	}
	if usage.InputTokensDetails != nil {
		inputDetails := usage.InputTokensDetails
		inputCounters := []struct {
			name  string
			value int
		}{
			{"cached_tokens", inputDetails.CachedTokens},
			{"cached_creation_tokens", inputDetails.CachedCreationTokens},
			{"cache_write_tokens", inputDetails.CacheWriteTokens},
			{"text_tokens", inputDetails.TextTokens},
			{"audio_tokens", inputDetails.AudioTokens},
			{"image_tokens", inputDetails.ImageTokens},
		}
		for _, counter := range inputCounters {
			if err := validateUsageCounter(prefix+".input_tokens_details."+counter.name, counter.value); err != nil {
				return err
			}
		}
	}
	if err := validateUsageCounterSum(prefix+".prompt_tokens+completion_tokens", usage.PromptTokens, usage.CompletionTokens); err != nil {
		return err
	}
	if err := validateUsageCounterSum(prefix+".input_tokens+output_tokens", usage.InputTokens, usage.OutputTokens); err != nil {
		return err
	}
	if err := validateUsageCounterSum(prefix+".claude_cache_creation_tokens", usage.ClaudeCacheCreation5mTokens, usage.ClaudeCacheCreation1hTokens); err != nil {
		return err
	}
	return validateUsageCounterSum(prefix+".gemini_cached_input_tokens", usage.GeminiCachedAudioInputTokens, usage.GeminiCachedImageInputTokens)
}

func validateBillingUsageDetailConsistency(usage *dto.Usage) error {
	if usage == nil {
		return nil
	}
	inputTotal := usage.PromptTokens
	if usage.UsageSource == dto.BillingUsageSourceClaudeMessages && usage.InputTokens > inputTotal {
		// Anthropic input_tokens excludes cache categories. The converted
		// InputTokens field is their validated aggregate and therefore the
		// authoritative ceiling for its separately billed details.
		inputTotal = usage.InputTokens
	}
	inputDetails := []struct {
		name  string
		value int
	}{
		{"cached_tokens", usage.PromptTokensDetails.CachedTokens},
		{"cached_creation_tokens", usage.PromptTokensDetails.CachedCreationTokens},
		{"cache_write_tokens", usage.PromptTokensDetails.CacheWriteTokens},
		{"text_tokens", usage.PromptTokensDetails.TextTokens},
		{"audio_tokens", usage.PromptTokensDetails.AudioTokens},
		{"image_tokens", usage.PromptTokensDetails.ImageTokens},
		{"claude_cache_creation_5_m_tokens", usage.ClaudeCacheCreation5mTokens},
		{"claude_cache_creation_1_h_tokens", usage.ClaudeCacheCreation1hTokens},
	}
	for _, detail := range inputDetails {
		if detail.value > inputTotal {
			return fmt.Errorf("billing usage detail %s exceeds authoritative input total %d: %d", detail.name, inputTotal, detail.value)
		}
	}
	outputDetails := []struct {
		name  string
		value int
	}{
		{"text_tokens", usage.CompletionTokenDetails.TextTokens},
		{"audio_tokens", usage.CompletionTokenDetails.AudioTokens},
		{"image_tokens", usage.CompletionTokenDetails.ImageTokens},
		{"reasoning_tokens", usage.CompletionTokenDetails.ReasoningTokens},
	}
	for _, detail := range outputDetails {
		if detail.value > usage.CompletionTokens {
			return fmt.Errorf("billing usage detail %s exceeds authoritative output total %d: %d", detail.name, usage.CompletionTokens, detail.value)
		}
	}

	if usage.UsageSemantic == dto.BillingUsageSemanticAnthropic || usage.UsageSource == dto.BillingUsageSourceClaudeMessages {
		if err := validateUsageBreakdownTotal(
			"Anthropic input category",
			usage.InputTokens,
			usage.PromptTokens,
			usage.PromptTokensDetails.CachedTokens,
			usage.PromptTokensDetails.CachedCreationTokens,
		); err != nil {
			return err
		}
		if err := validateUsageBreakdownTotal(
			"Anthropic cache creation split",
			usage.PromptTokensDetails.CachedCreationTokens,
			usage.ClaudeCacheCreation5mTokens,
			usage.ClaudeCacheCreation1hTokens,
		); err != nil {
			return err
		}
	}

	if usage.UsageSemantic == dto.BillingUsageSemanticGemini || usage.UsageSource == dto.BillingUsageSourceGeminiChat {
		if err := validateUsageBreakdownTotal(
			"Gemini input modality",
			usage.PromptTokens,
			usage.PromptTokensDetails.TextTokens,
			usage.PromptTokensDetails.AudioTokens,
			usage.PromptTokensDetails.ImageTokens,
		); err != nil {
			return err
		}
		if err := validateUsageBreakdownTotal(
			"Gemini output modality",
			usage.CompletionTokens,
			usage.CompletionTokenDetails.TextTokens,
			usage.CompletionTokenDetails.AudioTokens,
			usage.CompletionTokenDetails.ImageTokens,
			usage.CompletionTokenDetails.ReasoningTokens,
		); err != nil {
			return err
		}
		if err := validateUsageBreakdownTotal(
			"Gemini cached input modality",
			usage.PromptTokensDetails.CachedTokens,
			usage.GeminiCachedAudioInputTokens,
			usage.GeminiCachedImageInputTokens,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateUsageBreakdownTotal(name string, authoritativeTotal int, values ...int) error {
	total := 0
	for _, value := range values {
		if value < 0 {
			return fmt.Errorf("billing usage %s breakdown contains a negative value: %d", name, value)
		}
		if value > common.MaxTokensLimit-total {
			return fmt.Errorf("billing usage %s breakdown exceeds limit %d", name, common.MaxTokensLimit)
		}
		total += value
	}
	if total > authoritativeTotal {
		return fmt.Errorf("billing usage %s breakdown exceeds authoritative total %d: %d", name, authoritativeTotal, total)
	}
	return nil
}

func validateClaudeUsageCounters(usage *dto.ClaudeUsage) error {
	counters := []struct {
		name  string
		value int
	}{
		{"input_tokens", usage.InputTokens},
		{"output_tokens", usage.OutputTokens},
		{"cache_creation_input_tokens", usage.CacheCreationInputTokens},
		{"cache_read_input_tokens", usage.CacheReadInputTokens},
		{"claude_cache_creation_5_m_tokens", usage.ClaudeCacheCreation5mTokens},
		{"claude_cache_creation_1_h_tokens", usage.ClaudeCacheCreation1hTokens},
	}
	for _, counter := range counters {
		if err := validateUsageCounter("billing_usage.claude_usage."+counter.name, counter.value); err != nil {
			return err
		}
	}
	if usage.CacheCreation != nil {
		if err := validateUsageCounter("billing_usage.claude_usage.cache_creation.ephemeral_5m_input_tokens", usage.CacheCreation.Ephemeral5mInputTokens); err != nil {
			return err
		}
		if err := validateUsageCounter("billing_usage.claude_usage.cache_creation.ephemeral_1h_input_tokens", usage.CacheCreation.Ephemeral1hInputTokens); err != nil {
			return err
		}
		if err := validateUsageCounterSum("billing_usage.claude_usage.cache_creation", usage.CacheCreation.Ephemeral5mInputTokens, usage.CacheCreation.Ephemeral1hInputTokens); err != nil {
			return err
		}
	}
	if usage.ServerToolUse != nil {
		if err := validateUsageCounter("billing_usage.claude_usage.server_tool_use.web_search_requests", usage.ServerToolUse.WebSearchRequests); err != nil {
			return err
		}
	}
	if err := validateUsageCounterSum("billing_usage.claude_usage.total_tokens", usage.InputTokens, usage.OutputTokens); err != nil {
		return err
	}
	if err := validateUsageCounterSum("billing_usage.claude_usage.total_input_tokens", usage.InputTokens, usage.CacheReadInputTokens, usage.CacheCreationInputTokens); err != nil {
		return err
	}
	return validateUsageCounterSum("billing_usage.claude_usage.legacy_cache_creation_tokens", usage.ClaudeCacheCreation5mTokens, usage.ClaudeCacheCreation1hTokens)
}

func validateGeminiUsageCounters(metadata *dto.GeminiUsageMetadata) error {
	counters := []struct {
		name  string
		value int
	}{
		{"promptTokenCount", metadata.PromptTokenCount},
		{"toolUsePromptTokenCount", metadata.ToolUsePromptTokenCount},
		{"candidatesTokenCount", metadata.CandidatesTokenCount},
		{"totalTokenCount", metadata.TotalTokenCount},
		{"thoughtsTokenCount", metadata.ThoughtsTokenCount},
		{"cachedContentTokenCount", metadata.CachedContentTokenCount},
	}
	for _, counter := range counters {
		if err := validateUsageCounter("billing_usage.gemini_usage_metadata."+counter.name, counter.value); err != nil {
			return err
		}
	}

	detailGroups := []struct {
		name    string
		target  string
		details []dto.GeminiPromptTokensDetails
	}{
		{"promptTokensDetails", "input", metadata.PromptTokensDetails},
		{"cacheTokensDetails", "cache", metadata.CacheTokensDetails},
		{"toolUsePromptTokensDetails", "input", metadata.ToolUsePromptTokensDetails},
		{"candidatesTokensDetails", "output", metadata.CandidatesTokensDetails},
	}
	convertedModalityTotals := map[string]int{}
	for _, group := range detailGroups {
		for index, detail := range group.details {
			path := fmt.Sprintf("billing_usage.gemini_usage_metadata.%s[%d].tokenCount", group.name, index)
			if err := validateUsageCounter(path, detail.TokenCount); err != nil {
				return err
			}
			modality := strings.ToUpper(detail.Modality)
			totalKey := group.target + "." + modality
			total := convertedModalityTotals[totalKey]
			if detail.TokenCount > common.MaxTokensLimit-total {
				return fmt.Errorf("billing usage counter %s details for modality %q exceeds limit %d", group.target, modality, common.MaxTokensLimit)
			}
			convertedModalityTotals[totalKey] = total + detail.TokenCount
		}
	}

	if err := validateUsageCounterSum("billing_usage.gemini_usage_metadata.prompt_tokens", metadata.PromptTokenCount, metadata.ToolUsePromptTokenCount); err != nil {
		return err
	}
	promptTokens := metadata.PromptTokenCount + metadata.ToolUsePromptTokenCount
	if err := validateUsageCounterSum("billing_usage.gemini_usage_metadata.completion_tokens", metadata.CandidatesTokenCount, metadata.ThoughtsTokenCount); err != nil {
		return err
	}
	completionTokens := metadata.CandidatesTokenCount + metadata.ThoughtsTokenCount
	if err := validateUsageCounterSum("billing_usage.gemini_usage_metadata.derived_total_tokens", promptTokens, completionTokens); err != nil {
		return err
	}
	if metadata.TotalTokenCount > 0 && completionTokens == 0 && metadata.TotalTokenCount < promptTokens {
		return fmt.Errorf("billing usage counter totalTokenCount %d is less than prompt tokens %d", metadata.TotalTokenCount, promptTokens)
	}
	return nil
}

func validateUsageCounter(name string, value int) error {
	if value < 0 {
		return fmt.Errorf("billing usage counter %s cannot be negative: %d", name, value)
	}
	if value > common.MaxTokensLimit {
		return fmt.Errorf("billing usage counter %s exceeds limit %d: %d", name, common.MaxTokensLimit, value)
	}
	return nil
}

func validateUsageCounterSum(name string, values ...int) error {
	total := 0
	for _, value := range values {
		if value < 0 {
			return fmt.Errorf("billing usage counter %s contains a negative value: %d", name, value)
		}
		if value > common.MaxTokensLimit-total {
			return fmt.Errorf("billing usage counter %s exceeds limit %d", name, common.MaxTokensLimit)
		}
		total += value
	}
	return nil
}

func usageBillingPathForLog(isLocalCountTokens bool, usage *dto.Usage) string {
	if isLocalCountTokens {
		return usageBillingPathLocal
	}
	if usage == nil || usage.BillingUsage == nil {
		return usageBillingPathUpstream
	}
	source := strings.TrimSpace(usage.BillingUsage.Source)
	semantic := strings.TrimSpace(usage.BillingUsage.Semantic)
	if strings.EqualFold(source, dto.BillingUsageSourceOAIChat) ||
		strings.EqualFold(source, dto.BillingUsageSourceOAIResponses) ||
		strings.EqualFold(semantic, dto.BillingUsageSemanticOpenAI) {
		if usage.BillingUsage.Estimated {
			return usageBillingPathOpenAIEstimated
		}
		return usageBillingPathOpenAI
	}
	if strings.EqualFold(source, dto.BillingUsageSourceClaudeMessages) ||
		strings.EqualFold(semantic, dto.BillingUsageSemanticAnthropic) {
		if usage.BillingUsage.Estimated {
			return usageBillingPathAnthropicEstimated
		}
		return usageBillingPathAnthropic
	}
	if strings.EqualFold(source, dto.BillingUsageSourceGeminiChat) ||
		strings.EqualFold(semantic, dto.BillingUsageSemanticGemini) {
		if usage.BillingUsage.Estimated {
			return usageBillingPathGeminiEstimated
		}
		return usageBillingPathGemini
	}
	return usageBillingPathUpstream
}

func appendUsageBillingPathForLog(other map[string]interface{}, isLocalCountTokens bool, usage *dto.Usage) {
	if other == nil {
		return
	}
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	if !ok || adminInfo == nil {
		adminInfo = make(map[string]interface{})
		other["admin_info"] = adminInfo
	}
	adminInfo["usage_billing_path"] = usageBillingPathForLog(isLocalCountTokens, usage)
}

func usageFromBillingUsage(usage *dto.Usage) (*dto.Usage, bool) {
	if usage == nil || usage.BillingUsage == nil {
		return nil, false
	}
	billingUsage := usage.BillingUsage
	source := strings.TrimSpace(billingUsage.Source)
	semantic := strings.TrimSpace(billingUsage.Semantic)

	if billingUsage.OpenAIUsage != nil &&
		(strings.EqualFold(source, dto.BillingUsageSourceOAIChat) ||
			strings.EqualFold(source, dto.BillingUsageSourceOAIResponses) ||
			strings.EqualFold(semantic, dto.BillingUsageSemanticOpenAI)) {
		return usageFromOpenAIBillingUsage(billingUsage), true
	}

	if billingUsage.ClaudeUsage != nil &&
		(strings.EqualFold(source, dto.BillingUsageSourceClaudeMessages) ||
			strings.EqualFold(semantic, dto.BillingUsageSemanticAnthropic)) {
		return usageFromClaudeBillingUsage(billingUsage), true
	}

	if billingUsage.GeminiUsageMetadata != nil &&
		(strings.EqualFold(source, dto.BillingUsageSourceGeminiChat) ||
			strings.EqualFold(semantic, dto.BillingUsageSemanticGemini)) {
		return usageFromGeminiBillingUsage(billingUsage), true
	}

	return nil, false
}

func usageFromOpenAIBillingUsage(billingUsage *dto.BillingUsage) *dto.Usage {
	usage := *billingUsage.OpenAIUsage
	if usage.PromptTokens == 0 && usage.InputTokens > 0 {
		usage.PromptTokens = usage.InputTokens
	}
	if usage.CompletionTokens == 0 && usage.OutputTokens > 0 {
		usage.CompletionTokens = usage.OutputTokens
	}
	if usage.InputTokens == 0 && usage.PromptTokens > 0 {
		usage.InputTokens = usage.PromptTokens
	}
	if usage.OutputTokens == 0 && usage.CompletionTokens > 0 {
		usage.OutputTokens = usage.CompletionTokens
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	usage.UsageSemantic = dto.BillingUsageSemanticOpenAI
	usage.UsageSource = billingUsage.Source
	usage.BillingUsage = dto.CloneBillingUsage(billingUsage)
	return &usage
}

func usageFromClaudeBillingUsage(billingUsage *dto.BillingUsage) *dto.Usage {
	claudeUsage := billingUsage.ClaudeUsage
	cacheCreation5m := claudeUsage.GetCacheCreation5mTokens()
	if cacheCreation5m == 0 {
		cacheCreation5m = claudeUsage.ClaudeCacheCreation5mTokens
	}
	cacheCreation1h := claudeUsage.GetCacheCreation1hTokens()
	if cacheCreation1h == 0 {
		cacheCreation1h = claudeUsage.ClaudeCacheCreation1hTokens
	}
	cacheCreationTotal := claudeUsage.CacheCreationInputTokens
	if cacheCreationTotal == 0 {
		// Some compatible Anthropic relays only expose the legacy 5-minute and
		// 1-hour split. Treat their validated sum as the aggregate so the split
		// remains bounded and does not get billed in addition to an absent base.
		cacheCreationTotal = cacheCreation5m + cacheCreation1h
	}

	usage := &dto.Usage{
		PromptTokens:                claudeUsage.InputTokens,
		CompletionTokens:            claudeUsage.OutputTokens,
		TotalTokens:                 claudeUsage.InputTokens + claudeUsage.OutputTokens,
		InputTokens:                 claudeUsage.InputTokens + claudeUsage.CacheReadInputTokens + cacheCreationTotal,
		OutputTokens:                claudeUsage.OutputTokens,
		UsageSemantic:               dto.BillingUsageSemanticAnthropic,
		UsageSource:                 dto.BillingUsageSourceClaudeMessages,
		BillingUsage:                dto.CloneBillingUsage(billingUsage),
		ClaudeCacheCreation5mTokens: cacheCreation5m,
		ClaudeCacheCreation1hTokens: cacheCreation1h,
		ClaudeSpeed:                 claudeUsage.Speed,
	}
	usage.PromptTokensDetails.CachedTokens = claudeUsage.CacheReadInputTokens
	usage.PromptTokensDetails.CachedCreationTokens = cacheCreationTotal
	return usage
}

func usageFromGeminiBillingUsage(billingUsage *dto.BillingUsage) *dto.Usage {
	metadata := *billingUsage.GeminiUsageMetadata
	promptTokens := metadata.PromptTokenCount + metadata.ToolUsePromptTokenCount
	usage := &dto.Usage{
		PromptTokens:      promptTokens,
		CompletionTokens:  metadata.CandidatesTokenCount + metadata.ThoughtsTokenCount,
		TotalTokens:       metadata.TotalTokenCount,
		GeminiServiceTier: metadata.ServiceTier,
		UsageSemantic:     dto.BillingUsageSemanticGemini,
		UsageSource:       dto.BillingUsageSourceGeminiChat,
		BillingUsage:      dto.CloneBillingUsage(billingUsage),
	}
	usage.CompletionTokenDetails.ReasoningTokens = metadata.ThoughtsTokenCount
	usage.PromptTokensDetails.CachedTokens = metadata.CachedContentTokenCount
	for _, detail := range metadata.CacheTokensDetails {
		switch detail.Modality {
		case "AUDIO":
			usage.GeminiCachedAudioInputTokens += detail.TokenCount
		case "IMAGE":
			usage.GeminiCachedImageInputTokens += detail.TokenCount
		}
	}

	for _, detail := range metadata.PromptTokensDetails {
		addGeminiInputTokenDetail(&usage.PromptTokensDetails, detail)
	}
	for _, detail := range metadata.ToolUsePromptTokensDetails {
		addGeminiInputTokenDetail(&usage.PromptTokensDetails, detail)
	}
	for _, detail := range metadata.CandidatesTokensDetails {
		switch detail.Modality {
		case "IMAGE":
			usage.CompletionTokenDetails.ImageTokens += detail.TokenCount
		case "AUDIO":
			usage.CompletionTokenDetails.AudioTokens += detail.TokenCount
		case "TEXT":
			usage.CompletionTokenDetails.TextTokens += detail.TokenCount
		}
	}

	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	} else if usage.CompletionTokens <= 0 {
		usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens
	}
	if usage.PromptTokens > 0 && usage.PromptTokensDetails.TextTokens == 0 && usage.PromptTokensDetails.AudioTokens == 0 && usage.PromptTokensDetails.ImageTokens == 0 {
		usage.PromptTokensDetails.TextTokens = usage.PromptTokens
	}
	return usage
}

func addGeminiInputTokenDetail(details *dto.InputTokenDetails, detail dto.GeminiPromptTokensDetails) {
	switch detail.Modality {
	case "AUDIO":
		details.AudioTokens += detail.TokenCount
	case "IMAGE":
		details.ImageTokens += detail.TokenCount
	case "TEXT":
		details.TextTokens += detail.TokenCount
	}
}
