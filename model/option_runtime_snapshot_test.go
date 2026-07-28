package model

import (
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpdateOptionsBulkPublishesLegacySettingsAtomically(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))

	originalDB := DB
	DB = db
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	originalStripeSecret := setting.StripeApiSecret
	originalStripeWebhookSecret := setting.StripeWebhookSecret
	originalStripeUnitPrice := setting.StripeUnitPrice
	common.OptionMap = map[string]string{
		"StripeApiSecret":     "secret-0",
		"StripeWebhookSecret": "webhook-0",
		"StripeUnitPrice":     "0",
	}
	setting.StripeApiSecret = "secret-0"
	setting.StripeWebhookSecret = "webhook-0"
	setting.StripeUnitPrice = 0
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		setting.StripeApiSecret = originalStripeSecret
		setting.StripeWebhookSecret = originalStripeWebhookSecret
		setting.StripeUnitPrice = originalStripeUnitPrice
		common.OptionMapRWMutex.Unlock()
		DB = originalDB
	})

	var stopped atomic.Bool
	failures := make(chan string, 1)
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for !stopped.Load() {
			snapshot := setting.GetStripeSettings()
			generation := strconv.FormatFloat(snapshot.UnitPrice, 'f', -1, 64)
			if snapshot.APISecret != "secret-"+generation ||
				snapshot.WebhookSecret != "webhook-"+generation {
				select {
				case failures <- fmt.Sprintf(
					"mixed Stripe generation: secret=%q webhook=%q price=%v",
					snapshot.APISecret,
					snapshot.WebhookSecret,
					snapshot.UnitPrice,
				):
				default:
				}
				return
			}
		}
	}()

	for generation := 1; generation <= 100; generation++ {
		value := strconv.Itoa(generation)
		require.NoError(t, UpdateOptionsBulk(map[string]string{
			"StripeApiSecret":     "secret-" + value,
			"StripeWebhookSecret": "webhook-" + value,
			"StripeUnitPrice":     value,
		}))
	}
	stopped.Store(true)
	readers.Wait()

	select {
	case failure := <-failures:
		assert.Fail(t, failure)
	default:
	}
}

func TestUpdateOptionsBulkValidatesAndPublishesRegisteredConfigAsOneCandidate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))

	originalDB := DB
	DB = db
	checkinConfig, ok := config.GlobalConfig.Get("checkin_setting").(*operation_setting.CheckinSetting)
	require.True(t, ok)
	originalCheckin := *operation_setting.GetCheckinSetting()
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = map[string]string{
		"checkin_setting.min_quota": strconv.Itoa(originalCheckin.MinQuota),
		"checkin_setting.max_quota": strconv.Itoa(originalCheckin.MaxQuota),
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(checkinConfig, map[string]string{
			"min_quota": strconv.Itoa(originalCheckin.MinQuota),
			"max_quota": strconv.Itoa(originalCheckin.MaxQuota),
		}))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
		DB = originalDB
	})

	require.NoError(t, UpdateOptionsBulk(map[string]string{
		"checkin_setting.min_quota": "20000",
		"checkin_setting.max_quota": "30000",
	}))
	assert.Equal(t, 20_000, operation_setting.GetCheckinSetting().MinQuota)
	assert.Equal(t, 30_000, operation_setting.GetCheckinSetting().MaxQuota)

	require.Error(t, UpdateOptionsBulk(map[string]string{
		"checkin_setting.min_quota": "40000",
		"checkin_setting.max_quota": "35000",
	}))
	assert.Equal(t, 20_000, operation_setting.GetCheckinSetting().MinQuota)
	assert.Equal(t, 30_000, operation_setting.GetCheckinSetting().MaxQuota)

	var stored []*Option
	require.NoError(t, db.Order("key").Find(&stored).Error)
	require.Len(t, stored, 2)
	assert.Equal(t, "30000", stored[0].Value)
	assert.Equal(t, "20000", stored[1].Value)

	require.NoError(t, db.Model(&Option{}).
		Where("key = ?", "checkin_setting.min_quota").
		Update("value", "40000").Error)
	require.NoError(t, db.Model(&Option{}).
		Where("key = ?", "checkin_setting.max_quota").
		Update("value", "50000").Error)
	loadOptionsFromDatabase()
	assert.Equal(t, 40_000, operation_setting.GetCheckinSetting().MinQuota)
	assert.Equal(t, 50_000, operation_setting.GetCheckinSetting().MaxQuota)
}
