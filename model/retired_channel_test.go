package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetiredChannelsAreExcludedFromModelSelection(t *testing.T) {
	const (
		activeChannelID  = 910001
		retiredChannelID = 910002
		sharedModel      = "retired-selection-shared-model"
	)
	activePriority := int64(1)
	retiredPriority := int64(100)

	require.NoError(t, DB.Create(&Channel{
		Id:       activeChannelID,
		Type:     constant.ChannelTypeOpenAI,
		Key:      "active-key",
		Status:   common.ChannelStatusEnabled,
		Name:     "active-selection-channel",
		Priority: &activePriority,
		Models:   sharedModel,
		Group:    "retired-selection",
	}).Error)
	require.NoError(t, DB.Create(&Channel{
		Id:       retiredChannelID,
		Type:     constant.ChannelTypePaLM,
		Key:      "retired-key",
		Status:   common.ChannelStatusEnabled,
		Name:     "retired-selection-channel",
		Priority: &retiredPriority,
		Models:   sharedModel,
		Group:    "retired-selection",
	}).Error)
	require.NoError(t, DB.Create([]Ability{
		{Group: "retired-selection", Model: sharedModel, ChannelId: activeChannelID, Enabled: true, Priority: &activePriority},
		{Group: "retired-selection", Model: sharedModel, ChannelId: retiredChannelID, Enabled: true, Priority: &retiredPriority},
	}).Error)

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	t.Cleanup(func() {
		require.NoError(t, DB.Where("channel_id IN ?", []int{activeChannelID, retiredChannelID}).Delete(&Ability{}).Error)
		require.NoError(t, DB.Where("id IN ?", []int{activeChannelID, retiredChannelID}).Delete(&Channel{}).Error)
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		InitChannelCache()
	})

	common.MemoryCacheEnabled = false
	selected, err := GetChannel("retired-selection", sharedModel, 0, "")
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, activeChannelID, selected.Id)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", retiredChannelID).Update("status", common.ChannelStatusManuallyDisabled).Error)
	common.MemoryCacheEnabled = true
	InitChannelCache()
	assert.False(t, UpdateChannelStatus(retiredChannelID, "", common.ChannelStatusEnabled, "test enable retired channel"))
	var retired Channel
	require.NoError(t, DB.First(&retired, "id = ?", retiredChannelID).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, retired.Status)
	selected, err = GetRandomSatisfiedChannel("retired-selection", sharedModel, 0, "")
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, activeChannelID, selected.Id)
}
