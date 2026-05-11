package setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestUpdateGroupFallbackByJsonString_StoresRulesAndRoundTrips(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupFallbackByJsonString(`{}`))
	})

	err := UpdateGroupFallbackByJsonString(`{
		"vip": {
			"fallback": ["default", "enterprise"],
			"pricing_mode": "origin",
			"origin_pricing_use_special_ratio": false,
			"target_pricing_ratio_mode": "prefer_origin_special"
		},
		"guest": {
			"fallback": ["standard"],
			"pricing_mode": "target"
		}
	}`)
	require.NoError(t, err)

	rule, ok := GetGroupFallback("vip")
	require.True(t, ok)
	require.Equal(t, []string{"default", "enterprise"}, rule.Fallback)
	require.Equal(t, "origin", rule.PricingMode)
	require.NotNil(t, rule.OriginPricingUseSpecialRatio)
	require.False(t, *rule.OriginPricingUseSpecialRatio)
	require.Equal(t, GroupFallbackTargetRatioModePreferOriginSpecial, rule.TargetPricingRatioMode)

	other, ok := GetGroupFallback("guest")
	require.True(t, ok)
	require.Equal(t, []string{"standard"}, other.Fallback)
	require.Equal(t, "target", other.PricingMode)
	require.Nil(t, other.OriginPricingUseSpecialRatio)
	require.Empty(t, other.TargetPricingRatioMode)

	got := GroupFallback2JSONString()
	var parsed map[string]GroupFallbackRule
	require.NoError(t, common.Unmarshal([]byte(got), &parsed))

	require.Len(t, parsed, 2)
	require.Equal(t, []string{"default", "enterprise"}, parsed["vip"].Fallback)
	require.Equal(t, "origin", parsed["vip"].PricingMode)
	require.NotNil(t, parsed["vip"].OriginPricingUseSpecialRatio)
	require.False(t, *parsed["vip"].OriginPricingUseSpecialRatio)
	require.Equal(t, GroupFallbackTargetRatioModePreferOriginSpecial, parsed["vip"].TargetPricingRatioMode)
	require.Equal(t, []string{"standard"}, parsed["guest"].Fallback)
	require.Equal(t, "target", parsed["guest"].PricingMode)
	require.Nil(t, parsed["guest"].OriginPricingUseSpecialRatio)
	require.Empty(t, parsed["guest"].TargetPricingRatioMode)
}

func TestGroupFallbackRule_DefaultSpecialRatioSwitches(t *testing.T) {
	rule := GroupFallbackRule{}
	require.True(t, rule.ShouldUseOriginPricingSpecialRatio())
	require.Equal(t, GroupFallbackTargetRatioModeTargetSpecial, rule.EffectiveTargetPricingRatioMode())

	useOriginSpecial := false
	rule.OriginPricingUseSpecialRatio = &useOriginSpecial
	require.False(t, rule.ShouldUseOriginPricingSpecialRatio())

	rule.TargetPricingRatioMode = "unknown"
	require.Equal(t, GroupFallbackTargetRatioModeTargetSpecial, rule.EffectiveTargetPricingRatioMode())

	rule.TargetPricingRatioMode = GroupFallbackTargetRatioModePreferTargetSpecial
	require.Equal(t, GroupFallbackTargetRatioModePreferTargetSpecial, rule.EffectiveTargetPricingRatioMode())
}

func TestUpdateGroupFallbackByJsonString_ResetsStateOnInvalidJSON(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupFallbackByJsonString(`{}`))
	})

	require.NoError(t, UpdateGroupFallbackByJsonString(`{"vip":{"fallback":["default"],"pricing_mode":"target"}}`))
	original, ok := GetGroupFallback("vip")
	require.True(t, ok)

	err := UpdateGroupFallbackByJsonString(`{invalid_json`)
	require.Error(t, err)

	restored, ok := GetGroupFallback("vip")
	require.True(t, ok)
	require.Equal(t, original, restored)
}

func TestUpdateGroupFallbackByJsonString_RejectsInvalidPayloadType(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupFallbackByJsonString(`{}`))
	})

	err := UpdateGroupFallbackByJsonString(`{
		"vip": {
			"fallback": "not-an-array",
			"pricing_mode": "target"
		}
	}`)
	require.Error(t, err)
	_, ok := GetGroupFallback("vip")
	require.False(t, ok)

	err = UpdateGroupFallbackByJsonString(`{
		"vip": {
			"fallback": [1]
		}
	}`)
	require.Error(t, err)
	_, ok = GetGroupFallback("vip")
	require.False(t, ok)
}

func TestUpdateGroupFallbackByJsonString_ReplacesEntireMap(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupFallbackByJsonString(`{}`))
	})

	require.NoError(t, UpdateGroupFallbackByJsonString(`{"vip":{"fallback":["default"],"pricing_mode":"target"}}`))
	require.NoError(t, UpdateGroupFallbackByJsonString(`{"standard":{"fallback":["guest"],"pricing_mode":"origin"}}`))

	_, ok := GetGroupFallback("vip")
	require.False(t, ok)
	standardRule, ok := GetGroupFallback("standard")
	require.True(t, ok)
	require.Equal(t, []string{"guest"}, standardRule.Fallback)
	require.Equal(t, "origin", standardRule.PricingMode)
}

func TestUpdateGroupFallbackByJsonString_BlankInputClearsRules(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupFallbackByJsonString(`{}`))
	})

	require.NoError(t, UpdateGroupFallbackByJsonString(`{"vip":{"fallback":["default"],"pricing_mode":"target"}}`))

	require.NoError(t, UpdateGroupFallbackByJsonString(`   `))

	_, ok := GetGroupFallback("vip")
	require.False(t, ok)
	require.Equal(t, `{}`, GroupFallback2JSONString())
}
