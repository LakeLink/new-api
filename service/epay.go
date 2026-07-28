package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

func GetCallbackAddress() string {
	callbackAddress := operation_setting.GetLegacyPaymentSetting().CustomCallbackAddress
	if callbackAddress == "" {
		return common.GetLegacyOptionString("ServerAddress", &system_setting.ServerAddress)
	}
	return callbackAddress
}
