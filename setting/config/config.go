package config

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
)

type configEntry struct {
	source   interface{}
	snapshot atomic.Value
}

// semanticValidator lets a registered config enforce cross-field and
// point-of-use constraints after parsing but before publication.
type semanticValidator interface {
	Validate() error
}

// ConfigManager 统一管理所有配置
type ConfigManager struct {
	configs map[string]*configEntry
	mutex   sync.RWMutex
}

var GlobalConfig = NewConfigManager()

func NewConfigManager() *ConfigManager {
	return &ConfigManager{
		configs: make(map[string]*configEntry),
	}
}

// Register 注册一个配置模块
func (cm *ConfigManager) Register(name string, config interface{}) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	if err := validateSemantics(config); err != nil {
		panic(fmt.Sprintf("failed to register config %q: %v", name, err))
	}
	entry := &configEntry{source: config}
	snapshot, err := cloneConfig(config)
	if err != nil {
		panic(fmt.Sprintf("failed to register config %q: %v", name, err))
	}
	entry.snapshot.Store(snapshot)
	cm.configs[name] = entry
}

// Get 获取指定配置模块
func (cm *ConfigManager) Get(name string) interface{} {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	entry := cm.configs[name]
	if entry == nil {
		return nil
	}
	return entry.source
}

// Snapshot returns the immutable snapshot currently published for a registered
// config. Runtime updates build a new snapshot and atomically replace the old
// one, so callers never observe reflection updating a struct field-by-field.
func Snapshot[T any](name string) *T {
	GlobalConfig.mutex.RLock()
	entry := GlobalConfig.configs[name]
	GlobalConfig.mutex.RUnlock()
	if entry == nil {
		return nil
	}
	snapshot, _ := entry.snapshot.Load().(*T)
	return snapshot
}

