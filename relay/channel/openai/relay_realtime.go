package openai

import (
	"context"
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func OpenaiRealtimeHandler(c *gin.Context, info *relaycommon.RelayInfo) (*types.NewAPIError, *dto.RealtimeUsage) {
	if info == nil || info.ClientWs == nil || info.TargetWs == nil {
		return types.NewError(fmt.Errorf("invalid websocket connection"), types.ErrorCodeBadResponse), nil
	}

	info.IsStream = true
	clientConn := info.ClientWs
	targetConn := info.TargetWs
	helper.LimitClientWebsocketMessages(clientConn)
	helper.LimitUpstreamWebsocketMessages(targetConn)
	relayCtx := c.Request.Context()
	if info.RelayCancelCtx != nil {
		relayCtx = info.RelayCancelCtx
	}
	handlerCtx, stopHandler := context.WithCancel(relayCtx)
	defer stopHandler()

	clientClosed := make(chan struct{})
	targetClosed := make(chan struct{})
	errChan := make(chan error, 2)

	usage := &dto.RealtimeUsage{}
	localUsage := &dto.RealtimeUsage{}
	sumUsage := &dto.RealtimeUsage{}
	var stateMu sync.Mutex
	var readers sync.WaitGroup
	var closeConnectionsOnce sync.Once
	closeConnections := func() {
		closeConnectionsOnce.Do(func() {
			_ = clientConn.Close()
			_ = targetConn.Close()
		})
	}
	defer closeConnections()

	gopool.Go(func() {
		select {
		case <-handlerCtx.Done():
			closeConnections()
		case <-c.Request.Context().Done():
			closeConnections()
		}
	})

	reportError := func(err error) {
		select {
		case errChan <- err:
		case <-handlerCtx.Done():
		}
	}

	readers.Add(2)
	gopool.Go(func() {
		stateLocked := false
		defer readers.Done()
		defer close(clientClosed)
		defer func() {
			if r := recover(); r != nil {
				if stateLocked {
					stateMu.Unlock()
				}
				reportError(fmt.Errorf("panic in client reader: %v", r))
			}
		}()
		for {
			select {
			case <-handlerCtx.Done():
				return
			default:
				_, message, err := clientConn.ReadMessage()
				if err != nil {
					if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
						reportError(fmt.Errorf("error reading from client: %v", err))
					}
					return
				}

				realtimeEvent := &dto.RealtimeEvent{}
				err = common.Unmarshal(message, realtimeEvent)
				if err != nil {
					reportError(fmt.Errorf("error unmarshalling message: %v", err))
					return
				}

				stateMu.Lock()
				stateLocked = true
				if realtimeEvent.Type == dto.RealtimeEventTypeSessionUpdate {
					if realtimeEvent.Session != nil {
						if realtimeEvent.Session.Tools != nil {
							info.RealtimeTools = realtimeEvent.Session.Tools
						}
					}
				}

				textToken, audioToken, err := service.CountTokenRealtime(info, *realtimeEvent, info.UpstreamModelName)
				if err != nil {
					stateLocked = false
					stateMu.Unlock()
					reportError(fmt.Errorf("error counting text token: %v", err))
					return
				}
				logger.LogInfo(c, fmt.Sprintf("type: %s, textToken: %d, audioToken: %d", realtimeEvent.Type, textToken, audioToken))
				localUsage.TotalTokens += textToken + audioToken
				localUsage.InputTokens += textToken + audioToken
				localUsage.InputTokenDetails.TextTokens += textToken
				localUsage.InputTokenDetails.AudioTokens += audioToken
				stateLocked = false
				stateMu.Unlock()

				err = helper.WssString(c, targetConn, string(message))
				if err != nil {
					reportError(fmt.Errorf("error writing to target: %v", err))
					return
				}
			}
		}
	})

	gopool.Go(func() {
		stateLocked := false
		defer readers.Done()
		defer close(targetClosed)
		defer func() {
			if r := recover(); r != nil {
				if stateLocked {
					stateMu.Unlock()
				}
				reportError(fmt.Errorf("panic in target reader: %v", r))
			}
		}()
		for {
			select {
			case <-handlerCtx.Done():
				return
			default:
				_, message, err := targetConn.ReadMessage()
				if err != nil {
					if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
						reportError(fmt.Errorf("error reading from target: %v", err))
					}
					return
				}
				realtimeEvent := &dto.RealtimeEvent{}
				err = common.Unmarshal(message, realtimeEvent)
				if err != nil {
					reportError(fmt.Errorf("error unmarshalling message: %v", err))
					return
				}

				stateMu.Lock()
				stateLocked = true
				info.SetFirstResponseTime()
				if realtimeEvent.Type == dto.RealtimeEventTypeResponseDone {
					var realtimeUsage *dto.RealtimeUsage
					if realtimeEvent.Response != nil {
						realtimeUsage = realtimeEvent.Response.Usage
					}
					if realtimeUsage != nil {
						usage.TotalTokens += realtimeUsage.TotalTokens
						usage.InputTokens += realtimeUsage.InputTokens
						usage.OutputTokens += realtimeUsage.OutputTokens
						usage.InputTokenDetails.AudioTokens += realtimeUsage.InputTokenDetails.AudioTokens
						usage.InputTokenDetails.CachedTokens += realtimeUsage.InputTokenDetails.CachedTokens
						usage.InputTokenDetails.TextTokens += realtimeUsage.InputTokenDetails.TextTokens
						usage.OutputTokenDetails.AudioTokens += realtimeUsage.OutputTokenDetails.AudioTokens
						usage.OutputTokenDetails.TextTokens += realtimeUsage.OutputTokenDetails.TextTokens
						err := preConsumeUsage(c, info, usage, sumUsage)
						if err != nil {
							stateLocked = false
							stateMu.Unlock()
							reportError(fmt.Errorf("error consume usage: %v", err))
							return
						}
						// 本次计费完成，清除
						usage = &dto.RealtimeUsage{}

						localUsage = &dto.RealtimeUsage{}
					} else {
						textToken, audioToken, err := service.CountTokenRealtime(info, *realtimeEvent, info.UpstreamModelName)
						if err != nil {
							stateLocked = false
							stateMu.Unlock()
							reportError(fmt.Errorf("error counting text token: %v", err))
							return
						}
						logger.LogInfo(c, fmt.Sprintf("type: %s, textToken: %d, audioToken: %d", realtimeEvent.Type, textToken, audioToken))
						localUsage.TotalTokens += textToken + audioToken
						info.IsFirstRequest = false
						localUsage.InputTokens += textToken + audioToken
						localUsage.InputTokenDetails.TextTokens += textToken
						localUsage.InputTokenDetails.AudioTokens += audioToken
						err = preConsumeUsage(c, info, localUsage, sumUsage)
						if err != nil {
							stateLocked = false
							stateMu.Unlock()
							reportError(fmt.Errorf("error consume usage: %v", err))
							return
						}
						// 本次计费完成，清除
						localUsage = &dto.RealtimeUsage{}
						// print now usage
					}
					logger.LogDebug(c, "realtime streaming usage updated: total=%v, local=%v", sumUsage, localUsage)

				} else if realtimeEvent.Type == dto.RealtimeEventTypeSessionUpdated || realtimeEvent.Type == dto.RealtimeEventTypeSessionCreated {
					realtimeSession := realtimeEvent.Session
					if realtimeSession != nil {
						// update audio format
						info.InputAudioFormat = common.GetStringIfEmpty(realtimeSession.InputAudioFormat, info.InputAudioFormat)
						info.OutputAudioFormat = common.GetStringIfEmpty(realtimeSession.OutputAudioFormat, info.OutputAudioFormat)
					}
				} else {
					textToken, audioToken, err := service.CountTokenRealtime(info, *realtimeEvent, info.UpstreamModelName)
					if err != nil {
						stateLocked = false
						stateMu.Unlock()
						reportError(fmt.Errorf("error counting text token: %v", err))
						return
					}
					logger.LogInfo(c, fmt.Sprintf("type: %s, textToken: %d, audioToken: %d", realtimeEvent.Type, textToken, audioToken))
					localUsage.TotalTokens += textToken + audioToken
					localUsage.OutputTokens += textToken + audioToken
					localUsage.OutputTokenDetails.TextTokens += textToken
					localUsage.OutputTokenDetails.AudioTokens += audioToken
				}
				stateLocked = false
				stateMu.Unlock()

				err = helper.WssString(c, clientConn, string(message))
				if err != nil {
					reportError(fmt.Errorf("error writing to client: %v", err))
					return
				}
			}
		}
	})

	select {
	case <-clientClosed:
	case <-targetClosed:
	case err := <-errChan:
		//return service.OpenAIErrorWrapper(err, "realtime_error", http.StatusInternalServerError), nil
		logger.LogError(c, "realtime error: "+err.Error())
	case <-c.Done():
	case <-relayCtx.Done():
	}

	stopHandler()
	closeConnections()
	readers.Wait()

	stateMu.Lock()
	defer stateMu.Unlock()
	if usage.TotalTokens != 0 {
		_ = preConsumeUsage(c, info, usage, sumUsage)
	}

	if localUsage.TotalTokens != 0 {
		_ = preConsumeUsage(c, info, localUsage, sumUsage)
	}

	// check usage total tokens, if 0, use local usage

	return nil, sumUsage
}

