package common

import (
	"strconv"
	"strings"
)

// GetOptionValue returns a stable copy of one runtime option. OptionMap is the
// authoritative hot-reload snapshot; callers should use these accessors
// instead of reading legacy mutable package globals on request paths.
func GetOptionValue(key string) (string, bool) {
	OptionMapRWMutex.RLock()
	value, ok := OptionMap[key]
	OptionMapRWMutex.RUnlock()
	return value, ok
}

func GetOptionString(key, fallback string) string {
	if value, ok := GetOptionValue(key); ok {
		return value
	}
	return fallback
}

func GetOptionBool(key string, fallback bool) bool {
	value, ok := GetOptionValue(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func GetOptionInt(key string, fallback int) int {
	value, ok := GetOptionValue(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func GetOptionFloat64(key string, fallback float64) float64 {
	value, ok := GetOptionValue(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

// Legacy option accessors read the old package-level fallback while holding
// the same lock used by model.updateOptionMap. They allow request paths to stay
// race-free while compatibility globals remain available to older callers.
func GetLegacyOptionString(key string, fallback *string) string {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	if value, ok := OptionMap[key]; ok {
		return value
	}
	if fallback == nil {
		return ""
	}
	return *fallback
}

func GetLegacyOptionBool(key string, fallback *bool) bool {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	if value, ok := OptionMap[key]; ok {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return fallback != nil && *fallback
}

func GetLegacyOptionInt(key string, fallback *int) int {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	if value, ok := OptionMap[key]; ok {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	if fallback == nil {
		return 0
	}
	return *fallback
}

func GetLegacyOptionFloat64(key string, fallback *float64) float64 {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	if value, ok := OptionMap[key]; ok {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return parsed
		}
	}
	if fallback == nil {
		return 0
	}
	return *fallback
}

func GetEmailDomainWhitelist() []string {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	if value, ok := OptionMap["EmailDomainWhitelist"]; ok {
		if value == "" {
			return nil
		}
		return strings.Split(value, ",")
	}
	return append([]string(nil), EmailDomainWhitelist...)
}

func CurrentQuotaPerUnit() float64 {
	return GetLegacyOptionFloat64("QuotaPerUnit", &QuotaPerUnit)
}
