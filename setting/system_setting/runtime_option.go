package system_setting

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
)

type WorkerSetting struct {
	URL                   string
	ValidKey              string
	AllowHTTPImageRequest bool
}

func GetServerAddress() string {
	return common.GetLegacyOptionString("ServerAddress", &ServerAddress)
}

func GetWorkerSetting() WorkerSetting {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	setting := WorkerSetting{
		URL:                   WorkerUrl,
		ValidKey:              WorkerValidKey,
		AllowHTTPImageRequest: WorkerAllowHttpImageRequestEnabled,
	}
	if value, ok := common.OptionMap["WorkerUrl"]; ok {
		setting.URL = value
	}
	if value, ok := common.OptionMap["WorkerValidKey"]; ok {
		setting.ValidKey = value
	}
	if value, ok := common.OptionMap["WorkerAllowHttpImageRequestEnabled"]; ok {
		if parsed, err := strconv.ParseBool(value); err == nil {
			setting.AllowHTTPImageRequest = parsed
		}
	}
	return setting
}
