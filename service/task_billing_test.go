package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic("failed to open test db: " + err.Error())
	}
	sqlDB, err := db.DB()
	if err != nil {
		panic("failed to get sql.DB: " + err.Error())
	}
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true

	if err := db.AutoMigrate(
		&model.Task{},
		&model.User{},
		&model.AffiliateReward{},
		&model.Token{},
		&model.Log{},
		&model.Channel{},
		&model.TopUp{},
		&model.UserSubscription{},
		&model.SubscriptionPreConsumeRecord{},
		&model.SystemTask{},
		&model.SystemTaskLock{},
		&model.BillingReservation{},
		&model.TaskBillingFinalization{},
		&model.QuotaData{},
	); err != nil {
		panic("failed to migrate: " + err.Error())
	}

	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Seed helpers
// ---------------------------------------------------------------------------

func truncate(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM tasks")
		model.DB.Exec("DELETE FROM users")
		model.DB.Exec("DELETE FROM affiliate_rewards")
		model.DB.Exec("DELETE FROM tokens")
		model.DB.Exec("DELETE FROM logs")
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM top_ups")
		model.DB.Exec("DELETE FROM user_subscriptions")
		model.DB.Exec("DELETE FROM subscription_pre_consume_records")
		model.DB.Exec("DELETE FROM system_task_locks")
		model.DB.Exec("DELETE FROM system_tasks")
		model.DB.Exec("DELETE FROM billing_reservations")
		model.DB.Exec("DELETE FROM task_billing_finalizations")
		model.DB.Exec("DELETE FROM quota_data")
	})
}

func seedUser(t *testing.T, id int, quota int) {
	t.Helper()
	user := &model.User{Id: id, Username: "test_user", Quota: quota, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)
}

func seedToken(t *testing.T, id int, userId int, key string, remainQuota int) {
	t.Helper()
	token := &model.Token{
		Id:          id,
		UserId:      userId,
		Key:         key,
		Name:        "test_token",
		Status:      common.TokenStatusEnabled,
		RemainQuota: remainQuota,
		UsedQuota:   0,
	}
	require.NoError(t, model.DB.Create(token).Error)
}

func seedSubscription(t *testing.T, id int, userId int, amountTotal int64, amountUsed int64) {
	t.Helper()
	sub := &model.UserSubscription{
		Id:          id,
		UserId:      userId,
		AmountTotal: amountTotal,
		AmountUsed:  amountUsed,
		Status:      "active",
		StartTime:   time.Now().Unix(),
		EndTime:     time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(sub).Error)
}

func seedChannel(t *testing.T, id int) {
	t.Helper()
	ch := &model.Channel{Id: id, Name: "test_channel", Key: "sk-test", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(ch).Error)
}

func makeTask(userId, channelId, quota, tokenId int, billingSource string, subscriptionId int) *model.Task {
	return &model.Task{
		TaskID:    "task_" + time.Now().Format("150405.000"),
		UserId:    userId,
		ChannelId: channelId,
		Quota:     quota,
		Status:    model.TaskStatus(model.TaskStatusInProgress),
		Group:     "default",
		Data:      json.RawMessage(`{}`),
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		Properties: model.Properties{
			OriginModelName: "test-model",
		},
		PrivateData: model.TaskPrivateData{
			BillingSource:  billingSource,
			SubscriptionId: subscriptionId,
			TokenId:        tokenId,
			BillingContext: &model.TaskBillingContext{
				ModelPrice:      0.02,
				GroupRatio:      1.0,
				OriginModelName: "test-model",
			},
		},
	}
}

func TestTaskSubscriptionRefundPreservesUsageAfterQuotaReset(t *testing.T) {
	truncate(t)
	seedUser(t, 91, 0)
	seedToken(t, 92, 91, "task-reset-token", 400)
	seedSubscription(t, 93, 91, 1_000, 7)
	seedChannel(t, 94)

	requestID := "task-refund-before-subscription-reset"
	record := model.SubscriptionPreConsumeRecord{
		RequestId:          requestID,
		UserId:             91,
		UserSubscriptionId: 93,
		PreConsumed:        100,
		QuotaResetVersion:  0,
		Status:             "consumed",
	}
	require.NoError(t, model.DB.Create(&record).Error)
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", 93).
		Update("quota_reset_version", 1).Error)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 92).
		Updates(map[string]any{"remain_quota": 300, "used_quota": 100}).Error)

	task := makeTask(91, 94, 100, 92, BillingSourceSubscription, 93)
	task.TaskID = "task-reset-safe-refund"
	task.PrivateData.BillingRequestId = requestID
	RefundTaskQuota(context.Background(), task, "provider failure after reset")

	var subscription model.UserSubscription
	require.NoError(t, model.DB.First(&subscription, 93).Error)
	assert.Equal(t, int64(7), subscription.AmountUsed)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 92).Error)
	assert.Equal(t, 400, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	require.NoError(t, model.DB.First(&record, record.Id).Error)
	assert.Equal(t, "refunded", record.Status)
}

