Failed to create stream fd: Operation not permitted
Failed to create stream fd: Operation not permitted
Failed to create stream fd: Operation not permitted
package helper

import (
	"fmt"
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
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	fallbackRatioTestSourceGroup = "fallback-origin-test"
	fallbackRatioTestTargetGroup = "fallback-target-test"
	fallbackRatioTestUserGroup   = "fallback-user-test"
	fallbackRatioTestSourceRatio = 1.25
	fallbackRatioTestTargetRatio = 2.5
	fallbackRatioTestSourceSpec  = 0.75
	fallbackRatioTestTargetSpec  = 0.9
)

func setupFallbackRatioTest(t *testing.T, groupGroupRatio map[string]map[string]float64) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	savedGroupRatio := ratio_setting.GroupRatio2JSONString()
	savedGroupGroupRatio := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroupRatio))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(savedGroupGroupRatio))
		require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{}`))
	})

	groupRatioBytes, err := common.Marshal(map[string]float64{
		fallbackRatioTestSourceGroup: fallbackRatioTestSourceRatio,
		fallbackRatioTestTargetGroup: fallbackRatioTestTargetRatio,
	})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(groupRatioBytes)))

	if groupGroupRatio == nil {
		groupGroupRatio = map[string]map[string]float64{}
	}
	groupGroupRatioBytes, err := common.Marshal(groupGroupRatio)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(string(groupGroupRatioBytes)))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(ctx, constant.ContextKeyFallbackSourceGroup, fallbackRatioTestSourceGroup)
	common.SetContextKey(ctx, constant.ContextKeyFallbackGroup, fallbackRatioTestTargetGroup)

	info := &relaycommon.RelayInfo{
		UsingGroup: fallbackRatioTestSourceGroup,
		UserGroup:  fallbackRatioTestUserGroup,
	}
	return ctx, info
}

func updateFallbackRuleForRatioTest(t *testing.T, pricingMode string, fields string) {
	t.Helper()
	if fields != "" {
		fields = "," + fields
	}
	require.NoError(t, setting.UpdateGroupFallbackByJsonString(fmt.Sprintf(`{
		%q: {
			"fallback": [%q],
			"pricing_mode": %q%s
		}
	}`, fallbackRatioTestSourceGroup, fallbackRatioTestTargetGroup, pricingMode, fields)))
}

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

func TestHandleGroupRatio_FallbackOriginPricingSpecialRatioSwitch(t *testing.T) {
	groupGroupRatio := map[string]map[string]float64{
		fallbackRatioTestUserGroup: {
			fallbackRatioTestSourceGroup: fallbackRatioTestSourceSpec,
			fallbackRatioTestTargetGroup: fallbackRatioTestTargetSpec,
		},
	}

	t.Run("default uses source special ratio", func(t *testing.T) {
		ctx, info := setupFallbackRatioTest(t, groupGroupRatio)
		updateFallbackRuleForRatioTest(t, setting.GroupFallbackPricingModeOrigin, "")

		got := HandleGroupRatio(ctx, info)

		require.Equal(t, fallbackRatioTestSourceGroup, info.UsingGroup)
		require.True(t, got.HasSpecialRatio)
		require.Equal(t, fallbackRatioTestSourceSpec, got.GroupSpecialRatio)
		require.Equal(t, fallbackRatioTestSourceSpec, got.GroupRatio)
	})

	t.Run("disabled uses source normal ratio", func(t *testing.T) {
		ctx, info := setupFallbackRatioTest(t, groupGroupRatio)
		updateFallbackRuleForRatioTest(t, setting.GroupFallbackPricingModeOrigin, `"origin_pricing_use_special_ratio": false`)

		got := HandleGroupRatio(ctx, info)

		require.Equal(t, fallbackRatioTestSourceGroup, info.UsingGroup)
		require.False(t, got.HasSpecialRatio)
		require.Equal(t, -1.0, got.GroupSpecialRatio)
		require.Equal(t, fallbackRatioTestSourceRatio, got.GroupRatio)
	})
}

func TestHandleGroupRatio_FallbackTargetPricingRatioModes(t *testing.T) {
	bothSpecialRatios := map[string]map[string]float64{
		fallbackRatioTestUserGroup: {
			fallbackRatioTestSourceGroup: fallbackRatioTestSourceSpec,
			fallbackRatioTestTargetGroup: fallbackRatioTestTargetSpec,
		},
	}
	sourceSpecialOnly := map[string]map[string]float64{
		fallbackRatioTestUserGroup: {
			fallbackRatioTestSourceGroup: fallbackRatioTestSourceSpec,
		},
	}
	targetSpecialOnly := map[string]map[string]float64{
		fallbackRatioTestUserGroup: {
			fallbackRatioTestTargetGroup: fallbackRatioTestTargetSpec,
		},
	}
	noSpecialRatios := map[string]map[string]float64{}

	tests := []struct {
		name            string
		mode            string
		groupGroupRatio map[string]map[string]float64
		wantRatio       float64
		wantSpecial     bool
		wantSpecialVal  float64
	}{
		{
			name:            "default target_special uses target special",
			mode:            "",
			groupGroupRatio: bothSpecialRatios,
			wantRatio:       fallbackRatioTestTargetSpec,
			wantSpecial:     true,
			wantSpecialVal:  fallbackRatioTestTargetSpec,
		},
		{
			name:            "origin_special uses source special",
			mode:            setting.GroupFallbackTargetRatioModeOriginSpecial,
			groupGroupRatio: bothSpecialRatios,
			wantRatio:       fallbackRatioTestSourceSpec,
			wantSpecial:     true,
			wantSpecialVal:  fallbackRatioTestSourceSpec,
		},
		{
			name:            "origin_special falls back to target normal when source special missing",
			mode:            setting.GroupFallbackTargetRatioModeOriginSpecial,
			groupGroupRatio: targetSpecialOnly,
			wantRatio:       fallbackRatioTestTargetRatio,
		},
		{
			name:            "target_special uses target special",
			mode:            setting.GroupFallbackTargetRatioModeTargetSpecial,
			groupGroupRatio: bothSpecialRatios,
			wantRatio:       fallbackRatioTestTargetSpec,
			wantSpecial:     true,
			wantSpecialVal:  fallbackRatioTestTargetSpec,
		},
		{
			name:            "target_special falls back to target normal when target special missing",
			mode:            setting.GroupFallbackTargetRatioModeTargetSpecial,
			groupGroupRatio: sourceSpecialOnly,
			wantRatio:       fallbackRatioTestTargetRatio,
		},
		{
			name:            "normal_only ignores both special ratios",
			mode:            setting.GroupFallbackTargetRatioModeNormalOnly,
			groupGroupRatio: bothSpecialRatios,
			wantRatio:       fallbackRatioTestTargetRatio,
		},
		{
			name:            "prefer_origin_special prefers source special",
			mode:            setting.GroupFallbackTargetRatioModePreferOriginSpecial,
			groupGroupRatio: bothSpecialRatios,
			wantRatio:       fallbackRatioTestSourceSpec,
			wantSpecial:     true,
			wantSpecialVal:  fallbackRatioTestSourceSpec,
		},
		{
			name:            "prefer_origin_special uses target special when source missing",
			mode:            setting.GroupFallbackTargetRatioModePreferOriginSpecial,
			groupGroupRatio: targetSpecialOnly,
			wantRatio:       fallbackRatioTestTargetSpec,
			wantSpecial:     true,
			wantSpecialVal:  fallbackRatioTestTargetSpec,
		},
		{
			name:            "prefer_origin_special falls back to target normal when no special",
			mode:            setting.GroupFallbackTargetRatioModePreferOriginSpecial,
			groupGroupRatio: noSpecialRatios,
			wantRatio:       fallbackRatioTestTargetRatio,
		},
		{
			name:            "prefer_target_special prefers target special",
			mode:            setting.GroupFallbackTargetRatioModePreferTargetSpecial,
			groupGroupRatio: bothSpecialRatios,
			wantRatio:       fallbackRatioTestTargetSpec,
			wantSpecial:     true,
			wantSpecialVal:  fallbackRatioTestTargetSpec,
		},
		{
			name:            "prefer_target_special uses source special when target missing",
			mode:            setting.GroupFallbackTargetRatioModePreferTargetSpecial,
			groupGroupRatio: sourceSpecialOnly,
			wantRatio:       fallbackRatioTestSourceSpec,
			wantSpecial:     true,
			wantSpecialVal:  fallbackRatioTestSourceSpec,
		},
		{
			name:            "unknown mode keeps default target_special behavior",
			mode:            "unknown",
			groupGroupRatio: bothSpecialRatios,
			wantRatio:       fallbackRatioTestTargetSpec,
			wantSpecial:     true,
			wantSpecialVal:  fallbackRatioTestTargetSpec,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, info := setupFallbackRatioTest(t, tt.groupGroupRatio)
			fields := ""
			if tt.mode != "" {
				fields = fmt.Sprintf(`"target_pricing_ratio_mode": %q`, tt.mode)
			}
			updateFallbackRuleForRatioTest(t, setting.GroupFallbackPricingModeTarget, fields)

			got := HandleGroupRatio(ctx, info)

			require.Equal(t, fallbackRatioTestTargetGroup, info.UsingGroup)
			require.Equal(t, tt.wantRatio, got.GroupRatio)
			require.Equal(t, tt.wantSpecial, got.HasSpecialRatio)
			if tt.wantSpecial {
				require.Equal(t, tt.wantSpecialVal, got.GroupSpecialRatio)
			} else {
				require.Equal(t, -1.0, got.GroupSpecialRatio)
			}
		})
	}
}

func TestModelPriceHelperTieredPreConsumeMaxTokensFallback(t *testing.T) {
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
		"billing_setting.billing_mode":    `{"tiered-fallback-model":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"tiered-fallback-model":"tier(\"base\", p * 3 + c * 15)"}`,
		"group_ratio_setting.group_ratio": `{"default":1,"free":0}`,
	}))

	const promptTokens = 1000

	cases := []struct {
		name      string
		group     string
		maxTokens int
		expected  int
	}{
		{
			// max_tokens omitted in a paid group -> fall back to 8192 completion tokens.
			// p*3 + c*15 = 1000*3 + 8192*15 = 125880 -> /1e6 * 500000 = 62940
			name:      "non-free group falls back to 8192 completion tokens",
			group:     "default",
			maxTokens: 0,
			expected:  62940,
		},
		{
			// explicit max_tokens is used verbatim, no fallback.
			// 1000*3 + 100*15 = 4500 -> /1e6 * 500000 = 2250
			name:      "explicit max_tokens is used verbatim",
			group:     "default",
			maxTokens: 100,
			expected:  2250,
		},
		{
			// free group (ratio 0) stays zero; fallback is gated on non-zero group ratio.
			name:      "free group stays zero without fallback",
			group:     "free",
			maxTokens: 0,
			expected:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Header.Set("Content-Type", "application/json")
			ctx.Request = req
			ctx.Set("group", tc.group)

			info := &relaycommon.RelayInfo{
				OriginModelName: "tiered-fallback-model",
				UserGroup:       tc.group,
				UsingGroup:      tc.group,
				RequestHeaders:  map[string]string{"Content-Type": "application/json"},
				BillingRequestInput: &billingexpr.RequestInput{
					Headers: map[string]string{"Content-Type": "application/json"},
					Body:    []byte(`{}`),
				},
			}

			priceData, err := ModelPriceHelper(ctx, info, promptTokens, &types.TokenCountMeta{MaxTokens: tc.maxTokens})
			require.NoError(t, err)
			require.Equal(t, tc.expected, priceData.QuotaToPreConsume)
		})
	}
}
