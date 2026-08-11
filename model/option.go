package model

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"gorm.io/gorm"
)

type Option struct {
	Key   string `json:"key" gorm:"primaryKey"`
	Value string `json:"value"`
}

var optionUpdateMutex sync.Mutex

const removedPaymentComplianceOptionPrefix = "payment_setting.compliance_"

func purgeRemovedPaymentComplianceOptions() error {
	return DB.
		Where(commonKeyCol+" LIKE ?", removedPaymentComplianceOptionPrefix+"%").
		Delete(&Option{}).
		Error
}

func AllOption() ([]*Option, error) {
	var options []*Option
	var err error
	err = DB.Find(&options).Error
	return options, err
}

func InitOptionMap() {
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)

	// 添加原有的系统配置
	common.OptionMap["FileUploadPermission"] = strconv.Itoa(common.FileUploadPermission)
	common.OptionMap["FileDownloadPermission"] = strconv.Itoa(common.FileDownloadPermission)
	common.OptionMap["ImageUploadPermission"] = strconv.Itoa(common.ImageUploadPermission)
	common.OptionMap["ImageDownloadPermission"] = strconv.Itoa(common.ImageDownloadPermission)
	common.OptionMap["LogExportPermission"] = strconv.Itoa(common.LogExportPermission)
	common.OptionMap["PasswordLoginEnabled"] = strconv.FormatBool(common.PasswordLoginEnabled)
	common.OptionMap["PasswordRegisterEnabled"] = strconv.FormatBool(common.PasswordRegisterEnabled)
	common.OptionMap["EmailVerificationEnabled"] = strconv.FormatBool(common.EmailVerificationEnabled)
	common.OptionMap["GitHubOAuthEnabled"] = strconv.FormatBool(common.GitHubOAuthEnabled)
	common.OptionMap["LinuxDOOAuthEnabled"] = strconv.FormatBool(common.LinuxDOOAuthEnabled)
	common.OptionMap["TelegramOAuthEnabled"] = strconv.FormatBool(common.TelegramOAuthEnabled)
	common.OptionMap["WeChatAuthEnabled"] = strconv.FormatBool(common.WeChatAuthEnabled)
	common.OptionMap["TurnstileCheckEnabled"] = strconv.FormatBool(common.TurnstileCheckEnabled)
	common.OptionMap["RegisterEnabled"] = strconv.FormatBool(common.RegisterEnabled)
	common.OptionMap["AutomaticDisableChannelEnabled"] = strconv.FormatBool(common.AutomaticDisableChannelEnabled)
	common.OptionMap["AutomaticEnableChannelEnabled"] = strconv.FormatBool(common.AutomaticEnableChannelEnabled)
	common.OptionMap["LogConsumeEnabled"] = strconv.FormatBool(common.LogConsumeEnabled)
	common.OptionMap["DisplayInCurrencyEnabled"] = strconv.FormatBool(common.DisplayInCurrencyEnabled)
	common.OptionMap["DisplayTokenStatEnabled"] = strconv.FormatBool(common.DisplayTokenStatEnabled)
	common.OptionMap["DrawingEnabled"] = strconv.FormatBool(common.DrawingEnabled)
	common.OptionMap["TaskEnabled"] = strconv.FormatBool(common.TaskEnabled)
	common.OptionMap["DataExportEnabled"] = strconv.FormatBool(common.DataExportEnabled)
	common.OptionMap["ChannelDisableThreshold"] = strconv.FormatFloat(common.ChannelDisableThreshold, 'f', -1, 64)
	common.OptionMap["EmailDomainRestrictionEnabled"] = strconv.FormatBool(common.EmailDomainRestrictionEnabled)
	common.OptionMap["EmailAliasRestrictionEnabled"] = strconv.FormatBool(common.EmailAliasRestrictionEnabled)
	common.OptionMap["EmailDomainWhitelist"] = strings.Join(common.EmailDomainWhitelist, ",")
	common.OptionMap["SMTPServer"] = ""
	common.OptionMap["SMTPFrom"] = ""
	common.OptionMap["SMTPPort"] = strconv.Itoa(common.SMTPPort)
	common.OptionMap["SMTPAccount"] = ""
	common.OptionMap["SMTPToken"] = ""
	common.OptionMap["SMTPSSLEnabled"] = strconv.FormatBool(common.SMTPSSLEnabled)
	common.OptionMap["SMTPStartTLSEnabled"] = strconv.FormatBool(common.SMTPStartTLSEnabled)
	common.OptionMap["SMTPInsecureSkipVerify"] = strconv.FormatBool(common.SMTPInsecureSkipVerify)
	common.OptionMap["SMTPForceAuthLogin"] = strconv.FormatBool(common.SMTPForceAuthLogin)
	common.OptionMap["Notice"] = ""
	common.OptionMap["About"] = ""
	common.OptionMap["HomePageContent"] = ""
	common.OptionMap["Footer"] = common.Footer
	common.OptionMap["SystemName"] = common.SystemName
	common.OptionMap["Logo"] = common.Logo
	common.OptionMap["ServerAddress"] = ""
	common.OptionMap["WorkerUrl"] = system_setting.WorkerUrl
	common.OptionMap["WorkerValidKey"] = system_setting.WorkerValidKey
	common.OptionMap["WorkerAllowHttpImageRequestEnabled"] = strconv.FormatBool(system_setting.WorkerAllowHttpImageRequestEnabled)
	common.OptionMap["PayAddress"] = ""
	common.OptionMap["CustomCallbackAddress"] = ""
	common.OptionMap["EpayId"] = ""
	common.OptionMap["EpayKey"] = ""
	common.OptionMap["Price"] = strconv.FormatFloat(operation_setting.Price, 'f', -1, 64)
	common.OptionMap["USDExchangeRate"] = strconv.FormatFloat(operation_setting.USDExchangeRate, 'f', -1, 64)
	common.OptionMap["MinTopUp"] = strconv.Itoa(operation_setting.MinTopUp)
	common.OptionMap["StripeMinTopUp"] = strconv.Itoa(setting.StripeMinTopUp)
	common.OptionMap["StripeApiSecret"] = setting.StripeApiSecret
	common.OptionMap["StripeWebhookSecret"] = setting.StripeWebhookSecret
	common.OptionMap["StripePriceId"] = setting.StripePriceId
	common.OptionMap["StripeUnitPrice"] = strconv.FormatFloat(setting.StripeUnitPrice, 'f', -1, 64)
	common.OptionMap["StripePromotionCodesEnabled"] = strconv.FormatBool(setting.StripePromotionCodesEnabled)
	common.OptionMap["CreemApiKey"] = setting.CreemApiKey
	common.OptionMap["CreemProducts"] = setting.CreemProducts
	common.OptionMap["CreemTestMode"] = strconv.FormatBool(setting.CreemTestMode)
	common.OptionMap["CreemWebhookSecret"] = setting.CreemWebhookSecret
	common.OptionMap["WaffoEnabled"] = strconv.FormatBool(setting.WaffoEnabled)
	common.OptionMap["WaffoApiKey"] = setting.WaffoApiKey
	common.OptionMap["WaffoPrivateKey"] = setting.WaffoPrivateKey
	common.OptionMap["WaffoPublicCert"] = setting.WaffoPublicCert
	common.OptionMap["WaffoSandboxPublicCert"] = setting.WaffoSandboxPublicCert
	common.OptionMap["WaffoSandboxApiKey"] = setting.WaffoSandboxApiKey
	common.OptionMap["WaffoSandboxPrivateKey"] = setting.WaffoSandboxPrivateKey
	common.OptionMap["WaffoSandbox"] = strconv.FormatBool(setting.WaffoSandbox)
	common.OptionMap["WaffoMerchantId"] = setting.WaffoMerchantId
	common.OptionMap["WaffoNotifyUrl"] = setting.WaffoNotifyUrl
	common.OptionMap["WaffoReturnUrl"] = setting.WaffoReturnUrl
	common.OptionMap["WaffoSubscriptionReturnUrl"] = setting.WaffoSubscriptionReturnUrl
	common.OptionMap["WaffoCurrency"] = setting.WaffoCurrency
	common.OptionMap["WaffoUnitPrice"] = strconv.FormatFloat(setting.WaffoUnitPrice, 'f', -1, 64)
	common.OptionMap["WaffoMinTopUp"] = strconv.Itoa(setting.WaffoMinTopUp)
	common.OptionMap["WaffoPayMethods"] = setting.WaffoPayMethods2JsonString()
	common.OptionMap["WaffoPancakeMerchantID"] = setting.WaffoPancakeMerchantID
	common.OptionMap["WaffoPancakePrivateKey"] = setting.WaffoPancakePrivateKey
	common.OptionMap["WaffoPancakeReturnURL"] = setting.WaffoPancakeReturnURL
	common.OptionMap["WaffoPancakeUnitPrice"] = strconv.FormatFloat(setting.WaffoPancakeUnitPrice, 'f', -1, 64)
	common.OptionMap["WaffoPancakeMinTopUp"] = strconv.Itoa(setting.WaffoPancakeMinTopUp)
	common.OptionMap["WaffoPancakeStoreID"] = setting.WaffoPancakeStoreID
	common.OptionMap["WaffoPancakeProductID"] = setting.WaffoPancakeProductID
	common.OptionMap["TopupGroupRatio"] = common.TopupGroupRatio2JSONString()
	common.OptionMap["Chats"] = setting.Chats2JsonString()
	common.OptionMap["AutoGroups"] = setting.AutoGroups2JsonString()
	common.OptionMap["DefaultUseAutoGroup"] = strconv.FormatBool(setting.DefaultUseAutoGroup)
	common.OptionMap["PayMethods"] = operation_setting.PayMethods2JsonString()
	common.OptionMap["GitHubClientId"] = ""
	common.OptionMap["GitHubClientSecret"] = ""
	common.OptionMap["TelegramBotToken"] = ""
	common.OptionMap["TelegramBotName"] = ""
	common.OptionMap["WeChatServerAddress"] = ""
	common.OptionMap["WeChatServerToken"] = ""
	common.OptionMap["WeChatAccountQRCodeImageURL"] = ""
	common.OptionMap["TurnstileSiteKey"] = ""
	common.OptionMap["TurnstileSecretKey"] = ""
	common.OptionMap["QuotaForNewUser"] = strconv.Itoa(common.QuotaForNewUser)
	common.OptionMap["QuotaForInviter"] = strconv.Itoa(common.QuotaForInviter)
	common.OptionMap["QuotaForInvitee"] = strconv.Itoa(common.QuotaForInvitee)
	common.OptionMap["QuotaRemindThreshold"] = strconv.Itoa(common.QuotaRemindThreshold)
	common.OptionMap["PreConsumedQuota"] = strconv.Itoa(common.PreConsumedQuota)
	common.OptionMap["ModelRequestRateLimitCount"] = strconv.Itoa(setting.ModelRequestRateLimitCount)
	common.OptionMap["ModelRequestRateLimitDurationMinutes"] = strconv.Itoa(setting.ModelRequestRateLimitDurationMinutes)
	common.OptionMap["ModelRequestRateLimitSuccessCount"] = strconv.Itoa(setting.ModelRequestRateLimitSuccessCount)
	common.OptionMap["ModelRequestRateLimitGroup"] = setting.ModelRequestRateLimitGroup2JSONString()
	common.OptionMap["ModelRatio"] = ratio_setting.ModelRatio2JSONString()
	common.OptionMap["ModelPrice"] = ratio_setting.ModelPrice2JSONString()
	common.OptionMap["CacheRatio"] = ratio_setting.CacheRatio2JSONString()
	common.OptionMap["CreateCacheRatio"] = ratio_setting.CreateCacheRatio2JSONString()
	common.OptionMap["GroupRatio"] = ratio_setting.GroupRatio2JSONString()
	common.OptionMap["GroupGroupRatio"] = ratio_setting.GroupGroupRatio2JSONString()
	common.OptionMap["GroupFallback"] = setting.GroupFallback2JSONString()
	common.OptionMap["UserUsableGroups"] = setting.UserUsableGroups2JSONString()
	common.OptionMap["CompletionRatio"] = ratio_setting.CompletionRatio2JSONString()
	common.OptionMap["ImageRatio"] = ratio_setting.ImageRatio2JSONString()
	common.OptionMap["AudioRatio"] = ratio_setting.AudioRatio2JSONString()
	common.OptionMap["AudioCompletionRatio"] = ratio_setting.AudioCompletionRatio2JSONString()
	common.OptionMap["TopUpLink"] = common.TopUpLink
	//common.OptionMap["ChatLink"] = common.ChatLink
	//common.OptionMap["ChatLink2"] = common.ChatLink2
	common.OptionMap["QuotaPerUnit"] = strconv.FormatFloat(common.QuotaPerUnit, 'f', -1, 64)
	common.OptionMap["RetryTimes"] = strconv.Itoa(common.RetryTimes)
	common.OptionMap["DataExportInterval"] = strconv.Itoa(common.DataExportInterval)
	common.OptionMap["DataExportDefaultTime"] = common.DataExportDefaultTime
	common.OptionMap["DefaultCollapseSidebar"] = strconv.FormatBool(common.DefaultCollapseSidebar)
	common.OptionMap["MjNotifyEnabled"] = strconv.FormatBool(setting.MjNotifyEnabled)
	common.OptionMap["MjAccountFilterEnabled"] = strconv.FormatBool(setting.MjAccountFilterEnabled)
	common.OptionMap["MjModeClearEnabled"] = strconv.FormatBool(setting.MjModeClearEnabled)
	common.OptionMap["MjForwardUrlEnabled"] = strconv.FormatBool(setting.MjForwardUrlEnabled)
	common.OptionMap["MjActionCheckSuccessEnabled"] = strconv.FormatBool(setting.MjActionCheckSuccessEnabled)
	common.OptionMap["CheckSensitiveEnabled"] = strconv.FormatBool(setting.CheckSensitiveEnabled)
	common.OptionMap["DemoSiteEnabled"] = strconv.FormatBool(operation_setting.DemoSiteEnabled)
	common.OptionMap["SelfUseModeEnabled"] = strconv.FormatBool(operation_setting.SelfUseModeEnabled)
	common.OptionMap["ModelRequestRateLimitEnabled"] = strconv.FormatBool(setting.ModelRequestRateLimitEnabled)
	common.OptionMap["CheckSensitiveOnPromptEnabled"] = strconv.FormatBool(setting.CheckSensitiveOnPromptEnabled)
	common.OptionMap["StopOnSensitiveEnabled"] = strconv.FormatBool(setting.StopOnSensitiveEnabled)
	common.OptionMap["SensitiveWords"] = setting.SensitiveWordsToString()
	common.OptionMap["StreamCacheQueueLength"] = strconv.Itoa(setting.StreamCacheQueueLength)
	common.OptionMap["AutomaticDisableKeywords"] = operation_setting.AutomaticDisableKeywordsToString()
	common.OptionMap["AutomaticDisableStatusCodes"] = operation_setting.AutomaticDisableStatusCodesToString()
	common.OptionMap["AutomaticRetryStatusCodes"] = operation_setting.AutomaticRetryStatusCodesToString()
	common.OptionMap["ExposeRatioEnabled"] = strconv.FormatBool(ratio_setting.IsExposeRatioEnabled())

	// 自动添加所有注册的模型配置
	modelConfigs := config.GlobalConfig.ExportAllConfigs()
	for k, v := range modelConfigs {
		common.OptionMap[k] = v
	}

	common.OptionMapRWMutex.Unlock()
	loadOptionsFromDatabase()
}

