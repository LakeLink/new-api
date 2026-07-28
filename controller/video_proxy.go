package controller

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

// videoProxyError returns a standardized OpenAI-style error response.
func videoProxyError(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
		},
	})
}

func VideoProxy(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		videoProxyError(c, http.StatusBadRequest, "invalid_request_error", "task_id is required")
		return
	}

	userID := c.GetInt("id")
	task, exists, err := model.GetByTaskId(userID, taskID)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to query task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Task not found")
		return
	}

	if task.Status != model.TaskStatusSuccess {
		videoProxyError(c, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Task is not completed yet, current status: %s", task.Status))
		return
	}

	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to get channel for task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to retrieve channel information")
		return
	}
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	var videoURL string
	proxy := channel.GetSetting().Proxy
	client := service.GetSSRFProtectedHTTPClient()
	fetchSetting := system_setting.GetFetchSetting()
	if proxy != "" && (fetchSetting == nil || !fetchSetting.EnableSSRFProtection) {
		// A forward proxy resolves the target independently, so it cannot
		// preserve dial-time SSRF validation. Honor the channel proxy only when
		// the operator has explicitly disabled SSRF protection.
		client, err = service.GetHttpClientWithProxy(proxy)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create proxy client for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy client")
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "", nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create request: %s", err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy request")
		return
	}

	switch channel.Type {
	case constant.ChannelTypeGemini:
		apiKey := task.PrivateData.Key
		if apiKey == "" {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Missing stored API key for Gemini task %s", taskID))
			videoProxyError(c, http.StatusInternalServerError, "server_error", "API key not stored for task")
			return
		}
		videoURL, err = getGeminiVideoURL(ctx, channel, task, apiKey)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to resolve Gemini video URL for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to resolve Gemini video URL")
			return
		}
		req.Header.Set("x-goog-api-key", apiKey)
	case constant.ChannelTypeVertexAi:
		videoURL, err = getVertexVideoURL(ctx, channel, task)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to resolve Vertex video URL for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to resolve Vertex video URL")
			return
		}
	case constant.ChannelTypeOpenAI, constant.ChannelTypeSora:
		videoURL = fmt.Sprintf(
			"%s/v1/videos/%s/content",
			strings.TrimRight(baseURL, "/"),
			url.PathEscape(task.GetUpstreamTaskID()),
		)
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	default:
		// Video URL is stored in PrivateData.ResultURL (fallback to FailReason for old data)
		videoURL = task.GetResultURL()
	}

	videoURL = strings.TrimSpace(videoURL)
	if videoURL == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video URL is empty for task %s", taskID))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}

	if strings.HasPrefix(videoURL, "data:") {
		if err := writeVideoDataURL(c, videoURL); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to decode video data URL for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		}
		return
	}

	if validateErr := service.ValidateSSRFProtectedFetchURL(videoURL); validateErr != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video URL blocked for task %s: %v", taskID, validateErr))
		videoProxyError(c, http.StatusForbidden, "server_error", fmt.Sprintf("request blocked: %v", validateErr))
		return
	}

	req.URL, err = url.Parse(videoURL)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to parse URL %s: %s", common.MaskSensitiveInfo(videoURL), err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy request")
		return
	}

	resp, err := service.DoUpstreamRequest(client, req)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to fetch video from %s: %s", common.MaskSensitiveInfo(videoURL), common.MaskSensitiveInfo(err.Error())))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Upstream returned status %d for %s", resp.StatusCode, common.MaskSensitiveInfo(videoURL)))
		videoProxyError(c, http.StatusBadGateway, "server_error",
			fmt.Sprintf("Upstream service returned status %d", resp.StatusCode))
		return
	}

	maxResponseMB := constant.MaxUpstreamResponseBodyMB
	if maxResponseMB <= 0 {
		maxResponseMB = 128
	}
	maxResponseBytes := common.BytesFromMegabytes(maxResponseMB)
	if resp.ContentLength > maxResponseBytes {
		videoProxyError(c, http.StatusBadGateway, "server_error",
			fmt.Sprintf("Upstream video exceeds %d MB response limit", maxResponseMB))
		return
	}

	reader := bufio.NewReader(resp.Body)
	rawContentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	contentType, _, parseErr := mime.ParseMediaType(rawContentType)
	if rawContentType == "" {
		contentType = ""
		parseErr = nil
	}
	switch {
	case parseErr == nil && strings.HasPrefix(strings.ToLower(contentType), "video/"):
	case parseErr == nil && (contentType == "" || contentType == "application/octet-stream"):
		sniff, _ := reader.Peek(512)
		if sniffed := http.DetectContentType(sniff); strings.HasPrefix(strings.ToLower(sniffed), "video/") {
			contentType = sniffed
		} else {
			contentType = "application/octet-stream"
		}
	default:
		videoProxyError(c, http.StatusBadGateway, "server_error", "Upstream returned non-video content")
		return
	}

	service.CopyUpstreamResponseHeaders(c, resp.Header)
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.Header().Set("Cache-Control", "private, max-age=86400")
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err = io.Copy(c.Writer, http.MaxBytesReader(nil, io.NopCloser(reader), maxResponseBytes)); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to stream video content: %s", err.Error()))
	}
}

func writeVideoDataURL(c *gin.Context, dataURL string) error {
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid data url")
	}

	header := parts[0]
	payload := parts[1]
	if !strings.HasPrefix(header, "data:") || !strings.Contains(header, ";base64") {
		return fmt.Errorf("unsupported data url")
	}

	mimeType := strings.TrimPrefix(header, "data:")
	mimeType = strings.TrimSuffix(mimeType, ";base64")
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	parsedMimeType, _, err := mime.ParseMediaType(mimeType)
	if err != nil || !strings.HasPrefix(strings.ToLower(parsedMimeType), "video/") {
		return fmt.Errorf("unsupported data URL media type")
	}

	maxResponseMB := constant.MaxUpstreamResponseBodyMB
	if maxResponseMB <= 0 {
		maxResponseMB = 128
	}
	maxResponseBytes := common.BytesFromMegabytes(maxResponseMB)
	videoBytes, err := io.ReadAll(io.LimitReader(
		base64.NewDecoder(base64.StdEncoding, strings.NewReader(payload)),
		common.ReadLimitWithOverrunByte(maxResponseBytes),
	))
	if err != nil {
		videoBytes, err = io.ReadAll(io.LimitReader(
			base64.NewDecoder(base64.RawStdEncoding, strings.NewReader(payload)),
			common.ReadLimitWithOverrunByte(maxResponseBytes),
		))
		if err != nil {
			return err
		}
	}
	if int64(len(videoBytes)) > maxResponseBytes {
		return fmt.Errorf("video data exceeds %d MB response limit", maxResponseMB)
	}

	c.Writer.Header().Set("Content-Type", parsedMimeType)
	c.Writer.Header().Set("Cache-Control", "private, max-age=86400")
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(videoBytes)
	return err
}
