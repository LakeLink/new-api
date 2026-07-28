package common

import (
	"fmt"
	"math"
	"sync"
)

var topupGroupRatio = map[string]float64{
	"default": 1,
	"vip":     1,
	"svip":    1,
}
var topupGroupRatioMutex sync.RWMutex

func TopupGroupRatio2JSONString() string {
	topupGroupRatioMutex.RLock()
	defer topupGroupRatioMutex.RUnlock()
	jsonBytes, err := Marshal(topupGroupRatio)
	if err != nil {
		SysError("error marshalling topup group ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateTopupGroupRatioByJSONString(jsonStr string) error {
	next, err := parseTopupGroupRatio(jsonStr)
	if err != nil {
		return err
	}

	topupGroupRatioMutex.Lock()
	defer topupGroupRatioMutex.Unlock()
	topupGroupRatio = next
	return nil
}

func ValidateTopupGroupRatioJSONString(jsonStr string) error {
	_, err := parseTopupGroupRatio(jsonStr)
	return err
}

func parseTopupGroupRatio(jsonStr string) (map[string]float64, error) {
	next := make(map[string]float64)
	if err := UnmarshalJsonStr(jsonStr, &next); err != nil {
		return nil, err
	}
	if next == nil {
		return nil, fmt.Errorf("top-up group ratios must be a JSON object")
	}
	for group, ratio := range next {
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 {
			return nil, fmt.Errorf("top-up group ratio for %s must be a positive finite number", group)
		}
	}
	return next, nil
}

func GetTopupGroupRatio(name string) float64 {
	topupGroupRatioMutex.RLock()
	defer topupGroupRatioMutex.RUnlock()
	ratio, ok := topupGroupRatio[name]
	if !ok {
		SysError("topup group ratio not found: " + name)
		return 1
	}
	return ratio
}