func loadOptionsFromDatabase() {
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()

	if err := purgeRemovedPaymentComplianceOptions(); err != nil {
		common.SysError("failed to purge removed payment compliance options: " + err.Error())
		return
	}

	options, err := AllOption()
	if err != nil {
		common.SysError("failed to load options from database: " + err.Error())
		return
	}

	type registeredConfigUpdate struct {
		config interface{}
		fields map[string]string
		values map[string]string
	}
	configUpdates := make(map[string]*registeredConfigUpdate)
	legacyValues := make(map[string]string)
	for _, option := range options {
		if parts := strings.SplitN(option.Key, ".", 2); len(parts) == 2 {
			if cfg := config.GlobalConfig.Get(parts[0]); cfg != nil {
				update := configUpdates[parts[0]]
				if update == nil {
					update = &registeredConfigUpdate{
						config: cfg,
						fields: make(map[string]string),
						values: make(map[string]string),
					}
					configUpdates[parts[0]] = update
				}
				update.fields[parts[1]] = option.Value
				update.values[option.Key] = option.Value
				continue
			}
		}
		if err := validateOptionValue(option.Key, option.Value); err != nil {
			common.SysLog("failed to update option map: " + err.Error())
			continue
		}
		legacyValues[option.Key] = option.Value
	}

	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}

	configNames := make([]string, 0, len(configUpdates))
	for name := range configUpdates {
		configNames = append(configNames, name)
	}
	sort.Strings(configNames)
	for _, name := range configNames {
		update := configUpdates[name]
		if err := config.ValidateConfigFromMap(update.config, update.fields); err != nil {
			common.SysLog("failed to update option map: invalid " + name + " configuration: " + err.Error())
			continue
		}
		if err := config.UpdateConfigFromMap(update.config, update.fields); err != nil {
			common.SysLog("failed to update option map: " + err.Error())
			continue
		}
		afterConfigUpdate(name)
		for key, value := range update.values {
			common.OptionMap[key] = value
		}
	}

	legacyKeys := make([]string, 0, len(legacyValues))
	for key := range legacyValues {
		legacyKeys = append(legacyKeys, key)
	}
	sort.Strings(legacyKeys)
	for _, key := range legacyKeys {
		if err := updateOptionMapLocked(key, legacyValues[key]); err != nil {
			common.SysLog("failed to update option map: " + err.Error())
		}
	}
}

