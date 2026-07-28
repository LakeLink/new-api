package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestCheckGroupRatioRejectsNonFiniteAndNegativeValues(t *testing.T) {
	for _, input := range []string{
		`{"default":-0.1}`,
		`{"default":1e10000}`,
	} {
		require.Error(t, CheckGroupRatio(input))
	}
	require.NoError(t, CheckGroupRatio(`{"free":0,"default":1,"premium":2.5}`))
}

func TestRegisteredGroupRatioReplacementIsVisibleToLegacyAccessors(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"group_ratio_setting.group_ratio":       `{"default":1,"free":0}`,
		"group_ratio_setting.group_group_ratio": `{"vip":{"default":0.5}}`,
	}))

	require.Equal(t, 0.0, GetGroupRatio("free"))
	require.True(t, ContainsGroupRatio("free"))
	ratio, ok := GetGroupGroupRatio("vip", "default")
	require.True(t, ok)
	require.Equal(t, 0.5, ratio)
}
