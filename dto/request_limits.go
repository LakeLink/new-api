package dto

import "github.com/QuantumNous/new-api/common"

// boundedMaxTokenProduct keeps request token estimates representable on every
// architecture even when a DTO is constructed outside the normal validators.
// Production validation rejects products above the same limit; this clamp is
// defense in depth for direct callers of GetTokenCountMeta.
func boundedMaxTokenProduct(maxTokens uint, multiplier int) int {
	if maxTokens == 0 {
		return 0
	}
	if multiplier <= 0 {
		multiplier = 1
	}
	if maxTokens > uint(common.MaxTokensLimit)/uint(multiplier) {
		return common.MaxTokensLimit
	}
	return int(maxTokens * uint(multiplier))
}