func TestPriceDataOtherRatiosFilterAndSnapshot(t *testing.T) {
	priceData := types.PriceData{}

	priceData.AddOtherRatio("zero", 0)
	priceData.AddOtherRatio("negative", -0.5)
	priceData.AddOtherRatio("nan", math.NaN())
	priceData.AddOtherRatio("inf", math.Inf(1))
	priceData.AddOtherRatio("one", 1)
	priceData.AddOtherRatio("positive", 2.5)

	ratios := priceData.OtherRatios()
	require.Len(t, ratios, 2)
	assert.Equal(t, 1.0, ratios["one"])
	assert.Equal(t, 2.5, ratios["positive"])
	assert.True(t, priceData.HasOtherRatio("one"))
	assert.False(t, priceData.HasOtherRatio("zero"))

	ratios["positive"] = 99
	ratios["new"] = 3
	nextSnapshot := priceData.OtherRatios()
	assert.Equal(t, 2.5, nextSnapshot["positive"])
	assert.NotContains(t, nextSnapshot, "new")
}

func TestPriceDataReplaceAndApplyOtherRatios(t *testing.T) {
	priceData := types.PriceData{}

	replaced := priceData.ReplaceOtherRatios(map[string]float64{
		"zero":     0,
		"negative": -3,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
		"one":      1,
		"duration": 2,
		"size":     1.5,
	})

	require.True(t, replaced)
	assert.Equal(t, 3.0, priceData.OtherRatioMultiplier())
	assert.Equal(t, 30.0, priceData.ApplyOtherRatiosToFloat(10))
	assert.Equal(t, 10.0, priceData.RemoveOtherRatiosFromFloat(30))
	assert.True(t, decimal.NewFromInt(30).Equal(priceData.ApplyOtherRatiosToDecimal(decimal.NewFromInt(10))))

	replaced = priceData.ReplaceOtherRatios(map[string]float64{
		"zero": 0,
		"nan":  math.NaN(),
	})

	require.False(t, replaced)
	assert.Nil(t, priceData.OtherRatios())
	assert.Equal(t, 1.0, priceData.OtherRatioMultiplier())
}

