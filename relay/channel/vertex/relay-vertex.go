package vertex

import "github.com/QuantumNous/new-api/common"

func GetModelRegion(other string, localModelName string) string {
	// if other is json string
	if common.IsJsonObject(other) {
		m, err := common.StrToMap(other)
		if err != nil {
			return other // return original if parsing fails
		}
		if region, ok := m[localModelName].(string); ok && region != "" {
			return region
		}
		if region, ok := m["default"].(string); ok && region != "" {
			return region
		}
		return "global"
	}
	return other
}
