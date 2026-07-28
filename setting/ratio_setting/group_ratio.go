package ratio_setting

import (
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/types"
)

var defaultGroupRatio = map[string]float64{
	"default": 1,
	"vip":     1,
	"svip":    1,
}

var defaultGroupGroupRatio = map[string]map[string]float64{
	"vip": {
		"edit_this": 0.9,
	},
}

var defaultGroupSpecialUsableGroup = map[string]map[string]string{}

type GroupRatioSetting struct {
	GroupRatio              *types.RWMap[string, float64]            `json:"group_ratio"`
	GroupGroupRatio         *types.RWMap[string, map[string]float64] `json:"group_group_ratio"`
	GroupSpecialUsableGroup *types.RWMap[string, map[string]string]  `json:"group_special_usable_group"`
}

var groupRatioSetting GroupRatioSetting

func init() {
	groupRatioMap := types.NewRWMap[string, float64]()
	groupGroupRatioMap := types.NewRWMap[string, map[string]float64]()
	groupSpecialUsableGroup := types.NewRWMap[string, map[string]string]()
	groupSpecialUsableGroup.AddAll(defaultGroupSpecialUsableGroup)

	groupRatioMap.AddAll(defaultGroupRatio)
	groupGroupRatioMap.AddAll(defaultGroupGroupRatio)

	groupRatioSetting = GroupRatioSetting{
		GroupSpecialUsableGroup: groupSpecialUsableGroup,
		GroupRatio:              groupRatioMap,
		GroupGroupRatio:         groupGroupRatioMap,
	}

	config.GlobalConfig.Register("group_ratio_setting", &groupRatioSetting)
}

func GetGroupRatioSetting() *GroupRatioSetting {
	setting := config.Snapshot[GroupRatioSetting]("group_ratio_setting")
	if setting.GroupSpecialUsableGroup != nil {
		return setting
	}

	fallback := *setting
	fallback.GroupSpecialUsableGroup = types.NewRWMap[string, map[string]string]()
	fallback.GroupSpecialUsableGroup.AddAll(defaultGroupSpecialUsableGroup)
	return &fallback
}

func GetGroupRatioCopy() map[string]float64 {
	return GetGroupRatioSetting().GroupRatio.ReadAll()
}

func ContainsGroupRatio(name string) bool {
	_, ok := GetGroupRatioSetting().GroupRatio.Get(name)
	return ok
}

func GroupRatio2JSONString() string {
	return GetGroupRatioSetting().GroupRatio.MarshalJSONString()
}

func UpdateGroupRatioByJSONString(jsonStr string) error {
	if err := CheckGroupRatio(jsonStr); err != nil {
		return err
	}
	next := types.NewRWMap[string, float64]()
	if err := types.LoadFromJsonString(next, jsonStr); err != nil {
		return err
	}
	return config.Mutate(&groupRatioSetting, func() {
		groupRatioSetting.GroupRatio = next
	})
}

func GetGroupRatio(name string) float64 {
	ratio, ok := GetGroupRatioSetting().GroupRatio.Get(name)
	if !ok {
		common.SysLog("group ratio not found: " + name)
		return 1
	}
	return ratio
}

func GetGroupGroupRatio(userGroup, usingGroup string) (float64, bool) {
	gp, ok := GetGroupRatioSetting().GroupGroupRatio.Get(userGroup)
	if !ok {
		return -1, false
	}
	ratio, ok := gp[usingGroup]
	if !ok {
		return -1, false
	}
	return ratio, true
}

func GroupGroupRatio2JSONString() string {
	return GetGroupRatioSetting().GroupGroupRatio.MarshalJSONString()
}

func UpdateGroupGroupRatioByJSONString(jsonStr string) error {
	if err := CheckGroupGroupRatio(jsonStr); err != nil {
		return err
	}
	next := types.NewRWMap[string, map[string]float64]()
	if err := types.LoadFromJsonString(next, jsonStr); err != nil {
		return err
	}
	return config.Mutate(&groupRatioSetting, func() {
		groupRatioSetting.GroupGroupRatio = next
	})
}

func (s GroupRatioSetting) Validate() error {
	if s.GroupRatio == nil {
		return errors.New("group ratio must be a JSON object")
	}
	for name, ratio := range s.GroupRatio.ReadAll() {
		if name == "" {
			return errors.New("group ratio name must not be empty")
		}
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
			return errors.New("group ratio must be finite and not less than 0: " + name)
		}
	}
	if s.GroupGroupRatio == nil {
		return errors.New("group-to-group ratio must be a JSON object")
	}
	for userGroup, ratios := range s.GroupGroupRatio.ReadAll() {
		if userGroup == "" {
			return errors.New("group-to-group source name must not be empty")
		}
		for usingGroup, ratio := range ratios {
			if usingGroup == "" {
				return errors.New("group-to-group target name must not be empty")
			}
			if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
				return errors.New("group-to-group ratio must be finite and not less than 0: " + userGroup + "/" + usingGroup)
			}
		}
	}
	return nil
}

func CheckGroupRatio(jsonStr string) error {
	checkGroupRatio := make(map[string]float64)
	err := common.UnmarshalJsonStr(jsonStr, &checkGroupRatio)
	if err != nil {
		return err
	}
	if checkGroupRatio == nil {
		return errors.New("group ratio configuration must be a JSON object")
	}
	for name, ratio := range checkGroupRatio {
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
			return errors.New("group ratio must be finite and not less than 0: " + name)
		}
	}
	return nil
}