func SyncOptions(frequency int) {
	interval := common.SafeIntervalDuration(frequency, time.Second, 60*time.Second, "option sync")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		common.SysLog("syncing options from database")
		loadOptionsFromDatabase()
	}
}

func UpdateOption(key string, value string) error {
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()

	if err := validateOptionValue(key, value); err != nil {
		return err
	}
	// Save to database first
	option := Option{
		Key: key,
	}
	// https://gorm.io/docs/update.html#Save-All-Fields
	if err := DB.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
		return err
	}
	option.Value = value
	// Save is a combination function.
	// If save value does not contain primary key, it will execute Create,
	// otherwise it will execute Update (with all fields).
	if err := DB.Save(&option).Error; err != nil {
		return err
	}
	// Update OptionMap
	return updateOptionMapWithoutSequenceLock(key, value)
}

// UpdateOptionsBulk validates related registered-config fields as one
// candidate, persists every value in a database transaction, then publishes
// each config generation and the legacy option generation under the shared
// option lock.
func UpdateOptionsBulk(values map[string]string) error {
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()

	if len(values) == 0 {
		return nil
	}
	type registeredConfigUpdate struct {
		config interface{}
		fields map[string]string
		values map[string]string
	}
	configUpdates := make(map[string]*registeredConfigUpdate)
	legacyValues := make(map[string]string)
	for key, value := range values {
		if parts := strings.SplitN(key, ".", 2); len(parts) == 2 {
			if cfg := config.GlobalConfig.Get(parts[0]); cfg != nil {
				update := configUpdates[parts[0]]
				if update == nil {
					update = &registeredConfigUpdate{
						config: cfg,
						fields: make(map[string]string),
						values: make(map[string]string),
					}
					configUpdates[parts[0]] = update
				}
				update.fields[parts[1]] = value
				update.values[key] = value
				continue
			}
		}
		if err := validateOptionValue(key, value); err != nil {
			return err
		}
		legacyValues[key] = value
	}
	for name, update := range configUpdates {
		if err := config.ValidateConfigFromMap(update.config, update.fields); err != nil {
			return fmt.Errorf("invalid %s configuration: %w", name, err)
		}
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		for k, v := range values {
			option := Option{Key: k}
			if err := tx.FirstOrCreate(&option, Option{Key: k}).Error; err != nil {
				return err
			}
			option.Value = v
			if err := tx.Save(&option).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}

	configNames := make([]string, 0, len(configUpdates))
	for name := range configUpdates {
		configNames = append(configNames, name)
	}
	sort.Strings(configNames)
	for _, name := range configNames {
		update := configUpdates[name]
		if err := config.UpdateConfigFromMap(update.config, update.fields); err != nil {
			return err
		}
		afterConfigUpdate(name)
		for key, value := range update.values {
			common.OptionMap[key] = value
		}
	}

	legacyKeys := make([]string, 0, len(legacyValues))
	for key := range legacyValues {
		legacyKeys = append(legacyKeys, key)
	}
	sort.Strings(legacyKeys)
	for _, k := range legacyKeys {
		v := legacyValues[k]
		if err := updateOptionMapLocked(k, v); err != nil {
			return err
		}
	}
	return nil
}

func updateOptionMap(key string, value string) (err error) {
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()
	return updateOptionMapWithoutSequenceLock(key, value)
}

func updateOptionMapWithoutSequenceLock(key string, value string) (err error) {
	if err := validateOptionValue(key, value); err != nil {
		return err
	}
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	return updateOptionMapLocked(key, value)
}

// updateOptionMapLocked publishes one already-validated option while the
// caller holds OptionMapRWMutex for writing. Keeping the lock at the batch
// boundary prevents readers from observing mixed generations of related
// credentials and prices during database sync or UpdateOptionsBulk.
func updateOptionMapLocked(key string, value string) (err error) {
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	// 检查是否是模型配置 - 使用更规范的方式处理
	if handled, configErr := handleConfigUpdate(key, value); handled {
		if configErr != nil {
			return configErr
		}
		common.OptionMap[key] = value
		return nil // 已由配置系统处理
	}

	// 处理传统配置项...
	if strings.HasSuffix(key, "Permission") {
		intValue, _ := strconv.Atoi(value)
		switch key {
		case "FileUploadPermission":
			common.FileUploadPermission = intValue
		case "FileDownloadPermission":
			common.FileDownloadPermission = intValue
		case "ImageUploadPermission":
			common.ImageUploadPermission = intValue
		case "ImageDownloadPermission":
			common.ImageDownloadPermission = intValue
		case "LogExportPermission":
			if intValue != common.RoleAdminUser && intValue != common.RoleRootUser {
				intValue = common.RoleAdminUser
				value = strconv.Itoa(intValue)
			}
			common.LogExportPermission = intValue
		}
	}
	if strings.HasSuffix(key, "Enabled") || key == "DefaultCollapseSidebar" || key == "DefaultUseAutoGroup" || key == "SMTPForceAuthLogin" || key == "SMTPInsecureSkipVerify" {
		boolValue := value == "true"
		switch key {
		case "PasswordRegisterEnabled":
			common.PasswordRegisterEnabled = boolValue
		case "PasswordLoginEnabled":
			common.PasswordLoginEnabled = boolValue
		case "EmailVerificationEnabled":
			common.EmailVerificationEnabled = boolValue
		case "GitHubOAuthEnabled":
			common.GitHubOAuthEnabled = boolValue
		case "LinuxDOOAuthEnabled":
			common.LinuxDOOAuthEnabled = boolValue
		case "WeChatAuthEnabled":
			common.WeChatAuthEnabled = boolValue
		case "TelegramOAuthEnabled":
			common.TelegramOAuthEnabled = boolValue
		case "TurnstileCheckEnabled":
			common.TurnstileCheckEnabled = boolValue
		case "RegisterEnabled":
			common.RegisterEnabled = boolValue
		case "EmailDomainRestrictionEnabled":
			common.EmailDomainRestrictionEnabled = boolValue
		case "EmailAliasRestrictionEnabled":
			common.EmailAliasRestrictionEnabled = boolValue
		case "AutomaticDisableChannelEnabled":
			common.AutomaticDisableChannelEnabled = boolValue
		case "AutomaticEnableChannelEnabled":
			common.AutomaticEnableChannelEnabled = boolValue
		case "LogConsumeEnabled":
			common.LogConsumeEnabled = boolValue
		case "DisplayInCurrencyEnabled":
			// 兼容旧字段：同步到新配置 general_setting.quota_display_type（运行时生效）
			// true -> USD, false -> TOKENS
			newVal := "USD"
			if !boolValue {
				newVal = "TOKENS"
			}
			if cfg := config.GlobalConfig.Get("general_setting"); cfg != nil {
				err = config.UpdateConfigFromMap(cfg, map[string]string{"quota_display_type": newVal})
			}
		case "DisplayTokenStatEnabled":
			common.DisplayTokenStatEnabled = boolValue
		case "DrawingEnabled":
			common.DrawingEnabled = boolValue
		case "TaskEnabled":
			common.TaskEnabled = boolValue
		case "DataExportEnabled":
			common.DataExportEnabled = boolValue
		case "DefaultCollapseSidebar":
			common.DefaultCollapseSidebar = boolValue
		case "MjNotifyEnabled":
			setting.MjNotifyEnabled = boolValue
		case "MjAccountFilterEnabled":
			setting.MjAccountFilterEnabled = boolValue
		case "MjModeClearEnabled":
			setting.MjModeClearEnabled = boolValue
		case "MjForwardUrlEnabled":
			setting.MjForwardUrlEnabled = boolValue
		case "MjActionCheckSuccessEnabled":
			setting.MjActionCheckSuccessEnabled = boolValue
		case "CheckSensitiveEnabled":
			setting.CheckSensitiveEnabled = boolValue
		case "DemoSiteEnabled":
			operation_setting.DemoSiteEnabled = boolValue
		case "SelfUseModeEnabled":
			operation_setting.SelfUseModeEnabled = boolValue
		case "CheckSensitiveOnPromptEnabled":
			setting.CheckSensitiveOnPromptEnabled = boolValue
		case "ModelRequestRateLimitEnabled":
			setting.ModelRequestRateLimitEnabled = boolValue
		case "StopOnSensitiveEnabled":
			setting.StopOnSensitiveEnabled = boolValue
		case "SMTPSSLEnabled":
			common.SMTPSSLEnabled = boolValue
		case "SMTPStartTLSEnabled":
			common.SMTPStartTLSEnabled = boolValue
		case "SMTPInsecureSkipVerify":
			common.SMTPInsecureSkipVerify = boolValue
		case "SMTPForceAuthLogin":
			common.SMTPForceAuthLogin = boolValue
		case "WorkerAllowHttpImageRequestEnabled":
			system_setting.WorkerAllowHttpImageRequestEnabled = boolValue
		case "DefaultUseAutoGroup":
			setting.DefaultUseAutoGroup = boolValue
		case "ExposeRatioEnabled":
			ratio_setting.SetExposeRatioEnabled(boolValue)
		}
	}
	switch key {
	case "EmailDomainWhitelist":
		common.EmailDomainWhitelist = strings.Split(value, ",")
	case "SMTPServer":
		common.SMTPServer = value
	case "SMTPPort":
		intValue, _ := strconv.Atoi(value)
		common.SMTPPort = intValue
	case "SMTPAccount":
		common.SMTPAccount = value
	case "SMTPFrom":
		common.SMTPFrom = value
	case "SMTPToken":
		common.SMTPToken = value
	case "ServerAddress":
		system_setting.ServerAddress = value
	case "WorkerUrl":
		system_setting.WorkerUrl = value
	case "WorkerValidKey":
		system_setting.WorkerValidKey = value
	case "PayAddress":
		operation_setting.PayAddress = value
	case "Chats":
		err = setting.UpdateChatsByJsonString(value)
	case "AutoGroups":
		err = setting.UpdateAutoGroupsByJsonString(value)
	case "GroupFallback":
		err = setting.UpdateGroupFallbackByJsonString(value)
	case "CustomCallbackAddress":
		operation_setting.CustomCallbackAddress = value
	case "EpayId":
		operation_setting.EpayId = value
	case "EpayKey":
		operation_setting.EpayKey = value
	case "Price":
		operation_setting.Price, _ = strconv.ParseFloat(value, 64)
	case "USDExchangeRate":
		operation_setting.USDExchangeRate, _ = strconv.ParseFloat(value, 64)
	case "MinTopUp":
		operation_setting.MinTopUp, _ = strconv.Atoi(value)
	case "StripeApiSecret":
		setting.StripeApiSecret = value
	case "StripeWebhookSecret":
		setting.StripeWebhookSecret = value
	case "StripePriceId":
		setting.StripePriceId = value
	case "StripeUnitPrice":
		setting.StripeUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "StripeMinTopUp":
		setting.StripeMinTopUp, _ = strconv.Atoi(value)
	case "StripePromotionCodesEnabled":
		setting.StripePromotionCodesEnabled = value == "true"
	case "CreemApiKey":
		setting.CreemApiKey = value
	case "CreemProducts":
		setting.CreemProducts = value
	case "CreemTestMode":
		setting.CreemTestMode = value == "true"
	case "CreemWebhookSecret":
		setting.CreemWebhookSecret = value
	case "WaffoEnabled":
		setting.WaffoEnabled = value == "true"
	case "WaffoApiKey":
		setting.WaffoApiKey = value
	case "WaffoPrivateKey":
		setting.WaffoPrivateKey = value
	case "WaffoPublicCert":
		setting.WaffoPublicCert = value
	case "WaffoSandboxPublicCert":
		setting.WaffoSandboxPublicCert = value
	case "WaffoSandboxApiKey":
		setting.WaffoSandboxApiKey = value
	case "WaffoSandboxPrivateKey":
		setting.WaffoSandboxPrivateKey = value
	case "WaffoSandbox":
		setting.WaffoSandbox = value == "true"
	case "WaffoMerchantId":
		setting.WaffoMerchantId = value
	case "WaffoNotifyUrl":
		setting.WaffoNotifyUrl = value
	case "WaffoReturnUrl":
		setting.WaffoReturnUrl = value
	case "WaffoSubscriptionReturnUrl":
		setting.WaffoSubscriptionReturnUrl = value
	case "WaffoCurrency":
		setting.WaffoCurrency = value
	case "WaffoUnitPrice":
		setting.WaffoUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "WaffoMinTopUp":
		setting.WaffoMinTopUp, _ = strconv.Atoi(value)
	case "WaffoPancakeMerchantID":
		setting.WaffoPancakeMerchantID = value
	case "WaffoPancakePrivateKey":
		setting.WaffoPancakePrivateKey = value
	case "WaffoPancakeReturnURL":
		setting.WaffoPancakeReturnURL = value
	case "WaffoPancakeStoreID":
		setting.WaffoPancakeStoreID = value
	case "WaffoPancakeProductID":
		setting.WaffoPancakeProductID = value
	case "WaffoPancakeUnitPrice":
		setting.WaffoPancakeUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "WaffoPancakeMinTopUp":
		setting.WaffoPancakeMinTopUp, _ = strconv.Atoi(value)
	case "TopupGroupRatio":
		err = common.UpdateTopupGroupRatioByJSONString(value)
	case "GitHubClientId":
		common.GitHubClientId = value
	case "GitHubClientSecret":
		common.GitHubClientSecret = value
	case "LinuxDOClientId":
		common.LinuxDOClientId = value
	case "LinuxDOClientSecret":
		common.LinuxDOClientSecret = value
	case "LinuxDOMinimumTrustLevel":
		common.LinuxDOMinimumTrustLevel, _ = strconv.Atoi(value)
	case "Footer":
		common.Footer = value
	case "SystemName":
		common.SystemName = value
	case "Logo":
		common.Logo = value
	case "WeChatServerAddress":
		common.WeChatServerAddress = value
	case "WeChatServerToken":
		common.WeChatServerToken = value
	case "WeChatAccountQRCodeImageURL":
		common.WeChatAccountQRCodeImageURL = value
	case "TelegramBotToken":
		common.TelegramBotToken = value
	case "TelegramBotName":
		common.TelegramBotName = value
	case "TurnstileSiteKey":
		common.TurnstileSiteKey = value
	case "TurnstileSecretKey":
		common.TurnstileSecretKey = value
	case "QuotaForNewUser":
		common.QuotaForNewUser, _ = strconv.Atoi(value)
	case "QuotaForInviter":
		common.QuotaForInviter, _ = strconv.Atoi(value)
	case "QuotaForInvitee":
		common.QuotaForInvitee, _ = strconv.Atoi(value)
	case "QuotaRemindThreshold":
		common.QuotaRemindThreshold, _ = strconv.Atoi(value)
	case "PreConsumedQuota":
		common.PreConsumedQuota, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitCount":
		setting.ModelRequestRateLimitCount, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitDurationMinutes":
		setting.ModelRequestRateLimitDurationMinutes, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitSuccessCount":
		setting.ModelRequestRateLimitSuccessCount, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitGroup":
		err = setting.UpdateModelRequestRateLimitGroupByJSONString(value)
	case "RetryTimes":
		common.RetryTimes, _ = strconv.Atoi(value)
	case "DataExportInterval":
		common.DataExportInterval, _ = strconv.Atoi(value)
	case "DataExportDefaultTime":
		common.DataExportDefaultTime = value
	case "ModelRatio":
		err = ratio_setting.UpdateModelRatioByJSONString(value)
	case "GroupRatio":
		err = ratio_setting.UpdateGroupRatioByJSONString(value)
	case "GroupGroupRatio":
		err = ratio_setting.UpdateGroupGroupRatioByJSONString(value)
	case "UserUsableGroups":
		err = setting.UpdateUserUsableGroupsByJSONString(value)
	case "CompletionRatio":
		err = ratio_setting.UpdateCompletionRatioByJSONString(value)
	case "ModelPrice":
		err = ratio_setting.UpdateModelPriceByJSONString(value)
	case "CacheRatio":
		err = ratio_setting.UpdateCacheRatioByJSONString(value)
	case "CreateCacheRatio":
		err = ratio_setting.UpdateCreateCacheRatioByJSONString(value)
	case "ImageRatio":
		err = ratio_setting.UpdateImageRatioByJSONString(value)
	case "AudioRatio":
		err = ratio_setting.UpdateAudioRatioByJSONString(value)
	case "AudioCompletionRatio":
		err = ratio_setting.UpdateAudioCompletionRatioByJSONString(value)
	case "TopUpLink":
		common.TopUpLink = value
	//case "ChatLink":
	//	common.ChatLink = value
	//case "ChatLink2":
	//	common.ChatLink2 = value
	case "ChannelDisableThreshold":
		common.ChannelDisableThreshold, _ = strconv.ParseFloat(value, 64)
	case "QuotaPerUnit":
		common.QuotaPerUnit, _ = strconv.ParseFloat(value, 64)
	case "SensitiveWords":
		setting.SensitiveWordsFromString(value)
	case "AutomaticDisableKeywords":
		operation_setting.AutomaticDisableKeywordsFromString(value)
	case "AutomaticDisableStatusCodes":
		err = operation_setting.AutomaticDisableStatusCodesFromString(value)
	case "AutomaticRetryStatusCodes":
		err = operation_setting.AutomaticRetryStatusCodesFromString(value)
	case "StreamCacheQueueLength":
		setting.StreamCacheQueueLength, _ = strconv.Atoi(value)
	case "PayMethods":
		err = operation_setting.UpdatePayMethodsByJsonString(value)
	case "WaffoPayMethods":
		// WaffoPayMethods is read directly from OptionMap via setting.GetWaffoPayMethods().
		// No additional in-memory variable is required.
	}
	if err != nil {
		return err
	}
	common.OptionMap[key] = value
	return nil
}

func validateOptionValue(key string, value string) error {
	if parts := strings.SplitN(key, ".", 2); len(parts) == 2 {
		if cfg := config.GlobalConfig.Get(parts[0]); cfg != nil {
			if err := config.ValidateConfigFromMap(cfg, map[string]string{parts[1]: value}); err != nil {
				return fmt.Errorf("invalid %s: %w", key, err)
			}
		}
	}

	if strings.HasSuffix(key, "Enabled") ||
		key == "DefaultCollapseSidebar" ||
		key == "DefaultUseAutoGroup" ||
		key == "SMTPForceAuthLogin" ||
		key == "SMTPInsecureSkipVerify" ||
		key == "CreemTestMode" ||
		key == "WaffoSandbox" {
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("%s must be a boolean", key)
		}
	}

	switch key {
	case "FileUploadPermission", "FileDownloadPermission", "ImageUploadPermission", "ImageDownloadPermission":
		role, err := strconv.Atoi(value)
		if err != nil || !common.IsValidateRole(role) {
			return fmt.Errorf("%s must be a valid role value", key)
		}
	case "LogExportPermission":
		role, err := strconv.Atoi(value)
		if err != nil || (role != common.RoleAdminUser && role != common.RoleRootUser) {
			return fmt.Errorf("LogExportPermission must be admin or root")
		}
	case "SMTPPort":
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("SMTPPort must be an integer in the range [1, 65535]")
		}
	case "Price", "StripeUnitPrice", "WaffoUnitPrice", "WaffoPancakeUnitPrice":
		price, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(price) || math.IsInf(price, 0) || price < 0 || price > float64(common.MaxQuota) {
			return fmt.Errorf("%s must be finite and in the range [0, %d]", key, common.MaxQuota)
		}
	case "USDExchangeRate":
		rate, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 || rate > float64(common.MaxQuota) {
			return fmt.Errorf("USDExchangeRate must be finite and in the range (0, %d]", common.MaxQuota)
		}
	case "MinTopUp", "StripeMinTopUp", "WaffoMinTopUp", "WaffoPancakeMinTopUp":
		minimum, err := strconv.ParseInt(value, 10, strconv.IntSize)
		if err != nil || minimum < 1 || minimum > int64(common.MaxQuota) {
			return fmt.Errorf("%s must be an integer in the range [1, %d]", key, common.MaxQuota)
		}
	case "LinuxDOMinimumTrustLevel":
		level, err := strconv.ParseInt(value, 10, 32)
		if err != nil || level < 0 {
			return fmt.Errorf("LinuxDOMinimumTrustLevel must be an integer in the range [0, %d]", int64(math.MaxInt32))
		}
	case "ModelRequestRateLimitCount", "ModelRequestRateLimitSuccessCount":
		count, err := strconv.ParseInt(value, 10, 32)
		if err != nil || count < 0 {
			return fmt.Errorf("%s must be an integer in the range [0, %d]", key, int64(math.MaxInt32))
		}
	case "ModelRequestRateLimitDurationMinutes":
		duration, err := strconv.ParseInt(value, 10, 64)
		maxMinutes := int64(math.MaxInt64) / (60 * int64(math.MaxInt32))
		if err != nil || duration < 1 || duration > maxMinutes {
			return fmt.Errorf("ModelRequestRateLimitDurationMinutes must be an integer in the range [1, %d]", maxMinutes)
		}
	case "RetryTimes":
		retries, err := strconv.Atoi(value)
		if err != nil || retries < 0 || retries > 100 {
			return fmt.Errorf("RetryTimes must be an integer in the range [0, 100]")
		}
	case "ChannelDisableThreshold":
		threshold, err := strconv.ParseFloat(value, 64)
		maxSeconds := float64(math.MaxInt64 / 1000)
		if err != nil || math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0 || threshold > maxSeconds {
			return fmt.Errorf("ChannelDisableThreshold must be finite and in the range [0, %.0f]", maxSeconds)
		}
	case "StreamCacheQueueLength":
		length, err := strconv.ParseInt(value, 10, 32)
		if err != nil || length < 0 {
			return fmt.Errorf("StreamCacheQueueLength must be an integer in the range [0, %d]", int64(math.MaxInt32))
		}
	case "QuotaPerUnit":
		quotaPerUnit, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(quotaPerUnit) || math.IsInf(quotaPerUnit, 0) || quotaPerUnit <= 0 || quotaPerUnit > float64(common.MaxQuota) {
			return fmt.Errorf("QuotaPerUnit must be finite and in the range (0, %d]", common.MaxQuota)
		}
	case "DataExportInterval":
		interval, err := strconv.ParseInt(value, 10, strconv.IntSize)
		maxMinutes := int64(math.MaxInt64 / int64(time.Minute))
		if err != nil || interval <= 0 || interval > maxMinutes {
			return fmt.Errorf("DataExportInterval must be an integer in the range [1, %d]", maxMinutes)
		}
	case "DataExportDefaultTime":
		switch value {
		case "hour", "day", "week":
		default:
			return fmt.Errorf("DataExportDefaultTime must be hour, day, or week")
		}
	case "QuotaForNewUser", "QuotaForInviter", "QuotaForInvitee", "QuotaRemindThreshold", "PreConsumedQuota":
		quota, err := strconv.ParseInt(value, 10, 64)
		if err != nil || quota < 0 || quota > int64(common.MaxQuota) {
			return fmt.Errorf("%s must be an integer in the range [0, %d]", key, common.MaxQuota)
		}
	case "ModelPrice", "ModelRatio", "CompletionRatio", "CacheRatio", "CreateCacheRatio", "ImageRatio", "AudioRatio", "AudioCompletionRatio":
		if err := ratio_setting.CheckRatioMap(value); err != nil {
			return fmt.Errorf("invalid %s: %w", key, err)
		}
	case "GroupRatio":
		if err := ratio_setting.CheckGroupRatio(value); err != nil {
			return err
		}
	case "GroupGroupRatio":
		if err := ratio_setting.CheckGroupGroupRatio(value); err != nil {
			return err
		}
	case "TopupGroupRatio":
		if err := common.ValidateTopupGroupRatioJSONString(value); err != nil {
			return err
		}
	case "Chats":
		var chats []map[string]string
		if err := common.UnmarshalJsonStr(value, &chats); err != nil || chats == nil {
			return fmt.Errorf("Chats must be a JSON array")
		}
	case "AutoGroups":
		var groups []string
		if err := common.UnmarshalJsonStr(value, &groups); err != nil || groups == nil {
			return fmt.Errorf("AutoGroups must be a JSON array")
		}
	case "GroupFallback":
		if err := setting.ValidateGroupFallbackJSONString(value); err != nil {
			return err
		}
	case "UserUsableGroups":
		var groups map[string]string
		if err := common.UnmarshalJsonStr(value, &groups); err != nil || groups == nil {
			return fmt.Errorf("UserUsableGroups must be a JSON object")
		}
	case "ModelRequestRateLimitGroup":
		if err := setting.CheckModelRequestRateLimitGroup(value); err != nil {
			return err
		}
	case "AutomaticDisableStatusCodes", "AutomaticRetryStatusCodes":
		if _, err := operation_setting.ParseHTTPStatusCodeRanges(value); err != nil {
			return err
		}
	case "PayMethods":
		var methods []map[string]string
		if err := common.UnmarshalJsonStr(value, &methods); err != nil || methods == nil {
			return fmt.Errorf("PayMethods must be a JSON array")
		}
	case "CreemProducts":
		var products []map[string]interface{}
		if err := common.UnmarshalJsonStr(value, &products); err != nil || products == nil {
			return fmt.Errorf("CreemProducts must be a JSON array")
		}
	case "claude.thinking_adapter_budget_tokens_percentage":
		percentage, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(percentage) || math.IsInf(percentage, 0) || percentage < 0.1 || percentage > 1 {
			return fmt.Errorf("Claude thinking budget percentage must be finite and in the range [0.1, 1]")
		}
	case "gemini.thinking_adapter_budget_tokens_percentage":
		percentage, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(percentage) || math.IsInf(percentage, 0) || percentage < 0.002 || percentage > 1 {
			return fmt.Errorf("Gemini thinking budget percentage must be finite and in the range [0.002, 1]")
		}
	case "claude.default_max_tokens":
		var limits map[string]int
		if err := common.UnmarshalJsonStr(value, &limits); err != nil || limits == nil {
			return fmt.Errorf("Claude default max tokens must be a JSON object of integers")
		}
		for modelName, limit := range limits {
			if limit <= 0 || limit > common.MaxTokensLimit {
				return fmt.Errorf("Claude default max tokens for %s must be in the range [1, %d]", modelName, common.MaxTokensLimit)
			}
		}
	case "tool_price_setting.prices":
		var prices map[string]float64
		if err := common.UnmarshalJsonStr(value, &prices); err != nil {
			return fmt.Errorf("tool prices must be a JSON object: %w", err)
		}
		if err := operation_setting.ValidateToolPriceMap(prices); err != nil {
			return err
		}
	}
	return nil
}

// handleConfigUpdate 处理分层配置更新，返回是否已处理
func handleConfigUpdate(key, value string) (bool, error) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return false, nil // 不是分层配置
	}

	configName := parts[0]
	configKey := parts[1]

	// 获取配置对象
	cfg := config.GlobalConfig.Get(configName)
	if cfg == nil {
		return false, nil // 未注册的配置
	}

	// 更新配置
	configMap := map[string]string{
		configKey: value,
	}
	if err := config.UpdateConfigFromMap(cfg, configMap); err != nil {
		return true, err
	}

	afterConfigUpdate(configName)

	return true, nil // 已处理
}

func afterConfigUpdate(configName string) {
	if configName == "performance_setting" {
		performance_setting.UpdateAndSync()
	} else if configName == "tool_price_setting" {
		operation_setting.RebuildToolPriceIndex()
	} else if configName == "billing_setting" {
		InvalidatePricingCache()
		ratio_setting.InvalidateExposedDataCache()
	} else if configName == "theme" {
		system_setting.UpdateAndSyncTheme()
	}
}
