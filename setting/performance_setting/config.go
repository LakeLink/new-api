package performance_setting

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// PerformanceSetting 性能设置配置
type PerformanceSetting struct {
	// DiskCacheEnabled 是否启用磁盘缓存（磁盘换内存）
	DiskCacheEnabled bool `json:"disk_cache_enabled"`
	// DiskCacheThresholdMB 触发磁盘缓存的请求体大小阈值（MB）
	DiskCacheThresholdMB int `json:"disk_cache_threshold_mb"`
	// DiskCacheMaxSizeMB 磁盘缓存最大总大小（MB）
	DiskCacheMaxSizeMB int `json:"disk_cache_max_size_mb"`
	// DiskCachePath 磁盘缓存目录
	DiskCachePath string `json:"disk_cache_path"`

	// MonitorEnabled 是否启用性能监控
	MonitorEnabled bool `json:"monitor_enabled"`
	// MonitorCPUThreshold CPU 使用率阈值（%）
	MonitorCPUThreshold int `json:"monitor_cpu_threshold"`
	// MonitorMemoryThreshold 内存使用率阈值（%）
	MonitorMemoryThreshold int `json:"monitor_memory_threshold"`
	// MonitorDiskThreshold 磁盘使用率阈值（%）
	MonitorDiskThreshold int `json:"monitor_disk_threshold"`
}

// 默认配置
var performanceSetting = PerformanceSetting{
	DiskCacheEnabled:     false,
	DiskCacheThresholdMB: 10,   // 超过 10MB 使用磁盘缓存
	DiskCacheMaxSizeMB:   1024, // 最大 1GB 磁盘缓存
	DiskCachePath:        "",   // 空表示使用系统临时目录

	MonitorEnabled:         true,
	MonitorCPUThreshold:    90,
	MonitorMemoryThreshold: 90,
	MonitorDiskThreshold:   95,
}

func (s PerformanceSetting) Validate() error {
	maxMegabytes := int64(math.MaxInt64 >> 20)
	if s.DiskCacheThresholdMB < 0 || int64(s.DiskCacheThresholdMB) > maxMegabytes {
		return fmt.Errorf("disk cache threshold must be in the range [0, %d] MB", maxMegabytes)
	}
	if s.DiskCacheMaxSizeMB < 0 || int64(s.DiskCacheMaxSizeMB) > maxMegabytes {
		return fmt.Errorf("disk cache maximum size must be in the range [0, %d] MB", maxMegabytes)
	}
	if s.DiskCacheEnabled {
		if s.DiskCacheThresholdMB == 0 || s.DiskCacheMaxSizeMB == 0 {
			return fmt.Errorf("enabled disk cache requires positive threshold and maximum size")
		}
		if s.DiskCacheThresholdMB > s.DiskCacheMaxSizeMB {
			return fmt.Errorf("disk cache threshold must not exceed its maximum size")
		}
	}
	for _, threshold := range []struct {
		name  string
		value int
	}{
		{name: "CPU", value: s.MonitorCPUThreshold},
		{name: "memory", value: s.MonitorMemoryThreshold},
		{name: "disk", value: s.MonitorDiskThreshold},
	} {
		if threshold.value < 1 || threshold.value > 100 {
			return fmt.Errorf("%s monitor threshold must be in the range [1, 100]", threshold.name)
		}
	}
	return nil
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("performance_setting", &performanceSetting)
	// 同步初始配置到 common 包
	syncToCommon()
}

// syncToCommon 将配置同步到 common 包
func syncToCommon() {
	setting := GetPerformanceSetting()
	common.SetDiskCacheConfig(common.DiskCacheConfig{
		Enabled:     setting.DiskCacheEnabled,
		ThresholdMB: setting.DiskCacheThresholdMB,
		MaxSizeMB:   setting.DiskCacheMaxSizeMB,
		Path:        setting.DiskCachePath,
	})

	common.SetPerformanceMonitorConfig(common.PerformanceMonitorConfig{
		Enabled:         setting.MonitorEnabled,
		CPUThreshold:    setting.MonitorCPUThreshold,
		MemoryThreshold: setting.MonitorMemoryThreshold,
		DiskThreshold:   setting.MonitorDiskThreshold,
	})
}

// GetPerformanceSetting 获取性能设置
func GetPerformanceSetting() *PerformanceSetting {
	return config.Snapshot[PerformanceSetting]("performance_setting")
}

// UpdateAndSync 更新配置并同步到 common 包
// 当配置从数据库加载后，需要调用此函数同步
func UpdateAndSync() {
	syncToCommon()
}

// GetCacheStats 获取缓存统计信息（代理到 common 包）
func GetCacheStats() common.DiskCacheStats {
	return common.GetDiskCacheStats()
}

// ResetStats 重置统计信息
func ResetStats() {
	common.ResetDiskCacheStats()
}
