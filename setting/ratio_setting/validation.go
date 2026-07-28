package ratio_setting

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
)

func CheckRatioMap(jsonStr string) error {
	var ratios map[string]float64
	if err := common.UnmarshalJsonStr(jsonStr, &ratios); err != nil {
		return err
	}
	if ratios == nil {
		return fmt.Errorf("ratio configuration must be a JSON object")
	}
	for name, ratio := range ratios {
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
			return fmt.Errorf("ratio for %s must be finite and not less than 0", name)
		}
	}
	return nil
}

func CheckGroupGroupRatio(jsonStr string) error {
	var ratios map[string]map[string]float64
	if err := common.UnmarshalJsonStr(jsonStr, &ratios); err != nil {
		return err
	}
	if ratios == nil {
		return fmt.Errorf("group-to-group ratio configuration must be a JSON object")
	}
	for userGroup, groupRatios := range ratios {
		if groupRatios == nil {
			return fmt.Errorf("group-to-group ratios for %s must be a JSON object", userGroup)
		}
		for usingGroup, ratio := range groupRatios {
			if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
				return fmt.Errorf("group-to-group ratio for %s/%s must be finite and not less than 0", userGroup, usingGroup)
			}
		}
	}
	return nil
}
