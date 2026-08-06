package service

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	codexUsageLimitCheckInterval   = time.Hour
	codexUsageLimitCheckBatchSize  = 200
	codexUsageLimitCheckTimeout    = 15 * time.Second
	codexUsageLimitAutoPauseReason = "Codex weekly usage remaining is below the configured auto-pause threshold"
)

var (
	codexUsageLimitCheckOnce    sync.Once
	codexUsageLimitCheckRunning atomic.Bool
	codexUsageFetcher           = FetchCodexWhamUsage
)

type codexUsageLimitWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int64    `json:"limit_window_seconds"`
}

type codexUsageLimitRateLimit struct {
	PrimaryWindow   *codexUsageLimitWindow `json:"primary_window"`
	SecondaryWindow *codexUsageLimitWindow `json:"secondary_window"`
}

type codexUsageLimitPayload struct {
	RateLimit codexUsageLimitRateLimit `json:"rate_limit"`
}

func StartCodexUsageLimitCheckTask() {
	codexUsageLimitCheckOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("codex weekly usage limit check started: tick=%s", codexUsageLimitCheckInterval))
			ticker := time.NewTicker(codexUsageLimitCheckInterval)
			defer ticker.Stop()

			runCodexUsageLimitCheckOnce()
			for range ticker.C {
				runCodexUsageLimitCheckOnce()
			}
		})
	})
}

func runCodexUsageLimitCheckOnce() {
	if !codexUsageLimitCheckRunning.CompareAndSwap(false, true) {
		return
	}
	defer codexUsageLimitCheckRunning.Store(false)

	ctx := context.Background()
	lastID := 0
	var scanned, paused, resumed int

	for {
		var channels []*model.Channel
		query := model.DB.
			Select("id", "type", "key", "status", "name", "base_url", "setting", "other_info", "channel_info").
			Where("type = ? AND (status = ? OR status = ?)",
				constant.ChannelTypeCodex,
				common.ChannelStatusEnabled,
				common.ChannelStatusAutoDisabled,
			).
			Order("id asc").
			Limit(codexUsageLimitCheckBatchSize)
		if lastID > 0 {
			query = query.Where("id > ?", lastID)
		}
		if err := query.Find(&channels).Error; err != nil {
			logger.LogError(ctx, fmt.Sprintf("codex weekly usage limit check: query channels failed: %v", err))
			return
		}
		if len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id

		for _, channel := range channels {
			if channel == nil || channel.ChannelInfo.IsMultiKey {
				continue
			}
			setting := channel.GetSetting()
			if !setting.CodexAutoPauseWeeklyLimitEnabled {
				continue
			}
			scanned++
			oauthKey, err := parseCodexOAuthKey(strings.TrimSpace(channel.Key))
			if err != nil || strings.TrimSpace(oauthKey.AccessToken) == "" || strings.TrimSpace(oauthKey.AccountID) == "" {
				logger.LogWarn(ctx, fmt.Sprintf("codex weekly usage limit check: channel_id=%d has invalid credentials", channel.Id))
				continue
			}
			client, err := NewProxyHttpClient(setting.Proxy)
			if err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("codex weekly usage limit check: channel_id=%d create proxy client failed: %v", channel.Id, err))
				continue
			}
			requestCtx, cancel := context.WithTimeout(ctx, codexUsageLimitCheckTimeout)
			statusCode, body, err := codexUsageFetcher(requestCtx, client, channel.GetBaseURL(), oauthKey.AccessToken, oauthKey.AccountID)
			cancel()
			if err != nil || statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
				if err != nil {
					logger.LogWarn(ctx, fmt.Sprintf("codex weekly usage limit check: channel_id=%d fetch usage failed: %v", channel.Id, err))
				} else {
					logger.LogWarn(ctx, fmt.Sprintf("codex weekly usage limit check: channel_id=%d usage request returned status=%d", channel.Id, statusCode))
				}
				continue
			}

			remaining, ok := codexWeeklyUsageRemainingPercent(body)
			if !ok {
				logger.LogWarn(ctx, fmt.Sprintf("codex weekly usage limit check: channel_id=%d response has no weekly usage window", channel.Id))
				continue
			}
			if remaining < setting.CodexAutoPauseWeeklyLimitThreshold {
				if channel.Status == common.ChannelStatusEnabled && model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, codexUsageLimitAutoPauseReason) {
					paused++
					logger.LogInfo(ctx, fmt.Sprintf("codex weekly usage limit check: auto-paused channel_id=%d remaining=%.2f threshold=%.2f", channel.Id, remaining, setting.CodexAutoPauseWeeklyLimitThreshold))
				}
				continue
			}
			if channel.Status == common.ChannelStatusAutoDisabled && channel.GetOtherInfo()["status_reason"] == codexUsageLimitAutoPauseReason && model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusEnabled, "") {
				resumed++
				logger.LogInfo(ctx, fmt.Sprintf("codex weekly usage limit check: re-enabled channel_id=%d remaining=%.2f threshold=%.2f", channel.Id, remaining, setting.CodexAutoPauseWeeklyLimitThreshold))
			}
		}
	}

	if common.DebugEnabled {
		logger.LogDebug(ctx, "codex weekly usage limit check: scanned=%d paused=%d resumed=%d", scanned, paused, resumed)
	}
}

func codexWeeklyUsageRemainingPercent(body []byte) (float64, bool) {
	var payload codexUsageLimitPayload
	if err := common.Unmarshal(body, &payload); err != nil {
		return 0, false
	}
	for _, window := range []*codexUsageLimitWindow{
		payload.RateLimit.PrimaryWindow,
		payload.RateLimit.SecondaryWindow,
	} {
		if window == nil || window.UsedPercent == nil || window.LimitWindowSeconds < int64((24*time.Hour).Seconds()) || math.IsNaN(*window.UsedPercent) || math.IsInf(*window.UsedPercent, 0) {
			continue
		}
		return max(0, min(100, 100-*window.UsedPercent)), true
	}
	return 0, false
}
