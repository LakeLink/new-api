package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelPriceHelperTieredUsesPreloadedRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-test-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-test-model":"param(\"stream\") == true ? tier(\"stream\", p * 3) : tier(\"base\", p * 2)"}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/channel/test/1", nil)
	req.Body = nil
	req.ContentLength = 0
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	ctx.Set("group", "default")

	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-test-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(`{"stream":true}`),
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Equal(t, 1500, priceData.QuotaToPreConsume)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, "stream", info.TieredBillingSnapshot.EstimatedTier)
	require.Equal(t, billing_setting.BillingModeTieredExpr, info.TieredBillingSnapshot.BillingMode)
	require.Equal(t, common.QuotaPerUnit, info.TieredBillingSnapshot.QuotaPerUnit)
}

func TestHandleGroupRatio_UsesFallbackGroupForTargetPricingMode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{
		"vip": {
			"fallback": ["emergency"],
			"pricing_mode": "target"
		}
	}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{}`))
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(ctx, constant.ContextKeyFallbackSourceGroup, "vip")
	common.SetContextKey(ctx, constant.ContextKeyFallbackGroup, "emergency")

	info := &relaycommon.RelayInfo{
		UsingGroup: "vip",
		UserGroup:  "vip",
	}

	HandleGroupRatio(ctx, info)
	require.Equal(t, "emergency", info.UsingGroup)
}

func TestHandleGroupRatio_UsesFallbackSourceGroupAfterPreviousTargetMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{
		"vip": {
			"fallback": ["first", "second"],
			"pricing_mode": "target"
		}
	}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{}`))
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(ctx, constant.ContextKeyFallbackSourceGroup, "vip")
	common.SetContextKey(ctx, constant.ContextKeyFallbackGroup, "second")

	info := &relaycommon.RelayInfo{
		UsingGroup: "first",
		UserGroup:  "vip",
	}

	HandleGroupRatio(ctx, info)
	require.Equal(t, "second", info.UsingGroup)
}

func TestHandleGroupRatio_KeepsOriginalGroupForOriginPricingMode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{
		"vip": {
			"fallback": ["emergency"],
			"pricing_mode": "origin"
		}
	}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{}`))
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(ctx, constant.ContextKeyFallbackGroup, "emergency")

	info := &relaycommon.RelayInfo{
		UsingGroup: "vip",
		UserGroup:  "vip",
	}

	HandleGroupRatio(ctx, info)
	require.Equal(t, "vip", info.UsingGroup)
}

func TestHandleGroupRatio_UsesNoFallbackWhenMissingRule(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(ctx, constant.ContextKeyFallbackGroup, "emergency")

	info := &relaycommon.RelayInfo{
		UsingGroup: "vip",
		UserGroup:  "vip",
	}

	HandleGroupRatio(ctx, info)
	require.Equal(t, "vip", info.UsingGroup)
}
