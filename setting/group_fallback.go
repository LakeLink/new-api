package setting

import (
	"fmt"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

const (
	GroupFallbackPricingModeOrigin = "origin"
	GroupFallbackPricingModeTarget = "target"

	GroupFallbackTargetRatioModeOriginSpecial       = "origin_special"
	GroupFallbackTargetRatioModeTargetSpecial       = "target_special"
	GroupFallbackTargetRatioModeNormalOnly          = "normal_only"
	GroupFallbackTargetRatioModePreferOriginSpecial = "prefer_origin_special"
	GroupFallbackTargetRatioModePreferTargetSpecial = "prefer_target_special"
)

type GroupFallbackRule struct {
	Fallback                     []string `json:"fallback"`
	PricingMode                  string   `json:"pricing_mode"` // "origin" or "target"
	OriginPricingUseSpecialRatio *bool    `json:"origin_pricing_use_special_ratio,omitempty"`
	TargetPricingRatioMode       string   `json:"target_pricing_ratio_mode,omitempty"`
}

func (r GroupFallbackRule) ShouldUseOriginPricingSpecialRatio() bool {
	return r.OriginPricingUseSpecialRatio == nil || *r.OriginPricingUseSpecialRatio
}

func (r GroupFallbackRule) EffectiveTargetPricingRatioMode() string {
	switch r.TargetPricingRatioMode {
	case GroupFallbackTargetRatioModeOriginSpecial,
		GroupFallbackTargetRatioModeTargetSpecial,
		GroupFallbackTargetRatioModeNormalOnly,
		GroupFallbackTargetRatioModePreferOriginSpecial,
		GroupFallbackTargetRatioModePreferTargetSpecial:
		return r.TargetPricingRatioMode
	default:
		return GroupFallbackTargetRatioModeTargetSpecial
	}
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
	if err := validateGroupFallbackJSONString(jsonStr, &parsed); err != nil {
		return err
	}

	groupFallbackMutex.Lock()
	defer groupFallbackMutex.Unlock()
	groupFallback = parsed
	return nil
}

func ValidateGroupFallbackJSONString(jsonStr string) error {
	return validateGroupFallbackJSONString(jsonStr, nil)
}

func validateGroupFallbackJSONString(jsonStr string, destination *map[string]GroupFallbackRule) error {
	if strings.TrimSpace(jsonStr) == "" {
		jsonStr = "{}"
	}
	var parsed map[string]GroupFallbackRule
	if err := common.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return err
	}
	if parsed == nil {
		return fmt.Errorf("group fallback configuration must be a JSON object")
	}
	for group, rule := range parsed {
		if strings.TrimSpace(group) == "" {
			return fmt.Errorf("group fallback source name must not be empty")
		}
		switch rule.PricingMode {
		case "", GroupFallbackPricingModeOrigin, GroupFallbackPricingModeTarget:
		default:
			return fmt.Errorf("group fallback pricing mode for %q must be origin or target", group)
		}
		switch rule.TargetPricingRatioMode {
		case "",
			GroupFallbackTargetRatioModeOriginSpecial,
			GroupFallbackTargetRatioModeTargetSpecial,
			GroupFallbackTargetRatioModeNormalOnly,
			GroupFallbackTargetRatioModePreferOriginSpecial,
			GroupFallbackTargetRatioModePreferTargetSpecial:
		default:
			return fmt.Errorf("group fallback target ratio mode for %q is invalid", group)
		}
		for _, fallback := range rule.Fallback {
			if strings.TrimSpace(fallback) == "" {
				return fmt.Errorf("group fallback target for %q must not be empty", group)
			}
		}
	}
	if destination != nil {
		*destination = parsed
	}
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
