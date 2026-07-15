package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONObjectScannersAcceptDriverRepresentations(t *testing.T) {
	t.Run("channel info from string", func(t *testing.T) {
		value := ChannelInfo{IsMultiKey: true}
		require.NoError(t, value.Scan(`{"is_multi_key":false,"multi_key_size":3}`))
		assert.False(t, value.IsMultiKey)
		assert.Equal(t, 3, value.MultiKeySize)
	})

	t.Run("task properties from bytes", func(t *testing.T) {
		var value Properties
		require.NoError(t, value.Scan([]byte(`{"input":"prompt","origin_model_name":"model"}`)))
		assert.Equal(t, "prompt", value.Input)
		assert.Equal(t, "model", value.OriginModelName)
	})

	t.Run("task private data from string", func(t *testing.T) {
		var value TaskPrivateData
		require.NoError(t, value.Scan(`{"upstream_task_id":"upstream","subscription_id":42}`))
		assert.Equal(t, "upstream", value.UpstreamTaskID)
		assert.Equal(t, 42, value.SubscriptionId)
	})
}

func TestJSONObjectScannersResetForNullAndEmptyValues(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "nil", value: nil},
		{name: "empty bytes", value: []byte{}},
		{name: "blank string", value: "  \n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channelInfo := ChannelInfo{IsMultiKey: true, MultiKeySize: 2}
			require.NoError(t, channelInfo.Scan(test.value))
			assert.Equal(t, ChannelInfo{}, channelInfo)

			properties := Properties{Input: "stale"}
			require.NoError(t, properties.Scan(test.value))
			assert.Equal(t, Properties{}, properties)

			privateData := TaskPrivateData{Key: "stale"}
			require.NoError(t, privateData.Scan(test.value))
			assert.Equal(t, TaskPrivateData{}, privateData)
		})
	}
}

func TestJSONObjectScannersRejectInvalidDriverValues(t *testing.T) {
	var value Properties
	assert.Error(t, value.Scan(123))
	assert.Error(t, value.Scan("not-json"))
}