func cloneConfig(config interface{}) (interface{}, error) {
	value := reflect.ValueOf(config)
	if !value.IsValid() || value.Kind() != reflect.Ptr || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("config must be a non-nil pointer to a struct")
	}

	encoded, err := common.Marshal(config)
	if err != nil {
		return nil, err
	}
	snapshot := reflect.New(value.Elem().Type()).Interface()
	if err := common.Unmarshal(encoded, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (cm *ConfigManager) publishLocked(entry *configEntry) error {
	if err := validateSemantics(entry.source); err != nil {
		return err
	}
	snapshot, err := cloneConfig(entry.source)
	if err != nil {
		return err
	}
	entry.snapshot.Store(snapshot)
	return nil
}

func validateSemantics(config interface{}) error {
	if validator, ok := config.(semanticValidator); ok {
		if err := validator.Validate(); err != nil {
			return fmt.Errorf("configuration validation failed: %w", err)
		}
	}
	return nil
}

// LoadFromDB 从数据库加载配置
func (cm *ConfigManager) LoadFromDB(options map[string]string) error {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	for name, entry := range cm.configs {
		prefix := name + "."
		configMap := make(map[string]string)

		// 收集属于此配置的所有选项
		for key, value := range options {
			if strings.HasPrefix(key, prefix) {
				configKey := strings.TrimPrefix(key, prefix)
				configMap[configKey] = value
			}
		}

		// 如果找到配置项，则更新配置
		if len(configMap) > 0 {
			previous, err := cloneConfig(entry.source)
			if err != nil {
				return fmt.Errorf("failed to snapshot config %s: %w", name, err)
			}
			if err := updateConfigFromMap(entry.source, configMap, false); err != nil {
				common.SysError("failed to update config " + name + ": " + err.Error())
				continue
			}
			if err := cm.publishLocked(entry); err != nil {
				reflect.ValueOf(entry.source).Elem().Set(reflect.ValueOf(previous).Elem())
				return fmt.Errorf("failed to publish config %s: %w", name, err)
			}
		}
	}

	return nil
}

// SaveToDB 将配置保存到数据库
func (cm *ConfigManager) SaveToDB(updateFunc func(key, value string) error) error {
	cm.mutex.RLock()
	configs := make(map[string]map[string]string, len(cm.configs))
	for name, entry := range cm.configs {
		configMap, err := configToMap(entry.source)
		if err != nil {
			cm.mutex.RUnlock()
			return err
		}
		configs[name] = configMap
	}
	cm.mutex.RUnlock()

	for name, configMap := range configs {
		for key, value := range configMap {
			dbKey := name + "." + key
			if err := updateFunc(dbKey, value); err != nil {
				return err
			}
		}
	}

	return nil
}

// 辅助函数：将配置对象转换为map
func configToMap(config interface{}) (map[string]string, error) {
	result := make(map[string]string)

	val := reflect.ValueOf(config)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return nil, nil
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// 跳过未导出字段
		if !fieldType.IsExported() {
			continue
		}

		// 获取json标签作为键名
		key := strings.Split(fieldType.Tag.Get("json"), ",")[0]
		if key == "" {
			key = fieldType.Name
		}
		if key == "-" {
			continue
		}

		// 处理不同类型的字段
		var strValue string
		switch field.Kind() {
		case reflect.String:
			strValue = field.String()
		case reflect.Bool:
			strValue = strconv.FormatBool(field.Bool())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			strValue = strconv.FormatInt(field.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			strValue = strconv.FormatUint(field.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			strValue = strconv.FormatFloat(field.Float(), 'f', -1, 64)
		case reflect.Ptr:
			// 处理指针类型：如果非 nil，序列化指向的值
			if !field.IsNil() {
				bytes, err := common.Marshal(field.Interface())
				if err != nil {
					return nil, err
				}
				strValue = string(bytes)
			} else {
				// nil 指针序列化为 "null"
				strValue = "null"
			}
		case reflect.Map, reflect.Slice, reflect.Struct:
			// 复杂类型使用JSON序列化
			bytes, err := common.Marshal(field.Interface())
			if err != nil {
				return nil, err
			}
			strValue = string(bytes)
		default:
			// 跳过不支持的类型
			continue
		}

		result[key] = strValue
	}

	return result, nil
}

// updateConfigFromMap validates every supplied value against a deep copy and
// only replaces the source after the entire update succeeds. Database loading
// tolerates stale keys from older versions; interactive updates reject them.
func updateConfigFromMap(config interface{}, configMap map[string]string, rejectUnknown bool) error {
	candidate, err := cloneConfig(config)
	if err != nil {
		return err
	}
	val := reflect.ValueOf(candidate).Elem()
	typ := val.Type()
	recognized := make(map[string]struct{}, len(configMap))

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)
		if !fieldType.IsExported() || !field.CanSet() {
			continue
		}

		rawTag := fieldType.Tag.Get("json")
		key := strings.Split(rawTag, ",")[0]
		if key == "" {
			key = fieldType.Name
		}
		if key == "-" {
			continue
		}

		strValue, ok := configMap[key]
		if ok {
			recognized[key] = struct{}{}
		} else if rawTag != "" && rawTag != key {
			// Older versions accidentally persisted the complete JSON tag
			// (for example "channel_ids,omitempty") as the option key.
			strValue, ok = configMap[rawTag]
			if ok {
				recognized[rawTag] = struct{}{}
			}
		}
		if !ok {
			continue
		}

		switch field.Kind() {
		case reflect.String:
			field.SetString(strValue)
		case reflect.Bool:
			boolValue, parseErr := strconv.ParseBool(strValue)
			if parseErr != nil {
				return fmt.Errorf("%s must be a boolean: %w", key, parseErr)
			}
			field.SetBool(boolValue)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			bits := field.Type().Bits()
			intValue, parseErr := strconv.ParseInt(strValue, 10, bits)
			if parseErr != nil {
				// Preserve compatibility with legacy values such as
				// "2.000000", but never truncate a fractional value.
				floatValue, floatErr := strconv.ParseFloat(strValue, 64)
				if floatErr != nil || math.IsNaN(floatValue) || math.IsInf(floatValue, 0) ||
					math.Trunc(floatValue) != floatValue {
					return fmt.Errorf("%s must be an integer", key)
				}
				intValue, parseErr = strconv.ParseInt(
					strconv.FormatFloat(floatValue, 'f', 0, 64),
					10,
					bits,
				)
				if parseErr != nil {
					return fmt.Errorf("%s is outside the supported integer range: %w", key, parseErr)
				}
			}
			field.SetInt(intValue)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			bits := field.Type().Bits()
			uintValue, parseErr := strconv.ParseUint(strValue, 10, bits)
			if parseErr != nil {
				floatValue, floatErr := strconv.ParseFloat(strValue, 64)
				if floatErr != nil || math.IsNaN(floatValue) || math.IsInf(floatValue, 0) ||
					floatValue < 0 || math.Trunc(floatValue) != floatValue {
					return fmt.Errorf("%s must be a non-negative integer", key)
				}
				uintValue, parseErr = strconv.ParseUint(
					strconv.FormatFloat(floatValue, 'f', 0, 64),
					10,
					bits,
				)
				if parseErr != nil {
					return fmt.Errorf("%s is outside the supported unsigned integer range: %w", key, parseErr)
				}
			}
			field.SetUint(uintValue)
		case reflect.Float32, reflect.Float64:
			floatValue, parseErr := strconv.ParseFloat(strValue, field.Type().Bits())
			if parseErr != nil || math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
				return fmt.Errorf("%s must be a finite number", key)
			}
			field.SetFloat(floatValue)
		case reflect.Ptr:
			if strings.TrimSpace(strValue) == "null" {
				field.Set(reflect.Zero(field.Type()))
				continue
			}
			fresh := reflect.New(field.Type().Elem())
			if parseErr := common.UnmarshalJsonStr(strValue, fresh.Interface()); parseErr != nil {
				return fmt.Errorf("%s contains invalid JSON: %w", key, parseErr)
			}
			field.Set(fresh)
		case reflect.Map, reflect.Slice, reflect.Struct:
			fresh := reflect.New(field.Type())
			if parseErr := common.UnmarshalJsonStr(strValue, fresh.Interface()); parseErr != nil {
				return fmt.Errorf("%s contains invalid JSON: %w", key, parseErr)
			}
			field.Set(fresh.Elem())
		default:
			return fmt.Errorf("%s has unsupported configuration type %s", key, field.Kind())
		}
	}

	if rejectUnknown {
		for key := range configMap {
			if _, ok := recognized[key]; !ok {
				return fmt.Errorf("unknown configuration key %q", key)
			}
		}
	}

	if err := validateSemantics(candidate); err != nil {
		return err
	}
	// Confirm the candidate is publishable before changing the live source.
	if _, err := cloneConfig(candidate); err != nil {
		return err
	}
	reflect.ValueOf(config).Elem().Set(val)
	return nil
}

