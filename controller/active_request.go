package controller

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/active_request_setting"
	"github.com/gin-gonic/gin"
)

// GetActiveRequests returns active and recently completed relay requests.
func GetActiveRequests(c *gin.Context) {
	snapshots := service.GlobalActiveRequestTracker.List()
	c.JSON(http.StatusOK, gin.H{
		"success":                     true,
		"data":                        snapshots,
		"completed_retention_seconds": active_request_setting.GetActiveRequestSetting().CompletedRetentionSeconds,
	})
}

// StreamActiveRequests sends full snapshots, including elapsed times and retention expiry.
func StreamActiveRequests(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// Rotate connections so clients refresh credentials and bound XHR response buffering.
	lifetime := time.NewTimer(time.Minute)
	defer lifetime.Stop()
	for {
		if c.Request.Context().Err() != nil {
			return
		}
		if !middleware.RevalidateAdminAuth(c) {
			_, _ = c.Writer.WriteString("event: unauthorized\ndata: {}\n\n")
			c.Writer.Flush()
			return
		}
		payload, err := common.Marshal(gin.H{
			"success":                     true,
			"data":                        service.GlobalActiveRequestTracker.List(),
			"completed_retention_seconds": active_request_setting.GetActiveRequestSetting().CompletedRetentionSeconds,
		})
		if err != nil {
			return
		}
		if _, err = c.Writer.WriteString("event: snapshot\ndata: " + string(payload) + "\n\n"); err != nil {
			return
		}
		c.Writer.Flush()
		select {
		case <-c.Request.Context().Done():
			return
		case <-lifetime.C:
			return
		case <-ticker.C:
		}
	}
}

// TerminateActiveRequest cancels a specific active request by its ID.
func TerminateActiveRequest(c *gin.Context) {
	requestId := c.Param("requestId")
	if requestId == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "requestId is required",
		})
		return
	}

	ok := service.GlobalActiveRequestTracker.Terminate(requestId)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "request not found or already completed",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "request terminated",
	})
}
