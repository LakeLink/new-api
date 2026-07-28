package operation_setting

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

type MonitorSetting struct {
	AutoTestChannelEnabled bool    `json:"auto_test_channel_enabled"`
	AutoTestChannelMinutes float64 `json:"auto_test_channel_minutes"`
	ChannelTestMode        string  `json:"channel_test_mode"`
}

const (
	ChannelTestModeScheduledAll    = "scheduled_all"
	ChannelTestModePassiveRecovery = "passive_recovery"
)

// 默认配置
var monitorSetting = MonitorSetting{
	AutoTestChannelEnabled: false,
	AutoTestChannelMinutes: 10,
	ChannelTestMode:        ChannelTestModeScheduledAll,
}

func (s MonitorSetting) Validate() error {
	maxMinutes := float64(math.MaxInt64 / int64(time.Minute))
	if math.IsNaN(s.AutoTestChannelMinutes) || math.IsInf(s.AutoTestChannelMinutes, 0) ||
		s.AutoTestChannelMinutes <= 0 || s.AutoTestChannelMinutes > maxMinutes {
		return fmt.Errorf("automatic channel test interval must be finite and in the range (0, %.0f] minutes", maxMinutes)
	}
	switch s.ChannelTestMode {
	case "", ChannelTestModeScheduledAll, ChannelTestModePassiveRecovery:
	default:
		return fmt.Errorf("channel test mode must be scheduled_all or passive_recovery")
	}
	return nil
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("monitor_setting", &monitorSetting)
}

func GetMonitorSetting() *MonitorSetting {
	setting := *config.Snapshot[MonitorSetting]("monitor_setting")
	if os.Getenv("CHANNEL_TEST_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_TEST_FREQUENCY"))
		if err == nil && frequency > 0 {
			setting.AutoTestChannelEnabled = true
			setting.AutoTestChannelMinutes = float64(frequency)
			setting.ChannelTestMode = ChannelTestModeScheduledAll
		}
	}
	if enabled, ok := os.LookupEnv("CHANNEL_TEST_ENABLED"); ok {
		parsed, err := strconv.ParseBool(enabled)
		if err == nil {
			setting.AutoTestChannelEnabled = parsed
		}
	}
	if setting.ChannelTestMode != ChannelTestModePassiveRecovery {
		setting.ChannelTestMode = ChannelTestModeScheduledAll
	}
	return &setting
}
