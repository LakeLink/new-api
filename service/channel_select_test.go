package service

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupFallbackTestChannels(t *testing.T, channels ...*model.Channel) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))

	for _, channel := range channels {
		channel := channel
		require.NoError(t, model.DB.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(nil))
		channelID := channel.Id

		t.Cleanup(func() {
			model.DB.Delete(&model.Channel{}, channelID)
			model.DB.Delete(&model.Ability{}, "channel_id = ?", channelID)
		})
	}
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{}`))
	})
}

func newChannelForSelection(id int, group string, models string) *model.Channel {
	priority := int64(0)
	weight := uint(10)
	return &model.Channel{
		Id:     id,
		Name:   fmt.Sprintf("test-channel-%d", id),
		Key:    fmt.Sprintf("sk-test-%d", id),
		Status: common.ChannelStatusEnabled,
		Group:  group,
		Models: models,
		// keep zero values explicit for deterministic behavior in GetChannel
		Priority: &priority,
		Weight:   &weight,
	}
}

func TestCacheGetRandomSatisfiedChannel_UsesFallbackGroup(t *testing.T) {
	setupFallbackTestChannels(t,
		newChannelForSelection(8101, "default", "gpt-4"),
	)
	require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{"vip":{"fallback":["default"],"pricing_mode":"target"}}`))

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	common.SetContextKey(ctx, constant.ContextKeyFallbackGroup, "stale")
	common.SetContextKey(ctx, constant.ContextKeyFallbackSourceGroup, "stale")

	ch, selected, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:        ctx,
		TokenGroup: "vip",
		ModelName:  "gpt-4",
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.Equal(t, "default", selected)
	require.Equal(t, "default", common.GetContextKeyString(ctx, constant.ContextKeyFallbackGroup))
	require.Equal(t, "vip", common.GetContextKeyString(ctx, constant.ContextKeyFallbackSourceGroup))
}

func TestCacheGetRandomSatisfiedChannel_FollowsFallbackChainOrder(t *testing.T) {
	setupFallbackTestChannels(t,
		newChannelForSelection(8201, "enterprise", "gpt-4"),
	)
	require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{"vip":{"fallback":["default", "enterprise"],"pricing_mode":"origin"}}`))

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	common.SetContextKey(ctx, constant.ContextKeyFallbackGroup, "stale")
	common.SetContextKey(ctx, constant.ContextKeyFallbackSourceGroup, "stale")

	ch, selected, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:        ctx,
		TokenGroup: "vip",
		ModelName:  "gpt-4",
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.Equal(t, "enterprise", selected)
	require.Equal(t, "enterprise", common.GetContextKeyString(ctx, constant.ContextKeyFallbackGroup))
	require.Equal(t, "vip", common.GetContextKeyString(ctx, constant.ContextKeyFallbackSourceGroup))
}

func TestCacheGetRandomSatisfiedChannel_RetryStillUsesFallbackWhenPrimaryHasNoAbilities(t *testing.T) {
	setupFallbackTestChannels(t,
		newChannelForSelection(8251, "enterprise", "gpt-4"),
	)
	require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{"vip":{"fallback":["default", "enterprise"],"pricing_mode":"target"}}`))

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	retry := 1

	ch, selected, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:        ctx,
		TokenGroup: "vip",
		ModelName:  "gpt-4",
		Retry:      &retry,
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.Equal(t, "enterprise", selected)
	require.Equal(t, "enterprise", common.GetContextKeyString(ctx, constant.ContextKeyFallbackGroup))
	require.Equal(t, "vip", common.GetContextKeyString(ctx, constant.ContextKeyFallbackSourceGroup))
}

func TestCacheGetRandomSatisfiedChannel_PrefersConfiguredGroupWhenAvailable(t *testing.T) {
	setupFallbackTestChannels(t,
		newChannelForSelection(8301, "vip", "gpt-4"),
		newChannelForSelection(8302, "default", "gpt-4"),
	)
	require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{"vip":{"fallback":["default"],"pricing_mode":"target"}}`))

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)

	ch, selected, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:        ctx,
		TokenGroup: "vip",
		ModelName:  "gpt-4",
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.Equal(t, "vip", selected)
	require.Equal(t, "", common.GetContextKeyString(ctx, constant.ContextKeyFallbackGroup))
	require.Equal(t, "", common.GetContextKeyString(ctx, constant.ContextKeyFallbackSourceGroup))
	require.Equal(t, 8301, ch.Id)
}

func TestCacheGetRandomSatisfiedChannel_WithoutFallbackMatch(t *testing.T) {
	setupFallbackTestChannelsWithNoChannels(t)
	require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{"vip":{"fallback":["default"],"pricing_mode":"target"}}`))

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)

	ch, selected, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:        ctx,
		TokenGroup: "vip",
		ModelName:  "gpt-4",
	})
	require.NoError(t, err)
	require.Nil(t, ch)
	require.Equal(t, "vip", selected)
	require.Equal(t, "", common.GetContextKeyString(ctx, constant.ContextKeyFallbackGroup))
}

func setupFallbackTestChannelsWithNoChannels(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM abilities")
		model.DB.Exec("DELETE FROM channels")
		require.NoError(t, setting.UpdateGroupFallbackByJsonString(`{}`))
	})
}