// ConfigToMap 将配置对象转换为map（导出函数）
func ConfigToMap(config interface{}) (map[string]string, error) {
	GlobalConfig.mutex.RLock()
	defer GlobalConfig.mutex.RUnlock()
	return configToMap(config)
}

// UpdateConfigFromMap 从map更新配置对象（导出函数）
func UpdateConfigFromMap(config interface{}, configMap map[string]string) error {
	GlobalConfig.mutex.Lock()
	defer GlobalConfig.mutex.Unlock()

	previous, err := cloneConfig(config)
	if err != nil {
		return err
	}
	if err := updateConfigFromMap(config, configMap, true); err != nil {
		return err
	}
	for _, entry := range GlobalConfig.configs {
		if entry.source == config {
			if err := GlobalConfig.publishLocked(entry); err != nil {
				reflect.ValueOf(config).Elem().Set(reflect.ValueOf(previous).Elem())
				return err
			}
			return nil
		}
	}
	return nil
}

// ValidateConfigFromMap checks a prospective registered-config update without
// mutating the source or its published snapshot.
func ValidateConfigFromMap(config interface{}, configMap map[string]string) error {
	GlobalConfig.mutex.RLock()
	defer GlobalConfig.mutex.RUnlock()

	candidate, err := cloneConfig(config)
	if err != nil {
		return err
	}
	return updateConfigFromMap(candidate, configMap, true)
}

// Mutate updates a registered config through a caller-provided operation and
// publishes a replacement snapshot before releasing the write lock.
func Mutate(config interface{}, mutate func()) error {
	GlobalConfig.mutex.Lock()
	defer GlobalConfig.mutex.Unlock()

	previous, err := cloneConfig(config)
	if err != nil {
		return err
	}
	mutate()
	restore := func() {
		reflect.ValueOf(config).Elem().Set(reflect.ValueOf(previous).Elem())
	}
	if err := validateSemantics(config); err != nil {
		restore()
		return err
	}
	for _, entry := range GlobalConfig.configs {
		if entry.source == config {
			if err := GlobalConfig.publishLocked(entry); err != nil {
				restore()
				return err
			}
			return nil
		}
	}
	return nil
}

// ExportAllConfigs 导出所有已注册的配置为扁平结构
func (cm *ConfigManager) ExportAllConfigs() map[string]string {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	result := make(map[string]string)

	for name, entry := range cm.configs {
		configMap, err := configToMap(entry.source)
		if err != nil {
			continue
		}

		// 使用 "模块名.配置项" 的格式添加到结果中
		for key, value := range configMap {
			result[name+"."+key] = value
		}
	}

	return result
}
