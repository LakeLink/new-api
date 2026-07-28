package service

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSynchronousBillingFinalizationIsExactlyOnceForEveryDelta(t *testing.T) {
	tests := []struct {
		name        string
		actualQuota int
	}{
		{name: "positive delta", actualQuota: 130},
		{name: "negative delta", actualQuota: 70},
		{name: "verified zero", actualQuota: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupDurableBillingSessionTest(t)
			user, token := createDurableBillingBalances(t, db, "finalization-"+test.name)
			channel := model.Channel{Name: "finalization-" + test.name, Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(&channel).Error)
			requestID := "sync-finalization-" + test.name
			info := &relaycommon.RelayInfo{
				RequestId:       requestID,
				UserId:          user.Id,
				TokenId:         token.Id,
				TokenKey:        token.Key,
				OriginModelName: "sync-finalization-model",
				UsingGroup:      "default",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channel.Id},
			}
			session := &BillingSession{
				relayInfo:        info,
				funding:          &WalletFunding{userId: user.Id, consumed: 100},
				preConsumedQuota: 100,
				tokenConsumed:    100,
			}
			info.Billing = session
			ctx, _ := gin.CreateTestContext(nil)

			logSnapshot := model.TaskBillingFinalizationLog{
				Content:           "durable synchronous usage",
				ModelName:         info.OriginModelName,
				PromptTokens:      17,
				CompletionTokens:  13,
				TokenName:         "snapshot-token-name",
				UseTime:           9,
				IsStream:          true,
				IP:                "203.0.113.8",
				UpstreamRequestID: "upstream-request-123",
				Username:          "snapshot-user-name",
				Other:             map[string]interface{}{"billing_path": "test"},
				NodeName:          "snapshot-node",
				CreatedAt:         1_700_000_000,
			}
			require.NoError(t, FinalizeBilling(ctx, info, test.actualQuota, test.actualQuota, test.actualQuota, true, logSnapshot))
			require.NoError(t, FinalizeBilling(ctx, info, test.actualQuota, test.actualQuota, test.actualQuota, true, logSnapshot))

			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			assert.Equal(t, 1_000-test.actualQuota, user.Quota)
			assert.Equal(t, test.actualQuota, user.UsedQuota)
			assert.Equal(t, 1, user.RequestCount, "a verified zero-cost request must still be counted once")
			assert.Equal(t, 500-test.actualQuota, token.RemainQuota)
			assert.Equal(t, test.actualQuota, token.UsedQuota)
			assert.Equal(t, int64(test.actualQuota), channel.UsedQuota)

			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			log := logs[0]
			assert.Equal(t, requestID, log.RequestId)
			require.NotNil(t, log.BillingEventId)
			assert.Equal(t, model.BillingAdjustmentTaskID(requestID, model.BillingAdjustmentSettle), *log.BillingEventId)
			assert.NotEqual(t, log.RequestId, *log.BillingEventId, "the HTTP request identity and billing-event identity serve different contracts")
			assert.Equal(t, "upstream-request-123", log.UpstreamRequestId)
			assert.Equal(t, 17, log.PromptTokens)
			assert.Equal(t, 13, log.CompletionTokens)
			assert.Equal(t, "snapshot-token-name", log.TokenName)
			assert.Equal(t, "snapshot-user-name", log.Username)
			assert.Equal(t, 9, log.UseTime)
			assert.True(t, log.IsStream)
			assert.Equal(t, "203.0.113.8", log.Ip)
		})
	}
}

func TestSynchronousBillingFinalizationExportsTokenUsageOnce(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user, token := createDurableBillingBalances(t, db, "finalization-export")
	channel := model.Channel{Name: "finalization-export", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	oldDataExportEnabled := common.DataExportEnabled
	common.DataExportEnabled = true
	t.Cleanup(func() {
		common.DataExportEnabled = oldDataExportEnabled
	})

	info := &relaycommon.RelayInfo{
		RequestId:       "sync-finalization-export",
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		OriginModelName: "sync-finalization-export-model",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channel.Id},
	}
	info.Billing = &BillingSession{
		relayInfo:        info,
		funding:          &WalletFunding{userId: user.Id, consumed: 100},
		preConsumedQuota: 100,
		tokenConsumed:    100,
	}
	ctx, _ := gin.CreateTestContext(nil)
	logSnapshot := model.TaskBillingFinalizationLog{
		ModelName:        info.OriginModelName,
		PromptTokens:     21,
		CompletionTokens: 34,
		Username:         "export-user",
		NodeName:         "export-node",
	}
	require.NoError(t, FinalizeBilling(ctx, info, 100, 100, 100, true, logSnapshot))
	require.NoError(t, FinalizeBilling(ctx, info, 100, 100, 100, true, logSnapshot))

	eventID := model.BillingAdjustmentTaskID(info.RequestId, model.BillingAdjustmentSettle)
	var exported []model.QuotaData
	require.NoError(t, db.Where("billing_event_id = ?", eventID).Find(&exported).Error)
	require.Len(t, exported, 1)
	assert.Equal(t, 1, exported[0].Count)
	assert.Equal(t, 55, exported[0].TokenUsed)
	assert.Equal(t, 100, exported[0].Quota)
	assert.Equal(t, "export-node", exported[0].NodeName)
}