func TestTaskBillingOtherFiltersHistoricalOtherRatios(t *testing.T) {
	task := makeTask(1, 1, 100, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.OtherRatios = map[string]float64{
		"seconds":  2,
		"identity": 1,
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
	}

	other := taskBillingOther(task)

	assert.Equal(t, 2.0, other["seconds"])
	assert.Equal(t, 1.0, other["identity"])
	assert.NotContains(t, other, "zero")
	assert.NotContains(t, other, "negative")
	assert.NotContains(t, other, "nan")
	assert.NotContains(t, other, "inf")
}

func TestTaskBillingContextPriceDataFiltersMultiplier(t *testing.T) {
	priceData := taskBillingContextPriceData(&model.TaskBillingContext{
		OtherRatios: map[string]float64{
			"seconds":  2,
			"size":     3,
			"identity": 1,
			"zero":     0,
			"negative": -1,
			"nan":      math.NaN(),
			"inf":      math.Inf(1),
		},
	})

	require.NotNil(t, priceData)
	assert.Equal(t, 6.0, priceData.OtherRatioMultiplier())
	assert.Equal(t, map[string]float64{
		"seconds":  2,
		"size":     3,
		"identity": 1,
	}, priceData.OtherRatios())
}

func TestCalculateTaskQuotaByTokensUsesSnapshotAndLegacyFallback(t *testing.T) {
	originalModelRatios := ratio_setting.ModelRatio2JSONString()
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	originalGroupGroupRatios := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModelRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(originalGroupGroupRatios))
	})

	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{
		"snapshot-model": 11,
		"legacy-model": 5
	}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{
		"snapshot-group": 13,
		"legacy-group": 7
	}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{
		"snapshot-group": {
			"snapshot-group": 19
		}
	}`))

	t.Run("persisted submission snapshot is stable after live pricing changes", func(t *testing.T) {
		task := makeTask(1, 1, 1, 0, BillingSourceWallet, 0)
		task.Group = "snapshot-group"
		task.Properties.OriginModelName = "snapshot-model"
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			ModelRatio:      2,
			GroupRatio:      3,
			OtherRatios:     map[string]float64{"provider_multiplier": 0.5},
			OriginModelName: "snapshot-model",
		}

		actualQuota, reason, clamp, ok, err := calculateTaskQuotaByTokens(task, 10)

		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, 30, actualQuota)
		assert.Nil(t, clamp)
		assert.Equal(t, "token重算：tokens=10, modelRatio=2.00, groupRatio=3.00, otherMultiplier=0.5000", reason)
	})

	t.Run("legacy task without snapshot uses current settings", func(t *testing.T) {
		task := makeTask(1, 1, 1, 0, BillingSourceWallet, 0)
		task.Group = "legacy-group"
		task.Properties.OriginModelName = "legacy-model"
		task.PrivateData.BillingContext = nil

		actualQuota, reason, clamp, ok, err := calculateTaskQuotaByTokens(task, 10)

		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, 350, actualQuota)
		assert.Nil(t, clamp)
		assert.Equal(t, "token重算：tokens=10, modelRatio=5.00, groupRatio=7.00, otherMultiplier=1.0000", reason)
	})

	t.Run("provider token total above the billing boundary is rejected", func(t *testing.T) {
		task := makeTask(1, 1, 1, 0, BillingSourceWallet, 0)
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			ModelRatio:      2,
			GroupRatio:      3,
			OriginModelName: "snapshot-model",
		}

		actualQuota, reason, clamp, ok, err := calculateTaskQuotaByTokens(task, common.MaxTokensLimit+1)

		require.ErrorContains(t, err, "provider token total exceeds")
		assert.False(t, ok)
		assert.Zero(t, actualQuota)
		assert.Empty(t, reason)
		assert.Nil(t, clamp)
	})

	t.Run("missing task cannot enter token billing", func(t *testing.T) {
		actualQuota, reason, clamp, ok, err := calculateTaskQuotaByTokens(nil, 10)

		require.NoError(t, err)
		assert.False(t, ok)
		assert.Zero(t, actualQuota)
		assert.Empty(t, reason)
		assert.Nil(t, clamp)
	})

	t.Run("corrupt snapshot ratios fail closed", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			context *model.TaskBillingContext
		}{
			{
				name: "non-finite model ratio",
				context: &model.TaskBillingContext{
					ModelRatio: math.NaN(),
					GroupRatio: 1,
				},
			},
			{
				name: "non-positive group ratio",
				context: &model.TaskBillingContext{
					ModelRatio: 1,
					GroupRatio: 0,
				},
			},
			{
				name: "invalid other ratio",
				context: &model.TaskBillingContext{
					ModelRatio:  1,
					GroupRatio:  1,
					OtherRatios: map[string]float64{"seconds": math.Inf(1)},
				},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				task := makeTask(1, 1, 1, 0, BillingSourceWallet, 0)
				task.PrivateData.BillingContext = test.context

				actualQuota, reason, clamp, ok, err := calculateTaskQuotaByTokens(task, 10)

				require.Error(t, err)
				assert.False(t, ok)
				assert.Zero(t, actualQuota)
				assert.Empty(t, reason)
				assert.Nil(t, clamp)
			})
		}
	})

	t.Run("positive token usage has a minimum one-quota charge", func(t *testing.T) {
		task := makeTask(1, 1, 1, 0, BillingSourceWallet, 0)
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			ModelRatio: 0.0001,
			GroupRatio: 1,
		}

		actualQuota, _, clamp, ok, err := calculateTaskQuotaByTokens(task, 1)

		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, 1, actualQuota)
		assert.Nil(t, clamp)
	})

	t.Run("legacy user group lookup errors are propagated", func(t *testing.T) {
		task := makeTask(1, 1, 1, 0, BillingSourceWallet, 0)
		task.Group = ""
		task.Properties.OriginModelName = "legacy-model"
		task.PrivateData.BillingContext = nil

		callbackName := "inject_legacy_task_user_group_failure"
		require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(
			callbackName,
			func(tx *gorm.DB) {
				tx.AddError(errors.New("injected legacy task user lookup failure"))
			},
		))
		t.Cleanup(func() {
			require.NoError(t, model.DB.Callback().Query().Remove(callbackName))
		})

		actualQuota, reason, clamp, ok, err := calculateTaskQuotaByTokens(task, 10)

		require.ErrorContains(t, err, "injected legacy task user lookup failure")
		assert.False(t, ok)
		assert.Zero(t, actualQuota)
		assert.Empty(t, reason)
		assert.Nil(t, clamp)
	})
}

// ---------------------------------------------------------------------------
// Read-back helpers
// ---------------------------------------------------------------------------

func getUserQuota(t *testing.T, id int) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("quota").Where("id = ?", id).First(&user).Error)
	return user.Quota
}

func getTokenRemainQuota(t *testing.T, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Select("remain_quota").Where("id = ?", id).First(&token).Error)
	return token.RemainQuota
}

func getTokenUsedQuota(t *testing.T, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", id).First(&token).Error)
	return token.UsedQuota
}

func getSubscriptionUsed(t *testing.T, id int) int64 {
	t.Helper()
	var sub model.UserSubscription
	require.NoError(t, model.DB.Select("amount_used").Where("id = ?", id).First(&sub).Error)
	return sub.AmountUsed
}

func getLastLog(t *testing.T) *model.Log {
	t.Helper()
	var log model.Log
	err := model.LOG_DB.Order("id desc").First(&log).Error
	if err != nil {
		return nil
	}
	return &log
}

func countLogs(t *testing.T) int64 {
	t.Helper()
	var count int64
	model.LOG_DB.Model(&model.Log{}).Count(&count)
	return count
}

// ===========================================================================
// RefundTaskQuota tests
// ===========================================================================

func TestRefundTaskQuota_Wallet(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 1, 1, 1
	const initQuota, preConsumed = 10000, 3000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-test-key", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)

	RefundTaskQuota(ctx, task, "task failed: upstream error")
	RefundTaskQuota(ctx, task, "task failed: duplicate poll")

	// User quota should increase by preConsumed
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))

	// Token remain_quota should increase, used_quota should decrease
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, -preConsumed, getTokenUsedQuota(t, tokenID))

	// A refund log should be created
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed, log.Quota)
	assert.Equal(t, "test-model", log.ModelName)
	assert.Equal(t, int64(1), countLogs(t))
}

func TestRefundTaskQuota_Subscription(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 2, 2, 2, 1
	const preConsumed = 2000
	const subTotal, subUsed int64 = 100000, 50000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-sub-key", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)
	task.PrivateData.BillingRequestId = "task-subscription-refund"
	require.NoError(t, model.DB.Create(&model.SubscriptionPreConsumeRecord{
		RequestId:          task.PrivateData.BillingRequestId,
		UserId:             userID,
		UserSubscriptionId: subID,
		PreConsumed:        preConsumed,
		Status:             "consumed",
	}).Error)

	RefundTaskQuota(ctx, task, "subscription task failed")

	// Subscription used should decrease by preConsumed
	assert.Equal(t, subUsed-int64(preConsumed), getSubscriptionUsed(t, subID))

	// Token should also be refunded
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestRefundLegacySubscriptionTaskWithoutReservationProofRejectsPoisonOutbox(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID, subID = 117, 117, 117, 117
	const quota = 500
	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-legacy-subscription-task", 4_500)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, 10_000, 2_000)
	task := makeTask(userID, channelID, quota, tokenID, BillingSourceSubscription, subID)
	task.TaskID = "legacy-subscription-task-without-proof"

	RefundTaskQuota(context.Background(), task, "legacy task failure")

	assert.Equal(t, int64(2_000), getSubscriptionUsed(t, subID))
	assert.Equal(t, 4_500, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(0), countLogs(t))
	var finalizationCount int64
	require.NoError(t, model.DB.Model(&model.TaskBillingFinalization{}).Count(&finalizationCount).Error)
	assert.Zero(t, finalizationCount, "an adjustment that can never pass proof validation must not poison the retry queue")
	var adjustmentCount int64
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("type = ?", model.SystemTaskTypeBillingAdjustment).Count(&adjustmentCount).Error)
	assert.Zero(t, adjustmentCount)
}

func TestRefundTaskQuota_InvalidQuota(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 3
	seedUser(t, userID, 5000)

	task := makeTask(userID, 0, 0, 0, BillingSourceWallet, 0)

	RefundTaskQuota(ctx, task, "zero quota task")
	task.Quota = -100
	RefundTaskQuota(ctx, task, "corrupt negative quota task")
	task.Quota = common.MaxQuota + 1
	RefundTaskQuota(ctx, task, "corrupt oversized quota task")
	RefundTaskQuota(ctx, nil, "missing task")

	// No change to user quota
	assert.Equal(t, 5000, getUserQuota(t, userID))

	// No log created
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRefundTaskQuota_NoToken(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID = 4, 4
	const initQuota, preConsumed = 10000, 1500

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0) // TokenId=0

	RefundTaskQuota(ctx, task, "no token task failed")

	// User quota refunded
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))

	// Log created
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

// ===========================================================================
// RecalculateTaskQuota tests
// ===========================================================================

func TestRecalculate_PositiveDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 10, 10, 10
	const initQuota, preConsumed = 10000, 2000
	const actualQuota = 3000 // under-charged by 1000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-pos", tokenRemain)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Update("request_count", 1).Error)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)
	staleTask := *task

	RecalculateTaskQuota(ctx, task, actualQuota, "adaptor adjustment")
	RecalculateTaskQuota(ctx, &staleTask, actualQuota, "duplicate stale poll")

	// User quota should decrease by the delta (1000 additional charge)
	assert.Equal(t, initQuota-(actualQuota-preConsumed), getUserQuota(t, userID))

	// Token should also be charged the delta
	assert.Equal(t, tokenRemain-(actualQuota-preConsumed), getTokenRemainQuota(t, tokenID))

	// task.Quota should be updated to actualQuota
	assert.Equal(t, actualQuota, task.Quota)

	// Log type should be Consume (additional charge)
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, actualQuota-preConsumed, log.Quota)
	assert.Equal(t, int64(1), countLogs(t))
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Equal(t, 1, user.RequestCount, "task recalculation must not count a second request")
}

func TestTaskSubmissionAndRecalculationCountRequestExactlyOnce(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 115, 115, 115
	const preConsumedQuota, submittedQuota, recalculatedQuota = 120, 120, 150
	seedUser(t, userID, 10_000-preConsumedQuota)
	seedToken(t, tokenID, userID, "sk-task-submission-finalization", 5_000-preConsumedQuota)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", tokenID).Update("used_quota", preConsumedQuota).Error)

	info := &relaycommon.RelayInfo{
		RequestId:       "task-submission-finalization",
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "sk-task-submission-finalization",
		OriginModelName: "test-model",
		UsingGroup:      "default",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{Action: "generate"},
	}
	info.PriceData.Quota = submittedQuota
	session := &BillingSession{
		relayInfo:        info,
		funding:          &WalletFunding{userId: userID, consumed: preConsumedQuota},
		preConsumedQuota: preConsumedQuota,
		tokenConsumed:    preConsumedQuota,
	}
	info.Billing = session

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	c.Set(common.RequestIdKey, info.RequestId)
	c.Set("username", "test_user")
	c.Set("token_name", "test_token")

	task := makeTask(userID, channelID, submittedQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task-submission-finalization"
	task.PrivateData.BillingRequestId = info.RequestId
	task.PrivateData.NodeName = common.NodeName
	payload, err := BuildTaskSubmissionBillingFinalization(c, info, task, submittedQuota)
	require.NoError(t, err)
	finalizationID, err := model.InsertTaskWithBillingFinalization(task, payload)
	require.NoError(t, err)
	require.NotEmpty(t, finalizationID)
	require.NoError(t, ProcessPersistedBillingFinalization(c, info, submittedQuota, finalizationID))

	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Equal(t, 10_000-submittedQuota, user.Quota)
	assert.Equal(t, submittedQuota, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	assert.Equal(t, int64(submittedQuota), channel.UsedQuota)
	assert.Equal(t, int64(1), countLogs(t))

	RecalculateTaskQuota(context.Background(), task, recalculatedQuota, "provider token recalculation")
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Equal(t, 10_000-recalculatedQuota, user.Quota)
	assert.Equal(t, recalculatedQuota, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount, "recalculation must not increment the accepted task again")
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	assert.Equal(t, int64(recalculatedQuota), channel.UsedQuota)
	assert.Equal(t, int64(2), countLogs(t))
}

func TestSubscriptionTaskSubmissionRetainsProofForNegativeRecalculation(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID, subscriptionID = 119, 119, 119, 119
	const submittedQuota, recalculatedQuota = 120, 80
	const subscriptionTotal int64 = 1_000_000
	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-subscription-task-finalization", 5_000-submittedQuota)
	seedChannel(t, channelID)
	seedSubscription(t, subscriptionID, userID, subscriptionTotal, submittedQuota)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", tokenID).Update("used_quota", submittedQuota).Error)
	requestID := "subscription-task-finalization"
	require.NoError(t, model.DB.Create(&model.SubscriptionPreConsumeRecord{
		RequestId:          requestID,
		UserId:             userID,
		UserSubscriptionId: subscriptionID,
		PreConsumed:        submittedQuota,
		Status:             "consumed",
	}).Error)

	info := &relaycommon.RelayInfo{
		RequestId:                             requestID,
		UserId:                                userID,
		TokenId:                               tokenID,
		TokenKey:                              "sk-subscription-task-finalization",
		OriginModelName:                       "test-model",
		UsingGroup:                            "default",
		BillingSource:                         BillingSourceSubscription,
		SubscriptionId:                        subscriptionID,
		SubscriptionPreConsumed:               submittedQuota,
		SubscriptionAmountTotal:               subscriptionTotal,
		SubscriptionAmountUsedAfterPreConsume: submittedQuota,
		StartTime:                             time.Now(),
		ChannelMeta:                           &relaycommon.ChannelMeta{ChannelId: channelID},
		TaskRelayInfo:                         &relaycommon.TaskRelayInfo{Action: "generate"},
	}
	info.PriceData.Quota = submittedQuota
	info.Billing = &BillingSession{
		relayInfo: info,
		funding: &SubscriptionFunding{
			requestId:       requestID,
			userId:          userID,
			modelName:       info.OriginModelName,
			amount:          submittedQuota,
			subscriptionId:  subscriptionID,
			preConsumed:     submittedQuota,
			AmountTotal:     subscriptionTotal,
			AmountUsedAfter: submittedQuota,
		},
		preConsumedQuota: submittedQuota,
		tokenConsumed:    submittedQuota,
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	c.Set(common.RequestIdKey, requestID)

	task := makeTask(userID, channelID, submittedQuota, tokenID, BillingSourceSubscription, subscriptionID)
	task.TaskID = "task-subscription-finalization"
	task.PrivateData.BillingRequestId = requestID
	payload, err := BuildTaskSubmissionBillingFinalization(c, info, task, submittedQuota)
	require.NoError(t, err)
	finalizationID, err := model.InsertTaskWithBillingFinalization(task, payload)
	require.NoError(t, err)
	require.NoError(t, ProcessPersistedBillingFinalization(c, info, submittedQuota, finalizationID))

	var reservation model.SubscriptionPreConsumeRecord
	require.NoError(t, model.DB.Where("request_id = ?", requestID).First(&reservation).Error)
	assert.Equal(t, "settled", reservation.Status)
	assert.Equal(t, int64(submittedQuota), reservation.PreConsumed)

	RecalculateTaskQuota(context.Background(), task, recalculatedQuota, "provider task adjustment")
	require.NoError(t, model.DB.Where("request_id = ?", requestID).First(&reservation).Error)
	assert.Equal(t, "settled", reservation.Status)
	assert.Equal(t, int64(recalculatedQuota), reservation.PreConsumed)
	assert.Equal(t, int64(recalculatedQuota), getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, 5_000-recalculatedQuota, getTokenRemainQuota(t, tokenID))
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Equal(t, submittedQuota, user.UsedQuota, "refunds must not decrement gross cumulative usage")
	assert.Equal(t, 1, user.RequestCount)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	assert.Equal(t, int64(submittedQuota), channel.UsedQuota, "refunds must not decrement gross cumulative channel usage")
}

func TestTaskSubmissionInsertRollsBackWhenBillingOutboxIsInvalid(t *testing.T) {
	truncate(t)
	task := makeTask(118, 118, 100, 0, BillingSourceWallet, 0)
	task.TaskID = "task-invalid-submission-finalization"
	payload := model.TaskBillingFinalizationPayload{
		Adjustment: model.BillingAdjustment{
			RequestID:     "task-invalid-submission-finalization",
			Kind:          model.BillingAdjustmentSettle,
			FundingSource: model.BillingAdjustmentWallet,
			UserID:        task.UserId,
		},
		IncrementUserRequestCount: true,
		Log: model.TaskBillingFinalizationLog{
			UserID:    task.UserId + 1,
			LogType:   model.LogTypeConsume,
			ModelName: "test-model",
			CreatedAt: common.GetTimestamp(),
		},
	}

	_, err := model.InsertTaskWithBillingFinalization(task, payload)
	require.ErrorContains(t, err, "log user does not match adjustment")
	var taskCount int64
	require.NoError(t, model.DB.Model(&model.Task{}).Where("task_id = ?", task.TaskID).Count(&taskCount).Error)
	assert.Zero(t, taskCount)
	var finalizationCount int64
	require.NoError(t, model.DB.Model(&model.TaskBillingFinalization{}).Count(&finalizationCount).Error)
	assert.Zero(t, finalizationCount)
}

func TestTaskBillingFinalizationReplaysSettlementAfterBalanceCommitWithSeparateLogDB(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 110, 110, 110
	const initialQuota, initialTokenQuota, preConsumed, actualQuota = 10_000, 5_000, 2_000, 3_000
	delta := actualQuota - preConsumed
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, "sk-finalization-settle", initialTokenQuota)
	seedChannel(t, channelID)
	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)

	oldLogDB := model.LOG_DB
	separateLogDB, err := gorm.Open(sqlite.Open("file:task-finalization-log?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, separateLogDB.AutoMigrate(&model.Log{}))
	model.LOG_DB = separateLogDB
	t.Cleanup(func() {
		model.LOG_DB = oldLogDB
		sqlDB, dbErr := separateLogDB.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	payload := model.TaskBillingFinalizationPayload{
		Adjustment:                taskBillingAdjustment(task, model.BillingAdjustmentSettle, delta),
		TaskDatabaseID:            task.ID,
		UpdateTaskQuota:           true,
		TargetTaskQuota:           actualQuota,
		UserUsedQuotaDelta:        delta,
		IncrementUserRequestCount: false,
		ChannelUsedQuotaDelta:     delta,
		Log: model.TaskBillingFinalizationLog{
			UserID:    userID,
			LogType:   model.LogTypeConsume,
			Content:   "durable replay",
			ChannelID: channelID,
			ModelName: taskModelName(task),
			Quota:     delta,
			TokenID:   tokenID,
			Group:     task.Group,
			Other: map[string]interface{}{
				"task_id":            task.TaskID,
				"pre_consumed_quota": preConsumed,
				"actual_quota":       actualQuota,
			},
			CreatedAt: common.GetTimestamp(),
		},
	}
	finalizationID, err := model.EnqueueTaskBillingFinalization(payload)
	require.NoError(t, err)
	adjustmentID, err := model.EnqueueBillingAdjustment(payload.Adjustment)
	require.NoError(t, err)
	require.Equal(t, finalizationID, adjustmentID)
	_, err = model.ProcessBillingAdjustmentWithResult(adjustmentID)
	require.NoError(t, err)

	// Simulated crash point: balances committed, but none of the dependent
	// main-DB fields or separate-DB log have run.
	assert.Equal(t, initialQuota-delta, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota-delta, getTokenRemainQuota(t, tokenID))
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Zero(t, user.UsedQuota)
	assert.Zero(t, user.RequestCount)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	assert.Zero(t, channel.UsedQuota)
	var persistedTask model.Task
	require.NoError(t, model.DB.First(&persistedTask, task.ID).Error)
	assert.Equal(t, preConsumed, persistedTask.Quota)
	assert.Equal(t, int64(0), countLogs(t))

	_, err = model.ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	_, err = model.ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)

	assert.Equal(t, initialQuota-delta, getUserQuota(t, userID), "replay must not charge the balance twice")
	assert.Equal(t, initialTokenQuota-delta, getTokenRemainQuota(t, tokenID))
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Equal(t, delta, user.UsedQuota)
	assert.Zero(t, user.RequestCount)
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	assert.Equal(t, int64(delta), channel.UsedQuota)
	require.NoError(t, model.DB.First(&persistedTask, task.ID).Error)
	assert.Equal(t, actualQuota, persistedTask.Quota)
	assert.Equal(t, int64(1), countLogs(t))
}

func TestTaskBillingFinalizationReplaysRefundLogAfterBalanceCommit(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 111, 111, 111
	const initialQuota, initialTokenQuota, quota = 10_000, 5_000, 2_000
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, "sk-finalization-refund", initialTokenQuota)
	seedChannel(t, channelID)
	task := makeTask(userID, channelID, quota, tokenID, BillingSourceWallet, 0)
	payload := model.TaskBillingFinalizationPayload{
		Adjustment: taskBillingAdjustment(task, model.BillingAdjustmentRefund, -quota),
		Log: model.TaskBillingFinalizationLog{
			UserID:    userID,
			LogType:   model.LogTypeRefund,
			ChannelID: channelID,
			ModelName: taskModelName(task),
			Quota:     quota,
			TokenID:   tokenID,
			Group:     task.Group,
			Other:     map[string]interface{}{"task_id": task.TaskID, "reason": "durable refund replay"},
			CreatedAt: common.GetTimestamp(),
		},
	}
	finalizationID, err := model.EnqueueTaskBillingFinalization(payload)
	require.NoError(t, err)
	adjustmentID, err := model.EnqueueBillingAdjustment(payload.Adjustment)
	require.NoError(t, err)
	_, err = model.ProcessBillingAdjustmentWithResult(adjustmentID)
	require.NoError(t, err)
	assert.Equal(t, initialQuota+quota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota+quota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(0), countLogs(t))

	_, err = model.ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	_, err = model.ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	assert.Equal(t, initialQuota+quota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota+quota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestTaskBillingFinalizationSaturatesAggregateOverflowAndCompletes(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 112, 112, 112
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "sk-finalization-overflow", 5_000)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Update("used_quota", common.MaxQuota).Error)
	task := makeTask(userID, channelID, 2_000, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)
	payload := model.TaskBillingFinalizationPayload{
		Adjustment:                taskBillingAdjustment(task, model.BillingAdjustmentSettle, 1),
		TaskDatabaseID:            task.ID,
		UpdateTaskQuota:           true,
		TargetTaskQuota:           2_001,
		UserUsedQuotaDelta:        1,
		IncrementUserRequestCount: true,
		ChannelUsedQuotaDelta:     1,
		Log: model.TaskBillingFinalizationLog{
			UserID:    userID,
			LogType:   model.LogTypeConsume,
			ChannelID: channelID,
			ModelName: taskModelName(task),
			Quota:     1,
			TokenID:   tokenID,
			Group:     task.Group,
			CreatedAt: common.GetTimestamp(),
		},
	}
	finalizationID, err := model.EnqueueTaskBillingFinalization(payload)
	require.NoError(t, err)

	_, err = model.ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	assert.Equal(t, 9_999, getUserQuota(t, userID), "the already committed balance adjustment remains exactly once")
	assert.Equal(t, 4_999, getTokenRemainQuota(t, tokenID))
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Equal(t, common.MaxQuota, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	assert.Equal(t, int64(1), channel.UsedQuota)
	var persistedTask model.Task
	require.NoError(t, model.DB.First(&persistedTask, task.ID).Error)
	assert.Equal(t, 2_001, persistedTask.Quota)
	assert.Equal(t, int64(1), countLogs(t))

	_, err = model.ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	assert.Equal(t, 9_999, getUserQuota(t, userID))
	assert.Equal(t, 4_999, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestRecalculate_NegativeDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 11, 11, 11
	const initQuota, preConsumed = 10000, 5000
	const actualQuota = 3000 // over-charged by 2000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-neg", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)

	RecalculateTaskQuota(ctx, task, actualQuota, "adaptor adjustment")

	// User quota should increase by abs(delta) = 2000 (refund overpayment)
	assert.Equal(t, initQuota+(preConsumed-actualQuota), getUserQuota(t, userID))

	// Token should be refunded the difference
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	// task.Quota updated
	assert.Equal(t, actualQuota, task.Quota)

	// Log type should be Refund
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed-actualQuota, log.Quota)
}

func TestRecalculate_ZeroDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 12
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, preConsumed, "exact match")

	// No change to user quota
	assert.Equal(t, initQuota, getUserQuota(t, userID))

	// No log created (delta is zero)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRecalculate_ActualQuotaZero(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 13
	const initQuota = 10000

	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, 5000, 0, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, 0, "zero actual")

	// No change (early return)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRecalculate_Subscription_NegativeDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 14, 14, 14, 2
	const preConsumed = 5000
	const actualQuota = 2000 // over-charged by 3000
	const subTotal, subUsed int64 = 100000, 50000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-sub-recalc", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)
	task.PrivateData.BillingRequestId = "task-subscription-recalculation"
	require.NoError(t, model.DB.Create(task).Error)
	require.NoError(t, model.DB.Create(&model.SubscriptionPreConsumeRecord{
		RequestId:          task.PrivateData.BillingRequestId,
		UserId:             userID,
		UserSubscriptionId: subID,
		PreConsumed:        preConsumed,
		Status:             "consumed",
	}).Error)

	RecalculateTaskQuota(ctx, task, actualQuota, "subscription over-charge")

	// Subscription used should decrease by delta (refund 3000)
	assert.Equal(t, subUsed-int64(preConsumed-actualQuota), getSubscriptionUsed(t, subID))

	// Token refunded
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	assert.Equal(t, actualQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestRecalculateSubscriptionSplitsOverflowToWallet(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 15, 15, 15, 3
	const preConsumed = 10
	const actualQuota = 40
	const tokenRemain = 490

	seedUser(t, userID, 200)
	seedToken(t, tokenID, userID, "sk-sub-overflow", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, 100, 90)
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", subID).Update("allow_wallet_overflow", true).Error)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)
	require.NoError(t, model.DB.Create(task).Error)
	RecalculateTaskQuota(ctx, task, actualQuota, "subscription overflow")

	assert.Equal(t, 180, getUserQuota(t, userID))
	assert.Equal(t, int64(100), getSubscriptionUsed(t, subID))
	assert.Equal(t, tokenRemain-(actualQuota-preConsumed), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, actualQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	assert.Equal(t, float64(10), other["subscription_post_delta"])
	assert.Equal(t, float64(20), other["subscription_wallet_overflow"])
	assert.Equal(t, float64(20), other["wallet_quota_deducted"])
}

// ===========================================================================
// CAS + Billing integration tests
// Simulates the flow in updateVideoSingleTask (service/task_polling.go)
// ===========================================================================

// simulatePollBilling reproduces the CAS + billing logic from updateVideoSingleTask.
// It takes a persisted task (already in DB), applies the new status, and performs
// the conditional update + billing exactly as the polling loop does.
func simulatePollBilling(ctx context.Context, task *model.Task, newStatus model.TaskStatus, actualQuota int) {
	snap := task.Snapshot()

	shouldRefund := false
	shouldSettle := false
	quota := task.Quota

	task.Status = newStatus
	switch string(newStatus) {
	case model.TaskStatusSuccess:
		task.Progress = "100%"
		task.FinishTime = 9999
		shouldSettle = true
	case model.TaskStatusFailure:
		task.Progress = "100%"
		task.FinishTime = 9999
		task.FailReason = "upstream error"
		if quota != 0 {
			shouldRefund = true
		}
	default:
		task.Progress = "50%"
	}

	isDone := task.Status == model.TaskStatus(model.TaskStatusSuccess) || task.Status == model.TaskStatus(model.TaskStatusFailure)
	if isDone && snap.Status != task.Status {
		won, err := task.UpdateWithStatus(snap.Status)
		if err != nil {
			shouldRefund = false
			shouldSettle = false
		} else if !won {
			shouldRefund = false
			shouldSettle = false
		}
	} else if !snap.Equal(task.Snapshot()) {
		_, _ = task.UpdateWithStatus(snap.Status)
	}

	if shouldSettle && actualQuota > 0 {
		RecalculateTaskQuota(ctx, task, actualQuota, "test settle")
	}
	if shouldRefund {
		RefundTaskQuota(ctx, task, task.FailReason)
	}
}

func TestCASGuardedRefund_Win(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 20, 20, 20
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 6000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-refund-win", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusFailure), 0)

	// CAS wins: task in DB should now be FAILURE
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)

	// Refund should have happened
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestCASGuardedRefund_Lose(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 21, 21, 21
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 6000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-refund-lose", tokenRemain)
	seedChannel(t, channelID)

	// Create task with IN_PROGRESS in DB
	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	// Simulate another process already transitioning to FAILURE
	model.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusFailure)

	// Our process still has the old in-memory state (IN_PROGRESS) and tries to transition
	// task.Status is still IN_PROGRESS in the snapshot
	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusFailure), 0)

	// CAS lost: user quota should NOT change (no double refund)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))

	// No billing log should be created
	assert.Equal(t, int64(0), countLogs(t))
}

func TestCASGuardedSettle_Win(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 22, 22, 22
	const initQuota, preConsumed = 10000, 5000
	const actualQuota = 3000 // over-charged, should get partial refund
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-settle-win", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusSuccess), actualQuota)

	// CAS wins: task should be SUCCESS
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusSuccess, reloaded.Status)

	// Settlement should refund the over-charge (5000 - 3000 = 2000 back to user)
	assert.Equal(t, initQuota+(preConsumed-actualQuota), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	// task.Quota should be updated to actualQuota
	assert.Equal(t, actualQuota, task.Quota)
}

func TestNonTerminalUpdate_NoBilling(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID = 23, 23
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	task.Progress = "20%"
	require.NoError(t, model.DB.Create(task).Error)

	// Simulate a non-terminal poll update (still IN_PROGRESS, progress changed)
	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusInProgress), 0)

	// User quota should NOT change
	assert.Equal(t, initQuota, getUserQuota(t, userID))

	// No billing log
	assert.Equal(t, int64(0), countLogs(t))

	// Task progress should be updated in DB
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.Equal(t, "50%", reloaded.Progress)
}

// ===========================================================================
// Mock adaptor for settleTaskBillingOnComplete tests
// ===========================================================================

type mockAdaptor struct {
	adjustReturn int
}

func (m *mockAdaptor) Init(_ *relaycommon.RelayInfo) {}
func (m *mockAdaptor) FetchTask(context.Context, string, string, map[string]any, string) (*http.Response, error) {
	return nil, nil
}
func (m *mockAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) { return nil, nil }
func (m *mockAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return m.adjustReturn
}

// ===========================================================================
// PerCallBilling tests — settleTaskBillingOnComplete
// ===========================================================================

func TestSettle_PerCallBilling_SkipsAdaptorAdjust(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 30, 30, 30
	const initQuota, preConsumed = 10000, 5000
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-percall-adaptor", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.PerCallBilling = true

	adaptor := &mockAdaptor{adjustReturn: 2000}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Per-call: no adjustment despite adaptor returning 2000
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_PerCallBilling_SkipsTotalTokens(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 31, 31, 31
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 7000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-percall-tokens", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.PerCallBilling = true

	adaptor := &mockAdaptor{adjustReturn: 0}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, TotalTokens: 9999}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Per-call: no recalculation by tokens
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_NonPerCallBilling_AppliesAdaptorAdjustment(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 32, 32, 32
	const initQuota, preConsumed = 10000, 5000
	const adaptorQuota = 3000
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-nonpercall-adj", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	// PerCallBilling defaults to false
	require.NoError(t, model.DB.Create(task).Error)

	adaptor := &mockAdaptor{adjustReturn: adaptorQuota}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Non-per-call: adaptor adjustment applies (refund 2000)
	assert.Equal(t, initQuota+(preConsumed-adaptorQuota), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+(preConsumed-adaptorQuota), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, adaptorQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}
