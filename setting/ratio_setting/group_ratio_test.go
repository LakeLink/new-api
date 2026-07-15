package ratio_setting

import (
	"testing"

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
