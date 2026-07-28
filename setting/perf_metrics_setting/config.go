package perf_metrics_setting

import (
	"fmt"
	"math"
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

type PerfMetricsSetting struct {
	Enabled       bool   `json:"enabled"`
	FlushInterval int    `json:"flush_interval"`
	BucketTime    string `json:"bucket_time"`
	RetentionDays int    `json:"retention_days"`
}

var perfMetricsSetting = PerfMetricsSetting{
	Enabled:       true,
	FlushInterval: 5,
	BucketTime:    "hour",
	RetentionDays: 0,
}

func (s PerfMetricsSetting) Validate() error {
	maxMinutes := int64(math.MaxInt64 / int64(time.Minute))
	if s.FlushInterval < 1 || int64(s.FlushInterval) > maxMinutes {
		return fmt.Errorf("flush interval must be in the range [1, %d] minutes", maxMinutes)
	}
	switch s.BucketTime {
	case "minute", "5min", "hour":
	default:
		return fmt.Errorf("bucket time must be one of minute, 5min, or hour")
	}
	maxRetentionDays := int64(math.MaxInt64 / int64(24*time.Hour))
	if s.RetentionDays < 0 || int64(s.RetentionDays) > maxRetentionDays {
		return fmt.Errorf("retention days must be in the range [0, %d]", maxRetentionDays)
	}
	return nil
}

func init() {
	config.GlobalConfig.Register("perf_metrics_setting", &perfMetricsSetting)
}

func GetSetting() PerfMetricsSetting {
	return *config.Snapshot[PerfMetricsSetting]("perf_metrics_setting")
}

func GetBucketSeconds() int64 {
	switch GetSetting().BucketTime {
	case "minute":
		return 60
	case "5min":
		return 300
	case "hour":
		return 3600
	default:
		return 3600
	}
}

func GetFlushIntervalMinutes() int {
	setting := GetSetting()
	if setting.FlushInterval < 1 {
		return 1
	}
	return setting.FlushInterval
}
