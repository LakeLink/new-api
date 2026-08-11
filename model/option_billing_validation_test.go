package model

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	_ "github.com/QuantumNous/new-api/setting/console_setting"
	_ "github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPurgeRemovedPaymentComplianceOptions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))

	originalDB := DB
	DB = db
	t.Cleanup(func() { DB = originalDB })

	require.NoError(t, db.Create([]Option{
		{Key: "payment_setting.compliance_confirmed", Value: "true"},
		{Key: "payment_setting.compliance_confirmed_at", Value: "123"},
		{Key: "payment_setting.amount_options", Value: "[10,20]"},
	}).Error)

	require.NoError(t, purgeRemovedPaymentComplianceOptions())

	var options []Option
	require.NoError(t, db.Order("key").Find(&options).Error)
	require.Len(t, options, 1)
	require.Equal(t, "payment_setting.amount_options", options[0].Key)
}

func TestValidateOptionValueRejectsUnsafeBillingMaps(t *testing.T) {
	for _, key := range []string{
		"ModelPrice", "ModelRatio", "CompletionRatio", "CacheRatio", "CreateCacheRatio",
		"ImageRatio", "AudioRatio", "AudioCompletionRatio",
	} {
		require.Error(t, validateOptionValue(key, `{"model":-0.1}`), key)
		require.Error(t, validateOptionValue(key, `{"model":1e10000}`), key)
		require.NoError(t, validateOptionValue(key, `{"model":0}`), key)
	}
	require.Error(t, validateOptionValue("GroupGroupRatio", `{"vip":{"default":-1}}`))
	require.NoError(t, validateOptionValue("GroupGroupRatio", `{"vip":{"default":0}}`))
	require.Error(t, validateOptionValue("GroupRatio", `null`))
	require.Error(t, validateOptionValue("TopupGroupRatio", `{"default":-1}`))
	require.NoError(t, validateOptionValue("TopupGroupRatio", `{"default":1}`))
}

func TestValidateOptionValueBoundsQuotaConfiguration(t *testing.T) {
	for _, key := range []string{"QuotaForNewUser", "QuotaForInviter", "QuotaForInvitee", "QuotaRemindThreshold", "PreConsumedQuota"} {
		require.Error(t, validateOptionValue(key, "-1"), key)
		require.Error(t, validateOptionValue(key, "2147483648"), key)
		require.NoError(t, validateOptionValue(key, "0"), key)
		require.NoError(t, validateOptionValue(key, strconv.Itoa(common.MaxQuota)), key)
	}
}

func TestValidateOptionValueRejectsUnsafeToolPrices(t *testing.T) {
	require.Error(t, validateOptionValue("tool_price_setting.prices", `{"web_search":-0.01}`))
	require.Error(t, validateOptionValue("tool_price_setting.prices", `{"web_search":1e10000}`))
	require.Error(t, validateOptionValue("tool_price_setting.prices", `null`))
	require.NoError(t, validateOptionValue("tool_price_setting.prices", `{"web_search":0,"file_search":2.5}`))
}

func TestValidateOptionValueRequiresSafeDataExportInterval(t *testing.T) {
	require.Error(t, validateOptionValue("DataExportInterval", "0"))
	require.Error(t, validateOptionValue("DataExportInterval", "-1"))
	require.Error(t, validateOptionValue("DataExportInterval", "not-a-number"))

	maxMinutes := int64(math.MaxInt64 / int64(time.Minute))
	require.Error(t, validateOptionValue("DataExportInterval", strconv.FormatInt(maxMinutes+1, 10)))
	require.NoError(t, validateOptionValue("DataExportInterval", "1"))
	require.NoError(t, validateOptionValue("DataExportInterval", strconv.FormatInt(maxMinutes, 10)))
}

func TestValidateOptionValueRejectsMalformedRegisteredConfig(t *testing.T) {
	require.Error(t, validateOptionValue("performance_setting.monitor_cpu_threshold", "not-a-number"))
	require.Error(t, validateOptionValue("performance_setting.unknown_field", "1"))
	require.NoError(t, validateOptionValue("performance_setting.monitor_cpu_threshold", "90"))
}

