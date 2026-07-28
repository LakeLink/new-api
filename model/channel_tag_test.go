package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBatchSetChannelTagUpdatesAbilitiesWithinTransaction(t *testing.T) {
	truncateTables(t)

	oldTag := "old-tag"
	newTag := "new-tag"
	channel := &Channel{
		Type:   constant.ChannelTypeOpenAI,
		Name:   "tagged-channel",
		Status: common.ChannelStatusEnabled,
		Group:  "default",
		Models: "tagged-model",
		Tag:    &oldTag,
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.UpdateAbilities(nil))

	require.NoError(t, BatchSetChannelTag([]int{channel.Id}, &newTag))

	var ability Ability
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	require.NotNil(t, ability.Tag)
	assert.Equal(t, newTag, *ability.Tag)
}

func TestCleanupChannelPollingLocksPreservesStateOnQueryError(t *testing.T) {
	channelPollingLocks.Range(func(key, _ any) bool {
		channelPollingLocks.Delete(key)
		return true
	})
	original := GetChannelPollingLock(987654)

	callbackName := "inject_polling_lock_cleanup_query_failure"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			tx.AddError(errors.New("injected polling lock query failure"))
		},
	))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Query().Remove(callbackName))
		channelPollingLocks.Delete(987654)
	})

	CleanupChannelPollingLocks()

	assert.Same(t, original, GetChannelPollingLock(987654))
}
