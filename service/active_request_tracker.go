package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/active_request_setting"
	"github.com/gin-gonic/gin"
)

const (
	ActiveRequestStatusActive    = "active"
	ActiveRequestStatusCompleted = "completed"
)

// ActiveRequest represents an in-flight relay request tracked in memory.
type ActiveRequest struct {
	RequestId      string             `json:"request_id"`
	UserId         int                `json:"user_id"`
	Username       string             `json:"username"`
	TokenId        int                `json:"token_id"`
	TokenName      string             `json:"token_name"`
	Model          string             `json:"model"`
	ChannelName    string             `json:"channel_name"`
	ChannelId      int                `json:"channel_id"`
	ChannelType    int                `json:"channel_type"`
	StartTime      time.Time          `json:"start_time"`
	EndTime        time.Time          `json:"end_time"`
	IsStream       bool               `json:"is_stream"`
	ClientIP       string             `json:"client_ip"`
	InputTokens    int                `json:"input_tokens"`
	OutputChunks   int32              `json:"output_chunks"`
	Completed      bool               `json:"completed"`
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
	Username        string  `json:"username"`
	TokenId         int     `json:"token_id"`
	TokenName       string  `json:"token_name"`
	Model           string  `json:"model"`
	ChannelName     string  `json:"channel_name"`
	ChannelId       int     `json:"channel_id"`
	ChannelType     int     `json:"channel_type"`
	StartTime       int64   `json:"start_time"`
	EndTime         int64   `json:"end_time,omitempty"`
	Status          string  `json:"status"`
	IsStream        bool    `json:"is_stream"`
	ClientIP        string  `json:"client_ip"`
	InputTokens     int     `json:"input_tokens"`
	OutputChunks    int32   `json:"output_chunks"`
	ElapsedSeconds  float64 `json:"elapsed_seconds"`
	StaleForSeconds float64 `json:"stale_for_seconds"`
	EndedSecondsAgo float64 `json:"ended_seconds_ago,omitempty"`
	CanTerminate    bool    `json:"can_terminate"`
}

// ActiveRequestTracker is the global in-memory tracker for active relay requests.
type ActiveRequestTracker struct {
	mu        sync.RWMutex
	entries   map[string]*ActiveRequest
	completed map[string]*ActiveRequest
}

// GlobalActiveRequestTracker is the singleton instance.
var GlobalActiveRequestTracker = &ActiveRequestTracker{
	entries:   make(map[string]*ActiveRequest),
	completed: make(map[string]*ActiveRequest),
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
	username := common.GetContextKeyString(c, constant.ContextKeyUserName)
	channelName := common.GetContextKeyString(c, constant.ContextKeyChannelName)
	req := &ActiveRequest{
		RequestId:      info.RequestId,
		UserId:         info.UserId,
		Username:       username,
		TokenId:        info.TokenId,
		TokenName:      c.GetString("token_name"),
		Model:          info.OriginModelName,
		ChannelName:    channelName,
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
	t.ensureMapsLocked()
	t.entries[info.RequestId] = req
	delete(t.completed, info.RequestId)
	t.pruneCompletedLocked(now, active_request_setting.GetActiveRequestSetting().CompletedRetentionSeconds)
	t.mu.Unlock()

	logger.LogDebug(c, fmt.Sprintf("[ActiveRequestTracker] registered request %s (model=%s, stream=%v)", info.RequestId, info.OriginModelName, info.IsStream))
}

// Deregister removes a request from the active tracker and keeps a short-lived completed snapshot.
func (t *ActiveRequestTracker) Deregister(requestId string) {
	now := time.Now()
	retentionSeconds := active_request_setting.GetActiveRequestSetting().CompletedRetentionSeconds

	t.mu.Lock()
	t.ensureMapsLocked()
	if req, ok := t.entries[requestId]; ok {
		delete(t.entries, requestId)
		if retentionSeconds > 0 {
			req.Completed = true
			req.EndTime = now
			req.CancelFunc = nil
			t.completed[requestId] = req
		}
	}
	t.pruneCompletedLocked(now, retentionSeconds)
	t.mu.Unlock()
}

// Terminate cancels a specific active request. Returns true if the request was found.
func (t *ActiveRequestTracker) Terminate(requestId string) bool {
	t.mu.RLock()
	req, ok := t.entries[requestId]
	if !ok || req.CancelFunc == nil {
		t.mu.RUnlock()
		return false
	}
	cancelFunc := req.CancelFunc
	t.mu.RUnlock()
	cancelFunc()
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

// List returns a snapshot of all active and recently completed requests.
func (t *ActiveRequestTracker) List() []ActiveRequestSnapshot {
	now := time.Now()
	retentionSeconds := active_request_setting.GetActiveRequestSetting().CompletedRetentionSeconds

	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureMapsLocked()
	t.pruneCompletedLocked(now, retentionSeconds)

	snapshots := make([]ActiveRequestSnapshot, 0, len(t.entries)+len(t.completed))
	for _, req := range t.entries {
		snapshots = append(snapshots, req.snapshot(now))
	}
	for _, req := range t.completed {
		snapshots = append(snapshots, req.snapshot(now))
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Status != snapshots[j].Status {
			return snapshots[i].Status == ActiveRequestStatusActive
		}
		if snapshots[i].Status == ActiveRequestStatusCompleted {
			return snapshots[i].EndTime > snapshots[j].EndTime
		}
		return snapshots[i].StartTime > snapshots[j].StartTime
	})
	return snapshots
}

// Count returns the number of active requests.
func (t *ActiveRequestTracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.entries)
}

