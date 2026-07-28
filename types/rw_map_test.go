package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFromJsonStringPreservesMapOnInvalidJSON(t *testing.T) {
	values := NewRWMap[string, float64]()
	values.Set("existing", 1.5)

	require.Error(t, LoadFromJsonString(values, `{"replacement":`))
	value, ok := values.Get("existing")
	assert.True(t, ok)
	assert.Equal(t, 1.5, value)
}

func TestRWMapUnmarshalJSONPreservesMapOnInvalidJSON(t *testing.T) {
	values := NewRWMap[string, float64]()
	values.Set("existing", 1.5)

	require.Error(t, values.UnmarshalJSON([]byte(`{"replacement":`)))
	value, ok := values.Get("existing")
	assert.True(t, ok)
	assert.Equal(t, 1.5, value)
}

func TestLoadFromJsonStringWithCallbackPublishesBeforeUnlockedCallback(t *testing.T) {
	values := NewRWMap[string, float64]()
	callbackCalled := false
	callbackCouldLock := false
	var callbackValue float64
	var callbackFound bool

	require.NoError(t, LoadFromJsonStringWithCallback(values, `{"replacement":2.5}`, func() {
		callbackCalled = true
		callbackCouldLock = values.mutex.TryLock()
		if callbackCouldLock {
			values.mutex.Unlock()
		}
		callbackValue, callbackFound = values.Get("replacement")
	}))

	assert.True(t, callbackCalled)
	assert.True(t, callbackCouldLock)
	assert.True(t, callbackFound)
	assert.Equal(t, 2.5, callbackValue)
	value, ok := values.Get("replacement")
	assert.True(t, ok)
	assert.Equal(t, 2.5, value)
}
