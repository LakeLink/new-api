package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamOptionsPreserveExplicitFalseValues(t *testing.T) {
	var options StreamOptions
	require.NoError(t, common.Unmarshal([]byte(
		`{"include_usage":false,"include_obfuscation":false}`,
	), &options))

	require.NotNil(t, options.IncludeUsage)
	assert.False(t, *options.IncludeUsage)
	require.NotNil(t, options.IncludeObfuscation)
	assert.False(t, *options.IncludeObfuscation)

	encoded, err := common.Marshal(options)
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"include_usage":false,"include_obfuscation":false}`,
		string(encoded),
	)
}

func TestStreamOptionsOmitAbsentValues(t *testing.T) {
	var options StreamOptions
	require.NoError(t, common.Unmarshal([]byte(`{}`), &options))

	assert.Nil(t, options.IncludeUsage)
	assert.Nil(t, options.IncludeObfuscation)

	encoded, err := common.Marshal(options)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(encoded))
}
