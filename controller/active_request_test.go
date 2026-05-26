package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/active_request_setting"
	"github.com/gin-gonic/gin"
)

func withActiveRequestControllerTestState(t *testing.T) *service.ActiveRequestTracker {
	t.Helper()

	gin.SetMode(gin.TestMode)

	oldTracker := service.GlobalActiveRequestTracker
	tracker := &service.ActiveRequestTracker{}
	service.GlobalActiveRequestTracker = tracker

	setting := active_request_setting.GetActiveRequestSetting()
	oldRetentionSeconds := setting.CompletedRetentionSeconds
	setting.CompletedRetentionSeconds = 10

	t.Cleanup(func() {
		service.GlobalActiveRequestTracker = oldTracker
		setting.CompletedRetentionSeconds = oldRetentionSeconds
	})

	return tracker
}

func TestGetActiveRequestsReturnsApiPayload(t *testing.T) {
	tracker := withActiveRequestControllerTestState(t)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracker.Register((&relayInfoForActiveRequestTest{
		requestId:  "req-active",
		model:      "gpt-test",
		userId:     42,
		tokenId:    7,
		cancelFunc: cancel,
	}).RelayInfo(), newActiveRequestTestContext(t, http.MethodPost, "/v1/chat/completions"))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/active-requests", nil)

	GetActiveRequests(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response struct {
		Success                   bool                            `json:"success"`
		Data                      []service.ActiveRequestSnapshot `json:"data"`
		CompletedRetentionSeconds int                             `json:"completed_retention_seconds"`
	}
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !response.Success {
		t.Fatal("success = false, want true")
	}
	if response.CompletedRetentionSeconds != 10 {
		t.Fatalf("completed_retention_seconds = %d, want 10", response.CompletedRetentionSeconds)
	}
	if len(response.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(response.Data))
	}
	if got := response.Data[0]; got.RequestId != "req-active" || got.Status != service.ActiveRequestStatusActive || !got.CanTerminate {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
}

func TestTerminateActiveRequestCancelsTrackedRequest(t *testing.T) {
	tracker := withActiveRequestControllerTestState(t)
	ctx, cancel := context.WithCancel(context.Background())
	tracker.Register((&relayInfoForActiveRequestTest{
		requestId:  "req-cancel",
		model:      "gpt-test",
		cancelFunc: cancel,
	}).RelayInfo(), newActiveRequestTestContext(t, http.MethodPost, "/v1/chat/completions"))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/active-requests/req-cancel", nil)
	c.Params = gin.Params{{Key: "requestId", Value: "req-cancel"}}

	TerminateActiveRequest(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("terminate did not cancel request context")
	}
}

func TestTerminateActiveRequestReturnsNotFoundForMissingRequest(t *testing.T) {
	withActiveRequestControllerTestState(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/active-requests/missing", nil)
	c.Params = gin.Params{{Key: "requestId", Value: "missing"}}

	TerminateActiveRequest(c)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func newActiveRequestTestContext(t *testing.T, method, target string) *gin.Context {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, nil)
	c.Request.RemoteAddr = "127.0.0.1:12345"
	return c
}

type relayInfoForActiveRequestTest struct {
	requestId  string
	model      string
	userId     int
	tokenId    int
	cancelFunc context.CancelFunc
}

func (r *relayInfoForActiveRequestTest) RelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RequestId:       r.requestId,
		OriginModelName: r.model,
		UserId:          r.userId,
		TokenId:         r.tokenId,
		RelayCancelFunc: r.cancelFunc,
	}
}
