package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// GetActiveRequests returns all currently active relay requests.
func GetActiveRequests(c *gin.Context) {
	snapshots := service.GlobalActiveRequestTracker.List()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    snapshots,
	})
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
