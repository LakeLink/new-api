package ratio_setting

import "strings"

// GetPerplexityRequestPrice returns the Sonar request fee in USD. Token and
// research/search-query charges are reported separately in upstream usage.
func GetPerplexityRequestPrice(model, searchContextSize, searchType string) (float64, bool) {
	contextIndex := 0
	switch strings.ToLower(strings.TrimSpace(searchContextSize)) {
	case "", "low":
	case "medium":
		contextIndex = 1
	case "high":
		contextIndex = 2
	default:
		return 0, false
	}

	var prices [3]float64
	switch model {
	case "sonar":
		prices = [3]float64{0.005, 0.008, 0.012}
	case "sonar-pro":
		if searchType = strings.ToLower(strings.TrimSpace(searchType)); searchType == "pro" || searchType == "auto" {
			// Auto can select Pro Search, so reserve the documented Pro fee.
			prices = [3]float64{0.014, 0.018, 0.022}
		} else {
			prices = [3]float64{0.006, 0.010, 0.014}
		}
	case "sonar-reasoning-pro":
		prices = [3]float64{0.006, 0.010, 0.014}
	default:
		// Deep Research has no fixed request fee; its provider-reported cost
		// includes citation, reasoning, and search-query charges.
		return 0, false
	}
	return prices[contextIndex], true
}
