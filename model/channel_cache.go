package model

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*dto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex

func InitChannelCache() error {
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		return nil
	}
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*dto.AdvancedCustomConfig)
	var channels []*Channel
	if err := DB.Find(&channels).Error; err != nil {
		err = fmt.Errorf("load channels for runtime cache: %w", err)
		common.SysError(err.Error())
		return err
	}
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	var abilities []*Ability
	if err := DB.Find(&abilities).Error; err != nil {
		err = fmt.Errorf("load abilities for runtime cache: %w", err)
		common.SysError(err.Error())
		return err
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for _, ability := range abilities {
		if !ability.Enabled {
			continue
		}
		channel, ok := newChannelId2channel[ability.ChannelId]
		if !ok || channel.Status != common.ChannelStatusEnabled ||
			constant.IsRetiredChannelType(channel.Type) {
			continue
		}
		if newGroup2model2channels[ability.Group] == nil {
			newGroup2model2channels[ability.Group] = make(map[string][]int)
		}
		newGroup2model2channels[ability.Group][ability.Model] = append(
			newGroup2model2channels[ability.Group][ability.Model],
			channel.Id,
		)
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channelSyncLock.Unlock()
	// Lock ordering: InvalidatePricingCache acquires updatePricingLock, and
	// GetPricing (holding updatePricingLock) nests channelSyncLock.RLock via
	// loadPricingAdvancedCustomConfigs. channelSyncLock MUST be released before
	// invalidating the pricing cache, otherwise the reversed order deadlocks.
	InvalidatePricingCache()
	common.SysLog("channels synced from database")
	return nil
}

func SyncChannelCache(frequency int) {
	interval := common.SafeIntervalDuration(frequency, time.Second, 60*time.Second, "channel cache sync")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		common.SysLog("syncing channels from database")
		_ = InitChannelCache()
	}
}

func GetRandomSatisfiedChannel(group string, model string, retry int, requestPath string) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, requestPath)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	channels := filterChannelsByRequestPathAndModel(group2model2channels[group][model], requestPath, model)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels = filterChannelsByRequestPathAndModel(group2model2channels[group][normalizedModel], requestPath, model)
	}

	if len(channels) == 0 {
		return nil, nil
	}

	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			return cloneChannel(channel), nil
		}
		return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channels[0])
	}

	uniquePriorities := make(map[int]bool)
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			uniquePriorities[int(channel.GetPriority())] = true
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}
	var sortedUniquePriorities []int
	for priority := range uniquePriorities {
		sortedUniquePriorities = append(sortedUniquePriorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedUniquePriorities)))

	if retry >= len(uniquePriorities) {
		retry = len(uniquePriorities) - 1
	}
	targetPriority := int64(sortedUniquePriorities[retry])

	// get the priority for the given retry number
	var sumWeight int64
	var targetChannels []*Channel
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			if channel.GetPriority() == targetPriority {
				sumWeight += int64(channel.GetWeight())
				targetChannels = append(targetChannels, channel)
			}
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}

	if len(targetChannels) == 0 {
		return nil, errors.New(fmt.Sprintf("no channel found, group: %s, model: %s, priority: %d", group, model, targetPriority))
	}

	// smoothing factor and adjustment
	smoothingFactor := int64(1)
	smoothingAdjustment := int64(0)

	if sumWeight == 0 {
		// when all channels have weight 0, set sumWeight to the number of channels and set smoothing adjustment to 100
		// each channel's effective weight = 100
		sumWeight = int64(len(targetChannels)) * 100
		smoothingAdjustment = 100
	} else if sumWeight/int64(len(targetChannels)) < 10 {
		// when the average weight is less than 10, set smoothing factor to 100
		smoothingFactor = 100
	}

	// Calculate the total weight of all channels up to endIdx
	totalWeight := sumWeight * smoothingFactor

	// Generate a random value in the range [0, totalWeight)
	randomWeight := rand.Int63n(totalWeight)

	// Find a channel based on its weight
	for _, channel := range targetChannels {
		randomWeight -= int64(channel.GetWeight())*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return cloneChannel(channel), nil
		}
	}
	// return null if no channel is not found
	return nil, errors.New("channel not found")
}

// filterChannelsByRequestPathAndModel restricts candidates by request path and
// model. Only Advanced Custom (type 58) channels are path-checked: they are kept
// only when one of their configured routes matches requestPath and model. All
// other channel types always pass. When requestPath is empty, filtering is skipped.
// Caller must hold channelSyncLock (read lock). The cached slice is never mutated.
func filterChannelsByRequestPathAndModel(channels []int, requestPath string, model string) []int {
	if requestPath == "" || len(channels) == 0 {
		return channels
	}
	filtered := make([]int, 0, len(channels))
	for _, channelId := range channels {
		channel, ok := channelsIDM[channelId]
		if !ok {
			// keep it so the downstream consistency error is raised as before
			filtered = append(filtered, channelId)
			continue
		}
		if channel.Type != constant.ChannelTypeAdvancedCustom {
			filtered = append(filtered, channelId)
			continue
		}
		if config := channel2advancedCustomConfig[channelId]; config != nil && config.SupportsPathForModel(requestPath, model) {
			filtered = append(filtered, channelId)
		}
	}
	return filtered
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return cloneChannel(c), nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	snapshot := cloneChannel(c)
	return &snapshot.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	cacheUpdateChannelStatusLocked(id, status)
}