func preConsumeUsage(ctx *gin.Context, info *relaycommon.RelayInfo, usage *dto.RealtimeUsage, totalUsage *dto.RealtimeUsage) error {
	if usage == nil || totalUsage == nil || usage == totalUsage {
		return fmt.Errorf("invalid usage pointer")
	}
	if err := service.ValidateRealtimeBillingUsage(usage); err != nil {
		return fmt.Errorf("invalid realtime usage delta: %w", err)
	}
	if err := service.ValidateRealtimeBillingUsage(totalUsage); err != nil {
		return fmt.Errorf("invalid cumulative realtime usage: %w", err)
	}

	next := *totalUsage
	next.TotalTokens += usage.TotalTokens
	next.InputTokens += usage.InputTokens
	next.OutputTokens += usage.OutputTokens
	next.InputTokenDetails.CachedTokens += usage.InputTokenDetails.CachedTokens
	next.InputTokenDetails.TextTokens += usage.InputTokenDetails.TextTokens
	next.InputTokenDetails.AudioTokens += usage.InputTokenDetails.AudioTokens
	next.OutputTokenDetails.TextTokens += usage.OutputTokenDetails.TextTokens
	next.OutputTokenDetails.AudioTokens += usage.OutputTokenDetails.AudioTokens
	if err := service.ValidateRealtimeBillingUsage(&next); err != nil {
		return fmt.Errorf("invalid cumulative realtime usage: %w", err)
	}

	// The response represented by usage was already delivered. Commit it to the
	// final settlement exactly once even when extending the reservation fails;
	// leaving the delta live would make the connection-shutdown flush add it a
	// second time.
	*totalUsage = next
	*usage = dto.RealtimeUsage{}
	return service.PreWssConsumeQuota(ctx, info, totalUsage)
}
