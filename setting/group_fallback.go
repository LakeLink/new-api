package setting

import (
	"sync"

	"github.com/QuantumNous/new-api/common"
)

type GroupFallbackRule struct {
	Fallback    []string `json:"fallback"`
	PricingMode string   `json:"pricing_mode"` // "origin" or "target"
}

var groupFallback = map[string]GroupFallbackRule{}
var groupFallbackMutex sync.RWMutex

func GetGroupFallback(group string) (GroupFallbackRule, bool) {
	groupFallbackMutex.RLock()
	defer groupFallbackMutex.RUnlock()
	rule, ok := groupFallback[group]
	return rule, ok
}

func UpdateGroupFallbackByJsonString(jsonStr string) error {
	var parsed map[string]GroupFallbackRule
	if err := common.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return err
	}

	groupFallbackMutex.Lock()
	defer groupFallbackMutex.Unlock()
	groupFallback = parsed
	return nil
}

func GroupFallback2JSONString() string {
	groupFallbackMutex.RLock()
	defer groupFallbackMutex.RUnlock()
	jsonBytes, err := common.Marshal(groupFallback)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}
