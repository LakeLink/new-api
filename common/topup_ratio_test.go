package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateTopupGroupRatioValidatesBeforeReplacingCurrentConfig(t *testing.T) {
	original := TopupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateTopupGroupRatioByJSONString(original))
	})

	require.NoError(t, UpdateTopupGroupRatioByJSONString(`{"default":1,"vip":1.25}`))
	assert.Equal(t, 1.25, GetTopupGroupRatio("vip"))

	for _, invalid := range []string{
		`{"default":0}`,
		`{"default":-1}`,
		`{"default":1e10000}`,
		`{"default":`,
	} {
		require.Error(t, UpdateTopupGroupRatioByJSONString(invalid))
		assert.Equal(t, 1.25, GetTopupGroupRatio("vip"))
	}
}
