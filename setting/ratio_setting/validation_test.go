package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRatioMapUpdatesRejectUnsafeValuesWithoutReplacingLiveConfig(t *testing.T) {
	original := ModelRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateModelRatioByJSONString(original)) })
	require.NoError(t, UpdateModelRatioByJSONString(`{"safe":1.5}`))

	for _, value := range []string{
		`{"negative":-0.1}`,
		`{"infinite":1e10000}`,
		`{"broken":`,
		`null`,
	} {
		require.Error(t, UpdateModelRatioByJSONString(value))
		ratio, ok, _ := GetModelRatio("safe")
		assert.True(t, ok)
		assert.Equal(t, 1.5, ratio)
	}
}

func TestGroupGroupRatioRejectsNegativeNestedValues(t *testing.T) {
	require.Error(t, CheckGroupGroupRatio(`{"vip":{"default":-1}}`))
	require.Error(t, CheckGroupGroupRatio(`{"vip":null}`))
	require.NoError(t, CheckGroupGroupRatio(`{"vip":{"default":0,"premium":1.5}}`))
}

func TestGroupRatioRejectsNull(t *testing.T) {
	require.Error(t, CheckGroupRatio(`null`))
}
