package setting

import "github.com/QuantumNous/new-api/common"

type ModelRequestRateLimitSetting struct {
	Enabled         bool
	Count           int
	DurationMinutes int
	SuccessCount    int
}

func GetModelRequestRateLimitSetting() ModelRequestRateLimitSetting {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return ModelRequestRateLimitSetting{
		Enabled:         optionBoolLocked("ModelRequestRateLimitEnabled", ModelRequestRateLimitEnabled),
		Count:           optionIntLocked("ModelRequestRateLimitCount", ModelRequestRateLimitCount),
		DurationMinutes: optionIntLocked("ModelRequestRateLimitDurationMinutes", ModelRequestRateLimitDurationMinutes),
		SuccessCount:    optionIntLocked("ModelRequestRateLimitSuccessCount", ModelRequestRateLimitSuccessCount),
	}
}

func IsMjNotifyEnabled() bool {
	return common.GetLegacyOptionBool("MjNotifyEnabled", &MjNotifyEnabled)
}

func IsMjAccountFilterEnabled() bool {
	return common.GetLegacyOptionBool("MjAccountFilterEnabled", &MjAccountFilterEnabled)
}

func IsMjModeClearEnabled() bool {
	return common.GetLegacyOptionBool("MjModeClearEnabled", &MjModeClearEnabled)
}

func IsMjForwardURLEnabled() bool {
	return common.GetLegacyOptionBool("MjForwardUrlEnabled", &MjForwardUrlEnabled)
}

func IsMjActionCheckSuccessEnabled() bool {
	return common.GetLegacyOptionBool("MjActionCheckSuccessEnabled", &MjActionCheckSuccessEnabled)
}

func IsDefaultAutoGroupEnabled() bool {
	return common.GetLegacyOptionBool("DefaultUseAutoGroup", &DefaultUseAutoGroup)
}