func (t *ActiveRequestTracker) ensureMapsLocked() {
	if t.entries == nil {
		t.entries = make(map[string]*ActiveRequest)
	}
	if t.completed == nil {
		t.completed = make(map[string]*ActiveRequest)
	}
}

func (t *ActiveRequestTracker) pruneCompletedLocked(now time.Time, retentionSeconds int) {
	if retentionSeconds <= 0 {
		clear(t.completed)
		return
	}
	retention := time.Duration(retentionSeconds) * time.Second
	for requestId, req := range t.completed {
		if req.EndTime.IsZero() || now.Sub(req.EndTime) > retention {
			delete(t.completed, requestId)
		}
	}
}

func (r *ActiveRequest) snapshot(now time.Time) ActiveRequestSnapshot {
	status := ActiveRequestStatusActive
	canTerminate := r.CancelFunc != nil
	elapsedUntil := now
	endTime := int64(0)
	endedSecondsAgo := float64(0)

	if r.Completed {
		status = ActiveRequestStatusCompleted
		canTerminate = false
		if !r.EndTime.IsZero() {
			elapsedUntil = r.EndTime
			endTime = r.EndTime.Unix()
			endedSecondsAgo = now.Sub(r.EndTime).Seconds()
		}
	}

	elapsed := elapsedUntil.Sub(r.StartTime).Seconds()
	var staleFor float64
	lastNano := atomic.LoadInt64(&r.LastOutputNano)
	if lastNano > 0 {
		lastOutput := time.Unix(0, lastNano)
		if lastOutput.After(elapsedUntil) {
			staleFor = 0
		} else {
			staleFor = elapsedUntil.Sub(lastOutput).Seconds()
		}
	} else {
		staleFor = elapsed
	}

	return ActiveRequestSnapshot{
		RequestId:       r.RequestId,
		UserId:          r.UserId,
		Username:        r.Username,
		TokenId:         r.TokenId,
		TokenName:       r.TokenName,
		Model:           r.Model,
		ChannelName:     r.ChannelName,
		ChannelId:       r.ChannelId,
		ChannelType:     r.ChannelType,
		StartTime:       r.StartTime.Unix(),
		EndTime:         endTime,
		Status:          status,
		IsStream:        r.IsStream,
		ClientIP:        r.ClientIP,
		InputTokens:     r.InputTokens,
		OutputChunks:    atomic.LoadInt32(&r.OutputChunks),
		ElapsedSeconds:  elapsed,
		StaleForSeconds: staleFor,
		EndedSecondsAgo: endedSecondsAgo,
		CanTerminate:    canTerminate,
	}
}
