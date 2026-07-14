package service

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupDurableBillingSessionTest(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldRedisClient := common.RDB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", url.QueryEscape(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.UserSubscription{}, &model.SystemTask{}))
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.RDB = oldRedisClient
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func createDurableBillingBalances(t *testing.T, db *gorm.DB, suffix string) (model.User, model.Token) {
	t.Helper()
	user := model.User{Username: "billing-" + suffix, Password: "password", Quota: 900, AffCode: "aff-" + suffix}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "token-" + suffix, RemainQuota: 400, UsedQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	return user, token
}

func TestBillingSessionSettlePersistsAtomicAdjustment(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user, token := createDurableBillingBalances(t, db, "settle")
	info := &relaycommon.RelayInfo{
		RequestId: "relay-settle-request",
		UserId:    user.Id,
		TokenId:   token.Id,
		TokenKey:  token.Key,
	}
	session := &BillingSession{
		relayInfo:        info,
		funding:          &WalletFunding{userId: user.Id, consumed: 100},
		preConsumedQuota: 100,
		tokenConsumed:    100,
	}

	require.NoError(t, session.Settle(130))
	require.NoError(t, session.Settle(130))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 870, user.Quota)
	assert.Equal(t, 370, token.RemainQuota)
	assert.Equal(t, 130, token.UsedQuota)
	assert.False(t, session.NeedsRefund())
}

func TestBillingSessionSettleReportsSubscriptionWalletOverflow(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user := model.User{Username: "billing-session-overflow", Password: "password", Quota: 200, AffCode: "aff-session-overflow"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "token-session-overflow", RemainQuota: 490, UsedQuota: 10}
	require.NoError(t, db.Create(&token).Error)
	subscription := model.UserSubscription{
		UserId:              user.Id,
		AmountTotal:         100,
		AmountUsed:          90,
		AllowWalletOverflow: true,
	}
	require.NoError(t, db.Create(&subscription).Error)
	info := &relaycommon.RelayInfo{
		RequestId:                             "relay-subscription-overflow-request",
		UserId:                                user.Id,
		TokenId:                               token.Id,
		TokenKey:                              token.Key,
		BillingSource:                         BillingSourceSubscription,
		SubscriptionId:                        subscription.Id,
		SubscriptionPreConsumed:               10,
		SubscriptionAmountTotal:               100,
		SubscriptionAmountUsedAfterPreConsume: 90,
	}
	session := &BillingSession{
		relayInfo:        info,
		funding:          &SubscriptionFunding{subscriptionId: subscription.Id, preConsumed: 10},
		preConsumedQuota: 10,
		tokenConsumed:    10,
	}

	require.NoError(t, session.Settle(40))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 180, user.Quota)
	assert.Equal(t, int64(100), subscription.AmountUsed)
	assert.Equal(t, 460, token.RemainQuota)
	assert.Equal(t, 40, token.UsedQuota)
	assert.Equal(t, int64(10), info.SubscriptionPostDelta)
	assert.Equal(t, 20, info.SubscriptionWalletOverflow)

	other := map[string]interface{}{}
	appendBillingInfo(info, other)
	assert.Equal(t, int64(20), other["subscription_consumed"])
	assert.Equal(t, 20, other["subscription_wallet_overflow"])
	assert.Equal(t, 20, other["wallet_quota_deducted"])
}

func TestBillingSessionRefundIsSynchronousAndIdempotent(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user, token := createDurableBillingBalances(t, db, "refund")
	info := &relaycommon.RelayInfo{
		RequestId: "relay-refund-request",
		UserId:    user.Id,
		TokenId:   token.Id,
		TokenKey:  token.Key,
	}
	session := &BillingSession{
		relayInfo:        info,
		funding:          &WalletFunding{userId: user.Id, consumed: 100},
		preConsumedQuota: 100,
		tokenConsumed:    100,
	}
	c, _ := gin.CreateTestContext(nil)

	session.Refund(c)
	session.Refund(c)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 1_000, user.Quota)
	assert.Equal(t, 500, token.RemainQuota)
	assert.Equal(t, 0, token.UsedQuota)
	assert.False(t, session.NeedsRefund())
}

func TestBillingSessionFailedImmediateSettleRemainsDurableWithoutRefundRace(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user := model.User{Username: "billing-pending", Password: "password", Quota: 900, AffCode: "aff-pending"}
	require.NoError(t, db.Create(&user).Error)
	info := &relaycommon.RelayInfo{
		RequestId: "relay-pending-request",
		UserId:    user.Id,
		TokenId:   999_999,
	}
	session := &BillingSession{
		relayInfo:        info,
		funding:          &WalletFunding{userId: user.Id, consumed: 100},
		preConsumedQuota: 100,
		tokenConsumed:    100,
	}
	require.NoError(t, db.Migrator().DropTable(&model.Token{}))

	err := session.Settle(130)
	require.ErrorContains(t, err, "queued for retry")
	assert.False(t, session.NeedsRefund())
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 900, user.Quota)
	var task model.SystemTask
	require.NoError(t, db.Where("type = ?", model.SystemTaskTypeBillingAdjustment).First(&task).Error)
	assert.Equal(t, model.SystemTaskStatusPending, task.Status)
}
