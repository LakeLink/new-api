package active_request_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
)

// ActiveRequestSetting configures the active request monitor behavior.
type ActiveRequestSetting struct {
	// TimeoutSeconds is the maximum time (in seconds) a relay request may remain
	// active before it is automatically terminated. 0 means disabled (default).
	TimeoutSeconds int `json:"timeout_seconds"`
}

var activeRequestSetting = ActiveRequestSetting{
	TimeoutSeconds: 0,
}

func init() {
	config.GlobalConfig.Register("active_request_setting", &activeRequestSetting)
}

// GetActiveRequestSetting returns the current active request setting.
func GetActiveRequestSetting() *ActiveRequestSetting {
	return &activeRequestSetting
}