func TestUpdateOptionMapLockedDoesNotPublishRejectedRegisteredConfig(t *testing.T) {
	const key = "performance_setting.monitor_cpu_threshold"

	common.OptionMapRWMutex.Lock()
	originalMap := common.OptionMap
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	original, existed := common.OptionMap[key]
	common.OptionMap[key] = "90"
	err := updateOptionMapLocked(key, "not-a-number")
	current := common.OptionMap[key]
	if originalMap == nil {
		common.OptionMap = nil
	} else if existed {
		common.OptionMap[key] = original
	} else {
		delete(common.OptionMap, key)
	}
	common.OptionMapRWMutex.Unlock()

	require.Error(t, err)
	require.Equal(t, "90", current)
}

func TestValidateOptionValueRejectsUnsafeLegacyRuntimeSettings(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{key: "PasswordLoginEnabled", value: "sometimes"},
		{key: "FileUploadPermission", value: "9"},
		{key: "LogExportPermission", value: "1"},
		{key: "SMTPPort", value: "65536"},
		{key: "Price", value: "NaN"},
		{key: "USDExchangeRate", value: "0"},
		{key: "MinTopUp", value: "-1"},
		{key: "ModelRequestRateLimitCount", value: "-1"},
		{key: "ModelRequestRateLimitDurationMinutes", value: "0"},
		{key: "RetryTimes", value: "101"},
		{key: "ChannelDisableThreshold", value: "+Inf"},
		{key: "StreamCacheQueueLength", value: "-1"},
		{key: "DataExportDefaultTime", value: "month"},
		{key: "Chats", value: "null"},
		{key: "AutoGroups", value: `{}`},
		{key: "GroupFallback", value: `{"vip":{"pricing_mode":"invalid"}}`},
		{key: "UserUsableGroups", value: `[]`},
		{key: "ModelRequestRateLimitGroup", value: `{"default":[-1,1]}`},
		{key: "AutomaticRetryStatusCodes", value: "700"},
		{key: "PayMethods", value: "{}"},
		{key: "CreemProducts", value: "{}"},
	}
	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			require.Error(t, validateOptionValue(test.key, test.value))
		})
	}
}

func TestValidateOptionValueAppliesRegisteredConfigSemantics(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{key: "performance_setting.monitor_cpu_threshold", value: "101"},
		{key: "performance_setting.disk_cache_threshold_mb", value: "-1"},
		{key: "perf_metrics_setting.flush_interval", value: "0"},
		{key: "perf_metrics_setting.bucket_time", value: "second"},
		{key: "general_setting.quota_display_type", value: "BITCOIN"},
		{key: "general_setting.custom_currency_exchange_rate", value: "0"},
		{key: "payment_setting.amount_options", value: `[0]`},
		{key: "payment_setting.amount_discount", value: `{"10":1.1}`},
		{key: "checkin_setting.min_quota", value: "-1"},
		{key: "monitor_setting.channel_test_mode", value: "busy_loop"},
		{key: "token_setting.max_user_tokens", value: "-1"},
		{key: "channel_affinity_setting.max_entries", value: "1000001"},
		{
			key: "channel_affinity_setting.rules",
			value: `[{
				"name":"broken",
				"model_regex":["["],
				"key_sources":[{"type":"context_string","key":"id"}]
			}]`,
		},
		{key: "billing_setting.billing_mode", value: `{"model":"invalid"}`},
		{key: "billing_setting.billing_expr", value: `{"model":"p *"}`},
		{key: "fetch_setting.domain_list", value: `["https://example.com"]`},
		{key: "fetch_setting.ip_list", value: `["not-a-cidr"]`},
		{key: "fetch_setting.allowed_ports", value: `["70000"]`},
		{key: "theme.frontend", value: "unknown"},
		{key: "passkey.user_verification", value: "sometimes"},
		{key: "passkey.attachment_preference", value: "internal"},
		{key: "passkey.origins", value: "http://example.com/path"},
		{key: "grok.violation_deduction_amount", value: "-0.1"},
		{key: "claude.default_max_tokens", value: `{"default":0}`},
		{key: "gemini.thinking_adapter_budget_tokens_percentage", value: "1.1"},
		{key: "console_setting.api_info", value: "not-json"},
	}
	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			require.Error(t, validateOptionValue(test.key, test.value))
		})
	}
}
