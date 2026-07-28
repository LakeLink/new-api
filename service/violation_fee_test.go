package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViolationFeeFinalizationIsExactlyOnce(t *testing.T) {
	truncate(t)

	settings := model_setting.GetGrokSettings()
	originalSettings := *settings
	settings.ViolationDeductionEnabled = true
	settings.ViolationDeductionAmount = 0.001
	t.Cleanup(func() {
		*settings = originalSettings
	})

	const userID, tokenID, channelID = 116, 116, 116
	const initialUserQuota, initialTokenQuota = 10_000, 5_000
	seedUser(t, userID, initialUserQuota)
	seedToken(t, tokenID, userID, "sk-violation-finalization", initialTokenQuota)
	seedChannel(t, channelID)

	info := &relaycommon.RelayInfo{
		RequestId:       "violation-finalization",
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "sk-violation-finalization",
		OriginModelName: "grok-test",
		UsingGroup:      "default",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
	}
	info.PriceData.GroupRatioInfo.GroupRatio = 1
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, info.RequestId)
	c.Set("username", "test_user")
	c.Set("token_name", "test_token")
	apiErr := types.NewError(errors.New(CSAMViolationMarker), types.ErrorCodeViolationFeeGrokCSAM)

	require.True(t, ChargeViolationFeeIfNeeded(c, info, apiErr))
	info.StartTime = info.StartTime.Add(-time.Second)
	require.True(t, ChargeViolationFeeIfNeeded(c, info, apiErr))

	feeQuota, clamp := calcViolationFeeQuota(settings.ViolationDeductionAmount, 1)
	require.Nil(t, clamp)
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Equal(t, initialUserQuota-feeQuota, user.Quota)
	assert.Equal(t, feeQuota, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	var token model.Token
	require.NoError(t, model.DB.First(&token, tokenID).Error)
	assert.Equal(t, initialTokenQuota-feeQuota, token.RemainQuota)
	assert.Equal(t, feeQuota, token.UsedQuota)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	assert.Equal(t, int64(feeQuota), channel.UsedQuota)
	assert.Equal(t, int64(1), countLogs(t))
	var finalizationCount int64
	require.NoError(t, model.DB.Model(&model.TaskBillingFinalization{}).Count(&finalizationCount).Error)
	assert.Equal(t, int64(1), finalizationCount)
}
