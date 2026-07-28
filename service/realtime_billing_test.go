package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type realtimeReserveBilling struct {
	reserved int
	targets  []int
}

func (b *realtimeReserveBilling) Settle(_ int) error       { return nil }
func (b *realtimeReserveBilling) Refund(_ *gin.Context)    {}
func (b *realtimeReserveBilling) NeedsRefund() bool        { return false }
func (b *realtimeReserveBilling) GetPreConsumedQuota() int { return b.reserved }
func (b *realtimeReserveBilling) Reserve(target int) error {
	b.targets = append(b.targets, target)
	b.reserved = target
	return nil
}

func TestPreWssConsumeQuotaExtendsCumulativeReservationWithoutDirectCharge(t *testing.T) {
	originalModelRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-realtime":2}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModelRatios))
	})
	db := setupDurableBillingSessionTest(t)
	user, token := createDurableBillingBalances(t, db, "realtime-reserve")
	require.NoError(t, db.Model(&user).Update("quota", 100_000).Error)
	require.NoError(t, db.Model(&token).Update("remain_quota", 100_000).Error)
	billing := &realtimeReserveBilling{reserved: 10}
	info := &relaycommon.RelayInfo{
		RequestId:       "realtime-reserve-request",
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		OriginModelName: "gpt-realtime",
		UsingGroup:      "default",
		UserGroup:       "default",
		Billing:         billing,
	}
	usage := &dto.RealtimeUsage{
		TotalTokens: 100,
		InputTokens: 100,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens: 100,
		},
	}
	ctx, _ := gin.CreateTestContext(nil)

	require.NoError(t, PreWssConsumeQuota(ctx, info, usage))
	require.Len(t, billing.targets, 1)
	expected, clamp := calculateAudioQuota(QuotaInfo{
		InputDetails: TokenDetails{TextTokens: 100},
		ModelName:    "gpt-realtime",
		ModelRatio:   2,
		GroupRatio:   1,
	})
	require.Nil(t, clamp)
	assert.Equal(t, expected, billing.targets[0])

	// Reprocessing the same cumulative usage is a no-op, rather than another
	// direct wallet/token charge.
	require.NoError(t, PreWssConsumeQuota(ctx, info, usage))
	assert.Len(t, billing.targets, 1)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 100_000, user.Quota)
	assert.Equal(t, 100_000, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)

	common.SetContextKey(ctx, constant.ContextKeyAutoGroup, 42)
	err := PreWssConsumeQuota(ctx, info, usage)
	require.ErrorContains(t, err, "invalid automatic billing group")
	assert.Len(t, billing.targets, 1)
}
