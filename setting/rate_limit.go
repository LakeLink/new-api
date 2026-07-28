package setting

import (
	"fmt"
	"math"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

var ModelRequestRateLimitEnabled = false
var ModelRequestRateLimitDurationMinutes = 1
var ModelRequestRateLimitCount = 0
var ModelRequestRateLimitSuccessCount = 1000
var ModelRequestRateLimitGroup = map[string][2]int{}
var ModelRequestRateLimitMutex sync.RWMutex

func ModelRequestRateLimitGroup2JSONString() string {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	jsonBytes, err := common.Marshal(ModelRequestRateLimitGroup)
	if err != nil {
		common.SysLog("error marshalling model ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateModelRequestRateLimitGroupByJSONString(jsonStr string) error {
	next := make(map[string][2]int)
	if err := common.UnmarshalJsonStr(jsonStr, &next); err != nil {
		return err
	}
	if err := validateModelRequestRateLimitGroups(next); err != nil {
		return err
	}

	ModelRequestRateLimitMutex.Lock()
	defer ModelRequestRateLimitMutex.Unlock()
	ModelRequestRateLimitGroup = next
	return nil
}

func GetGroupRateLimit(group string) (totalCount, successCount int, found bool) {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	if ModelRequestRateLimitGroup == nil {
		return 0, 0, false
	}

	limits, found := ModelRequestRateLimitGroup[group]
	if !found {
		return 0, 0, false
	}
	return limits[0], limits[1], true
}

func CheckModelRequestRateLimitGroup(jsonStr string) error {
	checkModelRequestRateLimitGroup := make(map[string][2]int)
	if err := common.UnmarshalJsonStr(jsonStr, &checkModelRequestRateLimitGroup); err != nil {
		return err
	}
	return validateModelRequestRateLimitGroups(checkModelRequestRateLimitGroup)
}

func validateModelRequestRateLimitGroups(groups map[string][2]int) error {
	for group, limits := range groups {
		if limits[0] < 0 || limits[1] < 1 {
			return fmt.Errorf(
				"group %s rate limits must be in the ranges [0, %d] and [1, %d], got [%d, %d]",
				group, math.MaxInt32, math.MaxInt32, limits[0], limits[1],
			)
		}
		if limits[0] > math.MaxInt32 || limits[1] > math.MaxInt32 {
			return fmt.Errorf(
				"group %s rate limits must be in the ranges [0, %d] and [1, %d], got [%d, %d]",
				group, math.MaxInt32, math.MaxInt32, limits[0], limits[1],
			)
		}
	}

	return nil
}
