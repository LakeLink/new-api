package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/active_request_setting"
	"github.com/gin-gonic/gin"
)

func TestActiveRequestTrackerRetainsCompletedRequests(t *testing.T) {
	setting := active_request_setting.GetActiveRequestSetting()
	originalRetention := setting.CompletedRetentionSeconds
	setting.CompletedRetentionSeconds = 10
	t.Cleanup(func() {
		setting.CompletedRetentionSeconds = originalRetention
	})

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.RemoteAddr = "127.0.0.1:12345"
	common.SetContextKey(c, constant.ContextKeyUserName, "alice")
	common.SetContextKey(c, constant.ContextKeyChannelName, "primary-openai")

	tracker := &ActiveRequestTracker{}
	tracker.Register(&relaycommon.RelayInfo{
		RequestId:       "req-1",
		UserId:          42,
		TokenId:         7,
		OriginModelName: "gpt-test",
	}, c)
	tracker.RecordOutput("req-1")
	tracker.Deregister("req-1")

	if got := tracker.Count(); got != 0 {
		t.Fatalf("Count() = %d, want only active requests counted", got)
	}

	snapshots := tracker.List()
	if len(snapshots) != 1 {
		t.Fatalf("List() returned %d snapshots, want 1", len(snapshots))
	}

	snapshot := snapshots[0]
	if snapshot.Status != ActiveRequestStatusCompleted {
		t.Fatalf("Status = %q, want %q", snapshot.Status, ActiveRequestStatusCompleted)
	}
	if snapshot.Username != "alice" {
		t.Fatalf("Username = %q, want alice", snapshot.Username)
	}
	if snapshot.ChannelName != "primary-openai" {
		t.Fatalf("ChannelName = %q, want primary-openai", snapshot.ChannelName)
	}
	if snapshot.CanTerminate {
		t.Fatal("completed request should not be terminable")
	}
	if snapshot.EndTime == 0 {
		t.Fatal("completed request should include end_time")
	}
}

func TestActiveRequestTrackerPrunesCompletedRequests(t *testing.T) {
	tracker := &ActiveRequestTracker{
		completed: map[string]*ActiveRequest{
			"old": {
				RequestId: "old",
				StartTime: time.Now().Add(-3 * time.Second),
				EndTime:   time.Now().Add(-2 * time.Second),
				Completed: true,
			},
		},
	}

	tracker.mu.Lock()
	tracker.pruneCompletedLocked(time.Now(), 1)
	tracker.mu.Unlock()

	if snapshots := tracker.List(); len(snapshots) != 0 {
		t.Fatalf("List() returned %d snapshots after prune, want 0", len(snapshots))
	}
}
