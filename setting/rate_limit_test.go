package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateModelRequestRateLimitGroupReplacesConfiguredLimits(t *testing.T) {
	ModelRequestRateLimitMutex.Lock()
	original := ModelRequestRateLimitGroup
	ModelRequestRateLimitGroup = map[string][2]int{"stale": {1, 1}}
	ModelRequestRateLimitMutex.Unlock()
	t.Cleanup(func() {
		ModelRequestRateLimitMutex.Lock()
		ModelRequestRateLimitGroup = original
		ModelRequestRateLimitMutex.Unlock()
	})

	require.NoError(t, UpdateModelRequestRateLimitGroupByJSONString(`{"default":[20,15]}`))
	total, success, found := GetGroupRateLimit("default")
	assert.True(t, found)
	assert.Equal(t, 20, total)
	assert.Equal(t, 15, success)
	_, _, staleFound := GetGroupRateLimit("stale")
	assert.False(t, staleFound)

	require.Error(t, UpdateModelRequestRateLimitGroupByJSONString(`{"broken":`))
	total, success, found = GetGroupRateLimit("default")
	assert.True(t, found)
	assert.Equal(t, 20, total)
	assert.Equal(t, 15, success)

	require.Error(t, UpdateModelRequestRateLimitGroupByJSONString(`{"default":[-1,0]}`))
	total, success, found = GetGroupRateLimit("default")
	assert.True(t, found)
	assert.Equal(t, 20, total)
	assert.Equal(t, 15, success)
}

func TestJSONSettingUpdatesPreserveLastGoodValueOnDecodeFailure(t *testing.T) {
	userUsableGroupsMutex.Lock()
	originalGroups := userUsableGroups
	userUsableGroups = map[string]string{"default": "Default"}
	userUsableGroupsMutex.Unlock()
	originalChats := Chats
	Chats = []map[string]string{{"client": "url"}}
	autoGroupsMutex.Lock()
	originalAutoGroups := autoGroups
	autoGroups = []string{"default"}
	autoGroupsMutex.Unlock()
	t.Cleanup(func() {
		userUsableGroupsMutex.Lock()
		userUsableGroups = originalGroups
		userUsableGroupsMutex.Unlock()
		Chats = originalChats
		autoGroupsMutex.Lock()
		autoGroups = originalAutoGroups
		autoGroupsMutex.Unlock()
	})

	require.Error(t, UpdateUserUsableGroupsByJSONString(`{"broken":`))
	assert.Equal(t, map[string]string{"default": "Default"}, GetUserUsableGroupsCopy())
	require.Error(t, UpdateChatsByJsonString(`[{"broken":`))
	assert.Equal(t, []map[string]string{{"client": "url"}}, Chats)
	require.Error(t, UpdateAutoGroupsByJsonString(`["broken"`))
	assert.Equal(t, []string{"default"}, GetAutoGroups())
}