func TestSynchronousBillingFinalizationLogsSubscriptionOverflowResult(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user := model.User{Username: "finalization-subscription", Password: "password", Quota: 200, AffCode: "aff-finalization-subscription"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "token-finalization-subscription", RemainQuota: 490, UsedQuota: 10}
	require.NoError(t, db.Create(&token).Error)
	subscription := model.UserSubscription{
		UserId:              user.Id,
		AmountTotal:         100,
		AmountUsed:          90,
		AllowWalletOverflow: true,
	}
	require.NoError(t, db.Create(&subscription).Error)
	channel := model.Channel{Name: "finalization-subscription", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	const requestID = "sync-finalization-subscription-overflow"
	require.NoError(t, db.Create(&model.SubscriptionPreConsumeRecord{
		RequestId:          requestID,
		UserId:             user.Id,
		UserSubscriptionId: subscription.Id,
		PreConsumed:        10,
		Status:             "consumed",
	}).Error)

	info := &relaycommon.RelayInfo{
		RequestId:                             requestID,
		UserId:                                user.Id,
		TokenId:                               token.Id,
		TokenKey:                              token.Key,
		OriginModelName:                       "sync-finalization-subscription-model",
		UsingGroup:                            "default",
		BillingSource:                         BillingSourceSubscription,
		SubscriptionId:                        subscription.Id,
		SubscriptionPreConsumed:               10,
		SubscriptionAmountTotal:               100,
		SubscriptionAmountUsedAfterPreConsume: 90,
		UserSetting:                           dto.UserSetting{QuotaWarningThreshold: -1},
		UserQuota:                             1_000,
		ChannelMeta:                           &relaycommon.ChannelMeta{ChannelId: channel.Id},
	}
	info.Billing = &BillingSession{
		relayInfo: info,
		funding: &SubscriptionFunding{
			requestId:       requestID,
			userId:          user.Id,
			subscriptionId:  subscription.Id,
			preConsumed:     10,
			AmountTotal:     100,
			AmountUsedAfter: 90,
		},
		preConsumedQuota: 10,
		tokenConsumed:    10,
	}
	other := map[string]interface{}{}
	appendBillingInfo(info, other)
	ctx, _ := gin.CreateTestContext(nil)
	require.NoError(t, FinalizeBilling(ctx, info, 40, 40, 40, true, model.TaskBillingFinalizationLog{
		ModelName: info.OriginModelName,
		Other:     other,
	}))

	var log model.Log
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&log).Error)
	var loggedOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &loggedOther))
	assert.Equal(t, float64(10), loggedOther["subscription_post_delta"])
	assert.Equal(t, float64(20), loggedOther["subscription_wallet_overflow"])
	assert.Equal(t, float64(20), loggedOther["wallet_quota_deducted"])
	assert.Equal(t, float64(20), loggedOther["subscription_consumed"])
	assert.Equal(t, float64(100), loggedOther["subscription_total"])
	assert.Equal(t, float64(100), loggedOther["subscription_used"])
	assert.Equal(t, float64(0), loggedOther["subscription_remain"])
}

func TestSynchronousBillingFinalizationFailureChangesRefundStateOnlyAfterEnqueue(t *testing.T) {
	for _, processFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("process_failure_%t", processFailure), func(t *testing.T) {
			db := setupDurableBillingSessionTest(t)
			user, token := createDurableBillingBalances(t, db, fmt.Sprintf("finalization-failure-%t", processFailure))
			channel := model.Channel{Name: "finalization-failure", Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(&channel).Error)
			info := &relaycommon.RelayInfo{
				RequestId:       fmt.Sprintf("sync-finalization-failure-%t", processFailure),
				UserId:          user.Id,
				TokenId:         token.Id,
				TokenKey:        token.Key,
				OriginModelName: "sync-finalization-failure-model",
				UsingGroup:      "default",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channel.Id},
			}
			session := &BillingSession{
				relayInfo:        info,
				funding:          &WalletFunding{userId: user.Id, consumed: 100},
				preConsumedQuota: 100,
				tokenConsumed:    100,
			}
			info.Billing = session
			if processFailure {
				require.NoError(t, db.Migrator().DropTable(&model.Token{}))
			} else {
				require.NoError(t, db.Migrator().DropTable(&model.TaskBillingFinalization{}))
			}
			ctx, _ := gin.CreateTestContext(nil)
			err := FinalizeBilling(ctx, info, 130, 130, 130, true, model.TaskBillingFinalizationLog{ModelName: info.OriginModelName})
			require.Error(t, err)
			if processFailure {
				assert.False(t, session.NeedsRefund(), "a persisted outbox must own retry after processing fails")
				var count int64
				require.NoError(t, db.Model(&model.TaskBillingFinalization{}).Count(&count).Error)
				assert.Equal(t, int64(1), count)
			} else {
				assert.True(t, session.NeedsRefund(), "an enqueue failure leaves no durable settlement and remains refundable")
			}
		})
	}
}