// cacheUpdateChannelStatusLocked updates routing membership together with the
// cached channel status. Caller must hold channelSyncLock for writing.
func cacheUpdateChannelStatusLocked(id int, status int) {
	channel, ok := channelsIDM[id]
	if !ok {
		return
	}
	channel.Status = status
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, channels := range model2channels {
				for i, channelId := range channels {
					if channelId == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(channels[:i], channels[i+1:]...)
						break
					}
				}
			}
		}
		return
	}
	if constant.IsRetiredChannelType(channel.Type) {
		return
	}
	if group2model2channels == nil {
		group2model2channels = make(map[string]map[string][]int)
	}
	for _, group := range channel.GetGroups() {
		if group2model2channels[group] == nil {
			group2model2channels[group] = make(map[string][]int)
		}
		for _, model := range channel.GetModels() {
			channels := group2model2channels[group][model]
			alreadyPresent := false
			for _, channelID := range channels {
				if channelID == id {
					alreadyPresent = true
					break
				}
			}
			if !alreadyPresent {
				channels = append(channels, id)
			}
			sort.SliceStable(channels, func(i, j int) bool {
				return channelsIDM[channels[i]].GetPriority() > channelsIDM[channels[j]].GetPriority()
			})
			group2model2channels[group][model] = channels
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	if channel == nil {
		channelSyncLock.Unlock()
		return
	}

	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	channel = cloneChannel(channel)
	if oldChannel, ok := channelsIDM[channel.Id]; ok {
		logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldChannel.ChannelInfo.MultiKeyPollingIndex)
	}
	channelsIDM[channel.Id] = channel
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*dto.AdvancedCustomConfig)
	}
	delete(channel2advancedCustomConfig, channel.Id)
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		}
	}
	logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
	// Lock ordering: do NOT hold channelSyncLock while calling
	// InvalidatePricingCache. GetPricing acquires updatePricingLock first and then
	// channelSyncLock.RLock (via loadPricingAdvancedCustomConfigs); acquiring
	// updatePricingLock while holding channelSyncLock would be an AB-BA deadlock.
	channelSyncLock.Unlock()
	InvalidatePricingCache()
}

// cloneChannel isolates mutable request snapshots from the shared channel
// cache. In particular ChannelInfo contains maps and polling state that are
// updated by health checks while relay requests concurrently read the channel.
func cloneChannel(channel *Channel) *Channel {
	if channel == nil {
		return nil
	}
	snapshot := *channel
	if channel.OpenAIOrganization != nil {
		value := *channel.OpenAIOrganization
		snapshot.OpenAIOrganization = &value
	}
	if channel.TestModel != nil {
		value := *channel.TestModel
		snapshot.TestModel = &value
	}
	if channel.Weight != nil {
		value := *channel.Weight
		snapshot.Weight = &value
	}
	if channel.BaseURL != nil {
		value := *channel.BaseURL
		snapshot.BaseURL = &value
	}
	if channel.ModelMapping != nil {
		value := *channel.ModelMapping
		snapshot.ModelMapping = &value
	}
	if channel.StatusCodeMapping != nil {
		value := *channel.StatusCodeMapping
		snapshot.StatusCodeMapping = &value
	}
	if channel.Priority != nil {
		value := *channel.Priority
		snapshot.Priority = &value
	}
	if channel.AutoBan != nil {
		value := *channel.AutoBan
		snapshot.AutoBan = &value
	}
	if channel.Tag != nil {
		value := *channel.Tag
		snapshot.Tag = &value
	}
	if channel.Setting != nil {
		value := *channel.Setting
		snapshot.Setting = &value
	}
	if channel.ParamOverride != nil {
		value := *channel.ParamOverride
		snapshot.ParamOverride = &value
	}
	if channel.HeaderOverride != nil {
		value := *channel.HeaderOverride
		snapshot.HeaderOverride = &value
	}
	if channel.Remark != nil {
		value := *channel.Remark
		snapshot.Remark = &value
	}
	snapshot.Keys = append([]string(nil), channel.Keys...)
	if channel.ChannelInfo.MultiKeyStatusList != nil {
		snapshot.ChannelInfo.MultiKeyStatusList = make(map[int]int, len(channel.ChannelInfo.MultiKeyStatusList))
		for key, value := range channel.ChannelInfo.MultiKeyStatusList {
			snapshot.ChannelInfo.MultiKeyStatusList[key] = value
		}
	}
	if channel.ChannelInfo.MultiKeyDisabledReason != nil {
		snapshot.ChannelInfo.MultiKeyDisabledReason = make(map[int]string, len(channel.ChannelInfo.MultiKeyDisabledReason))
		for key, value := range channel.ChannelInfo.MultiKeyDisabledReason {
			snapshot.ChannelInfo.MultiKeyDisabledReason[key] = value
		}
	}
	if channel.ChannelInfo.MultiKeyDisabledTime != nil {
		snapshot.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64, len(channel.ChannelInfo.MultiKeyDisabledTime))
		for key, value := range channel.ChannelInfo.MultiKeyDisabledTime {
			snapshot.ChannelInfo.MultiKeyDisabledTime[key] = value
		}
	}
	return &snapshot
}
