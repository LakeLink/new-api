package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// ActiveRequest represents an in-flight relay request tracked in memory.
type ActiveRequest struct {
	RequestId      string             `json:"request_id"`
	UserId         int                `json:"user_id"`
	TokenId        int                `json:"token_id"`
	TokenName      string             `json:"token_name"`
	Model          string             `json:"model"`
	ChannelId      int                `json:"channel_id"`
	ChannelType    int                `json:"channel_type"`
	StartTime      time.Time          `json:"start_time"`
	IsStream       bool               `json:"is_stream"`
	ClientIP       string             `json:"client_ip"`
	InputTokens    int                `json:"input_tokens"`
	OutputChunks   int32              `json:"output_chunks"`
	LastOutputNano int64              `json:"-"` // atomic unix nano
	CancelFunc     context.CancelFunc `json:"-"`
}

// RecordOutput atomically increments the output chunk counter and updates last output time.
func (r *ActiveRequest) RecordOutput() {
	atomic.AddInt32(&r.OutputChunks, 1)
	atomic.StoreInt64(&r.LastOutputNano, time.Now().UnixNano())
}

// ActiveRequestSnapshot is the JSON-serializable view of an ActiveRequest.
type ActiveRequestSnapshot struct {
	RequestId       string  `json:"request_id"`
	UserId          int     `json:"user_id"`
	TokenId         int     `json:"token_id"`
	TokenName       string  `json:"token_name"`
	Model           string  `json:"model"`
	ChannelId       int     `json:"channel_id"`
	ChannelType     int     `json:"channel_type"`
	StartTime       int64   `json:"start_time"`
	IsStream        bool    `json:"is_stream"`
	ClientIP        string  `json:"client_ip"`
	InputTokens     int     `json:"input_tokens"`
	OutputChunks    int32   `json:"output_chunks"`
	ElapsedSeconds  float64 `json:"elapsed_seconds"`
	StaleForSeconds float64 `json:"stale_for_seconds"`
}

// ActiveRequestTracker is the global in-memory tracker for active relay requests.
type ActiveRequestTracker struct {
	mu      sync.RWMutex
	entries map[string]*ActiveRequest
}

// GlobalActiveRequestTracker is the singleton instance.
var GlobalActiveRequestTracker = &ActiveRequestTracker{
	entries: make(map[string]*ActiveRequest),
}

// Register adds a new active request to the tracker.
func (t *ActiveRequestTracker) Register(info *relaycommon.RelayInfo, c *gin.Context) {
	now := time.Now()
	channelId := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	channelType := common.GetContextKeyInt(c, constant.ContextKeyChannelType)
	if info.ChannelMeta != nil {
		channelId = info.ChannelMeta.ChannelId
		channelType = info.ChannelMeta.ChannelType
	}
	req := &ActiveRequest{
		RequestId:      info.RequestId,
		UserId:         info.UserId,
		TokenId:        info.TokenId,
		TokenName:      c.GetString("token_name"),
		Model:          info.OriginModelName,
		ChannelId:      channelId,
		ChannelType:    channelType,
		StartTime:      now,
		IsStream:       info.IsStream,
		ClientIP:       c.ClientIP(),
		InputTokens:    info.GetEstimatePromptTokens(),
		OutputChunks:   0,
		LastOutputNano: 0,
		CancelFunc:     info.RelayCancelFunc,
	}

	t.mu.Lock()
	t.entries[info.RequestId] = req
	t.mu.Unlock()

	logger.LogDebug(c, fmt.Sprintf("[ActiveRequestTracker] registered request %s (model=%s, stream=%v)", info.RequestId, info.OriginModelName, info.IsStream))
}

// Deregister removes a request from the tracker.
func (t *ActiveRequestTracker) Deregister(requestId string) {
	t.mu.Lock()
	delete(t.entries, requestId)
	t.mu.Unlock()
}

// Terminate cancels a specific active request. Returns true if the request was found.
func (t *ActiveRequestTracker) Terminate(requestId string) bool {
	t.mu.RLock()
	req, ok := t.entries[requestId]
	t.mu.RUnlock()
	if !ok || req.CancelFunc == nil {
		return false
	}
	req.CancelFunc()
	return true
}

// RecordOutput increments the output chunk counter for a request.
func (t *ActiveRequestTracker) RecordOutput(requestId string) {
	t.mu.RLock()
	req, ok := t.entries[requestId]
	t.mu.RUnlock()
	if ok {
		req.RecordOutput()
	}
}

// UpdateInputTokens sets the estimated input token count for a request.
func (t *ActiveRequestTracker) UpdateInputTokens(requestId string, tokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	req, ok := t.entries[requestId]
	if ok {
		req.InputTokens = tokens
	}
}

// List returns a snapshot of all active requests.
func (t *ActiveRequestTracker) List() []ActiveRequestSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	now := time.Now()
	snapshots := make([]ActiveRequestSnapshot, 0, len(t.entries))
	for _, req := range t.entries {
		elapsed := now.Sub(req.StartTime).Seconds()
		var staleFor float64
		lastNano := atomic.LoadInt64(&req.LastOutputNano)
		if lastNano > 0 {
			staleFor = now.Sub(time.Unix(0, lastNano)).Seconds()
		} else {
			staleFor = elapsed
		}
		snapshots = append(snapshots, ActiveRequestSnapshot{
			RequestId:       req.RequestId,
			UserId:          req.UserId,
			TokenId:         req.TokenId,
			TokenName:       req.TokenName,
			Model:           req.Model,
			ChannelId:       req.ChannelId,
			ChannelType:     req.ChannelType,
			StartTime:       req.StartTime.Unix(),
			IsStream:        req.IsStream,
			ClientIP:        req.ClientIP,
			InputTokens:     req.InputTokens,
			OutputChunks:    atomic.LoadInt32(&req.OutputChunks),
			ElapsedSeconds:  elapsed,
			StaleForSeconds: staleFor,
		})
	}
	return snapshots
}

// Count returns the number of active requests.
func (t *ActiveRequestTracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.entries)
}
