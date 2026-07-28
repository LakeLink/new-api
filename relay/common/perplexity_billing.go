package common

import "github.com/QuantumNous/new-api/dto"

// PerplexityRequestPricingParams extracts the request fields that select the
// provider's per-request search fee. Responses requests do not expose these
// Chat Completions options and therefore use the provider's default tier.
func PerplexityRequestPricingParams(request dto.Request) (searchContextSize, searchType string) {
	if chatRequest, ok := request.(*dto.GeneralOpenAIRequest); ok && chatRequest.WebSearchOptions != nil {
		return chatRequest.WebSearchOptions.SearchContextSize, chatRequest.WebSearchOptions.SearchType
	}
	return "", ""
}
