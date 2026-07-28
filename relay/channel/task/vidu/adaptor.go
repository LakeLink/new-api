package vidu

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/pkg/errors"
)

// ============================
// Request / Response structures
// ============================

type requestPayload struct {
	Model             string   `json:"model"`
	Images            []string `json:"images"`
	Prompt            string   `json:"prompt,omitempty"`
	Duration          int      `json:"duration,omitempty"`
	Seed              *int     `json:"seed,omitempty"`
	Resolution        string   `json:"resolution,omitempty"`
	MovementAmplitude string   `json:"movement_amplitude,omitempty"`
	Bgm               *bool    `json:"bgm,omitempty"`
	Payload           string   `json:"payload,omitempty"`
	CallbackUrl       string   `json:"callback_url,omitempty"`
}

type responsePayload struct {
	TaskId            string   `json:"task_id"`
	State             string   `json:"state"`
	Model             string   `json:"model"`
	Images            []string `json:"images"`
	Prompt            string   `json:"prompt"`
	Duration          int      `json:"duration"`
	Seed              int      `json:"seed"`
	Resolution        string   `json:"resolution"`
	Bgm               bool     `json:"bgm"`
	MovementAmplitude string   `json:"movement_amplitude"`
	Payload           string   `json:"payload"`
	CreatedAt         string   `json:"created_at"`
}

type taskResultResponse struct {
	State     string     `json:"state"`
	ErrCode   string     `json:"err_code"`
	Credits   int        `json:"credits"`
	Payload   string     `json:"payload"`
	Creations []creation `json:"creations"`
}

type creation struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	CoverURL string `json:"cover_url"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if err := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); err != nil {
		return err
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapper(err, "get_task_request_failed", http.StatusBadRequest)
	}
	action := constant.TaskActionTextGenerate
	if meatAction, ok := req.Metadata["action"]; ok {
		action, _ = meatAction.(string)
	} else if req.HasImage() {
		action = constant.TaskActionGenerate
		if info.ChannelType == constant.ChannelTypeVidu {
			// vidu 增加 首尾帧生视频和参考图生视频
			if len(req.Images) == 2 {
				action = constant.TaskActionFirstTailGenerate
			} else if len(req.Images) > 2 {
				action = constant.TaskActionReferenceGenerate
			}
		}
	}
	switch action {
	case constant.TaskActionTextGenerate, constant.TaskActionGenerate,
		constant.TaskActionFirstTailGenerate, constant.TaskActionReferenceGenerate:
	default:
		return service.TaskErrorWrapperLocal(fmt.Errorf("unsupported Vidu action %q", action), "invalid_request", http.StatusBadRequest)
	}
	info.Action = action

	return nil
}

func (a *TaskAdaptor) ValidateFinalRequest(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapper(err, "get_task_request_failed", http.StatusBadRequest)
	}
	if _, err := a.convertToRequestPayload(&req, info); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	return nil
}

// EstimateBilling preserves the configured price for each model/action's
// provider-default payload and scales only documented higher-cost variants.
// Credit schedules: https://platform.vidu.com/docs/pricing
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	payload, err := a.convertToRequestPayload(&req, info)
	if err != nil {
		return nil
	}
	defaultDuration, defaultResolution := viduDefaults(payload.Model)
	action := viduAction(info)
	baseCredits, ok := viduVideoCredits(payload.Model, action, defaultDuration, defaultResolution, false)
	if !ok {
		return nil
	}
	bgm := payload.Bgm != nil && *payload.Bgm
	requestCredits, ok := viduVideoCredits(payload.Model, action, payload.Duration, payload.Resolution, bgm)
	if !ok || requestCredits <= baseCredits {
		return nil
	}
	return map[string]float64{"provider_cost": requestCredits / baseCredits}
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return nil, fmt.Errorf("request not found in context")
	}
	req, ok := v.(relaycommon.TaskSubmitReq)
	if !ok {
		return nil, fmt.Errorf("invalid request type in context")
	}

	body, err := a.convertToRequestPayload(&req, info)
	if err != nil {
		return nil, err
	}

	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	var path string
	switch info.Action {
	case constant.TaskActionGenerate:
		path = "/img2video"
	case constant.TaskActionFirstTailGenerate:
		path = "/start-end2video"
	case constant.TaskActionReferenceGenerate:
		path = "/reference2video"
	default:
		path = "/text2video"
	}
	return fmt.Sprintf("%s/ent/v2%s", a.baseURL, path), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+info.ApiKey)
	return nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}

	var vResp responsePayload
	err = common.Unmarshal(responseBody, &vResp)
	if err != nil {
		taskErr = service.TaskErrorWrapper(
			errors.Wrapf(err, "unmarshal %d-byte response body", len(responseBody)),
			"unmarshal_response_failed",
			http.StatusInternalServerError,
		)
		return
	}

	if vResp.State == "failed" {
		taskErr = service.TaskErrorWrapperLocal(fmt.Errorf("task failed"), "task_failed", http.StatusBadRequest)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)
	return vResp.TaskId, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(ctx context.Context, baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	requestURL := fmt.Sprintf("%s/ent/v2/tasks/%s/creations", strings.TrimRight(baseUrl, "/"), url.PathEscape(taskID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, service.SanitizeNetworkError(err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return service.DoUpstreamRequest(client, req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return []string{"viduq2", "viduq1", "vidu2.0", "vidu1.5"}
}

func (a *TaskAdaptor) GetChannelName() string {
	return "vidu"
}

// ============================
// helpers
// ============================

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*requestPayload, error) {
	modelName := taskcommon.DefaultString(info.UpstreamModelName, "viduq1")
	defaultDuration, defaultResolution := viduDefaults(modelName)
	resolution := normalizeViduResolution(req.Size)
	if resolution == "" {
		resolution = defaultResolution
	}
	r := requestPayload{
		Model:             modelName,
		Images:            req.Images,
		Prompt:            req.Prompt,
		Duration:          taskcommon.DefaultInt(req.Duration, defaultDuration),
		Resolution:        resolution,
		MovementAmplitude: "auto",
	}
	if err := taskcommon.UnmarshalMetadata(req.Metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}
	// Model routing determines the configured price. Preserve it after native
	// metadata is applied and bound provider-native duration overrides.
	r.Model = modelName
	r.Resolution = normalizeViduResolution(r.Resolution)
	modelLower := strings.ToLower(modelName)
	action := viduAction(info)
	switch {
	case strings.HasPrefix(modelLower, "viduq3"):
		minDuration := 1
		if action == constant.TaskActionReferenceGenerate && modelLower != "viduq3-mix" {
			minDuration = 3
		}
		if r.Duration < minDuration || r.Duration > 16 {
			return nil, fmt.Errorf("duration must be between %d and 16 seconds for %s", minDuration, modelName)
		}
	case strings.HasPrefix(modelLower, "viduq2"):
		maxDuration := 10
		if action == constant.TaskActionFirstTailGenerate {
			maxDuration = 8
		}
		if r.Duration < 1 || r.Duration > maxDuration {
			return nil, fmt.Errorf("duration must be between 1 and %d seconds for %s", maxDuration, modelName)
		}
	case modelLower == "viduq1" || modelLower == "viduq1-classic":
		if r.Duration != 5 {
			return nil, fmt.Errorf("duration must be 5 seconds for %s", modelName)
		}
	case modelLower == "vidu2.0":
		if action == constant.TaskActionReferenceGenerate {
			if r.Duration != 4 {
				return nil, fmt.Errorf("duration must be 4 seconds for %s reference-to-video", modelName)
			}
		} else if r.Duration != 4 && r.Duration != 8 {
			return nil, fmt.Errorf("duration must be either 4 or 8 seconds for %s", modelName)
		}
	default:
		if r.Duration < 1 || r.Duration > 16 {
			return nil, fmt.Errorf("duration must be between 1 and 16 seconds")
		}
	}
	if err := validateViduResolution(modelLower, action, r.Duration, r.Resolution); err != nil {
		return nil, err
	}
	switch r.MovementAmplitude {
	case "auto", "small", "medium", "large":
	default:
		return nil, fmt.Errorf("movement_amplitude must be one of auto, small, medium, or large")
	}
	if len(r.Payload) > 1_048_576 {
		return nil, fmt.Errorf("payload must not exceed 1048576 bytes")
	}
	return &r, nil
}

func viduAction(info *relaycommon.RelayInfo) string {
	if info != nil && info.TaskRelayInfo != nil && info.Action != "" {
		return info.Action
	}
	return constant.TaskActionTextGenerate
}

func viduDefaults(modelName string) (int, string) {
	switch {
	case strings.EqualFold(modelName, "vidu2.0"):
		return 4, "360p"
	case strings.HasPrefix(strings.ToLower(modelName), "viduq2"),
		strings.HasPrefix(strings.ToLower(modelName), "viduq3"):
		return 5, "720p"
	default:
		return 5, "1080p"
	}
}

func normalizeViduResolution(resolution string) string {
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	switch {
	case resolution == "540p", strings.Contains(resolution, "540"):
		return "540p"
	case resolution == "720p", strings.Contains(resolution, "720"):
		return "720p"
	case resolution == "1080p", strings.Contains(resolution, "1080"):
		return "1080p"
	case resolution == "360p", strings.Contains(resolution, "360"):
		return "360p"
	default:
		return resolution
	}
}

func validateViduResolution(modelName, action string, duration int, resolution string) error {
	var allowed []string
	switch {
	case modelName == "viduq3-mix":
		allowed = []string{"720p", "1080p"}
	case strings.HasPrefix(modelName, "viduq3"), strings.HasPrefix(modelName, "viduq2"):
		allowed = []string{"540p", "720p", "1080p"}
	case modelName == "viduq1" || modelName == "viduq1-classic":
		allowed = []string{"1080p"}
	case modelName == "vidu2.0":
		if duration == 8 {
			allowed = []string{"720p"}
		} else if action == constant.TaskActionReferenceGenerate {
			allowed = []string{"360p", "720p"}
		} else {
			allowed = []string{"360p", "720p", "1080p"}
		}
	default:
		return nil
	}
	for _, candidate := range allowed {
		if resolution == candidate {
			return nil
		}
	}
	return fmt.Errorf("resolution %q is not supported by %s for this duration", resolution, modelName)
}

func viduVideoCredits(modelName, action string, duration int, resolution string, bgm bool) (float64, bool) {
	modelName = strings.ToLower(modelName)
	var credits float64
	switch {
	case strings.HasPrefix(modelName, "viduq3"):
		var rate float64
		switch {
		case action == constant.TaskActionReferenceGenerate && modelName == "viduq3-mix":
			rate = map[string]float64{"720p": 24, "1080p": 29}[resolution]
		case action == constant.TaskActionReferenceGenerate && modelName == "viduq3-turbo":
			rate = map[string]float64{"540p": 4, "720p": 10, "1080p": 13}[resolution]
		case action == constant.TaskActionReferenceGenerate:
			rate = map[string]float64{"540p": 7, "720p": 12, "1080p": 15}[resolution]
		case modelName == "viduq3-turbo":
			rate = map[string]float64{"540p": 7, "720p": 11, "1080p": 13}[resolution]
		case modelName == "viduq3-pro-fast":
			rate = map[string]float64{"720p": 20, "1080p": 25}[resolution]
		default:
			rate = map[string]float64{"540p": 9, "720p": 20, "1080p": 24}[resolution]
		}
		if rate == 0 {
			return 0, false
		}
		return rate * float64(duration), true
	case modelName == "viduq2":
		switch action {
		case constant.TaskActionTextGenerate:
			credits = viduLinearCredits(duration, map[string]float64{"540p": 10, "720p": 15, "1080p": 20}[resolution], map[string]float64{"540p": 2, "720p": 5, "1080p": 10}[resolution])
		case constant.TaskActionReferenceGenerate:
			credits = viduLinearCredits(duration, map[string]float64{"540p": 15, "720p": 25, "1080p": 75}[resolution], map[string]float64{"540p": 5, "720p": 5, "1080p": 10}[resolution])
		}
	case modelName == "viduq2-turbo" &&
		(action == constant.TaskActionGenerate || action == constant.TaskActionFirstTailGenerate):
		switch resolution {
		case "540p":
			credits = viduLinearCredits(duration, 6, 2)
		case "720p":
			if duration == 1 {
				credits = 8
			} else {
				credits = 10 + 10*float64(duration-2)
			}
		case "1080p":
			credits = viduLinearCredits(duration, 35, 10)
		}
	case modelName == "viduq2-pro" &&
		(action == constant.TaskActionGenerate || action == constant.TaskActionFirstTailGenerate):
		switch resolution {
		case "540p":
			if duration == 1 {
				credits = 8
			} else {
				credits = 10 + 5*float64(duration-2)
			}
		case "720p":
			credits = viduLinearCredits(duration, 15, 10)
		case "1080p":
			credits = viduLinearCredits(duration, 55, 15)
		}
	case modelName == "viduq2-pro-fast" &&
		(action == constant.TaskActionGenerate || action == constant.TaskActionFirstTailGenerate):
		switch resolution {
		case "720p":
			credits = viduLinearCredits(duration, 8, 2)
		case "1080p":
			credits = viduLinearCredits(duration, 16, 4)
		}
	case modelName == "viduq2-pro" && action == constant.TaskActionReferenceGenerate:
		credits = viduLinearCredits(duration, map[string]float64{"540p": 20, "720p": 30, "1080p": 85}[resolution], map[string]float64{"540p": 5, "720p": 5, "1080p": 10}[resolution])
	case modelName == "viduq1" || modelName == "viduq1-classic":
		credits = 80
	case modelName == "vidu2.0":
		switch action {
		case constant.TaskActionGenerate, constant.TaskActionFirstTailGenerate:
			switch {
			case duration == 4 && resolution == "360p":
				credits = 20
			case duration == 4 && resolution == "720p":
				credits = 40
			case duration == 4 && resolution == "1080p", duration == 8 && resolution == "720p":
				credits = 100
			}
		case constant.TaskActionReferenceGenerate:
			if duration == 4 && (resolution == "360p" || resolution == "720p") {
				credits = 80
			}
		}
	}
	if credits == 0 {
		return 0, false
	}
	if bgm && strings.HasPrefix(modelName, "viduq2") && duration < 9 &&
		(action == constant.TaskActionGenerate || action == constant.TaskActionReferenceGenerate) {
		credits += 15
	}
	return credits, true
}

func viduLinearCredits(duration int, firstSecond, eachAdditionalSecond float64) float64 {
	if duration < 1 || firstSecond == 0 || eachAdditionalSecond == 0 {
		return 0
	}
	return firstSecond + eachAdditionalSecond*float64(duration-1)
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	taskInfo := &relaycommon.TaskInfo{}

	var taskResp taskResultResponse
	err := common.Unmarshal(respBody, &taskResp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal response body")
	}

	state := taskResp.State
	switch state {
	case "created", "queueing":
		taskInfo.Status = model.TaskStatusSubmitted
	case "processing":
		taskInfo.Status = model.TaskStatusInProgress
	case "success":
		taskInfo.Status = model.TaskStatusSuccess
		if len(taskResp.Creations) > 0 {
			taskInfo.Url = taskResp.Creations[0].URL
		}
	case "failed":
		taskInfo.Status = model.TaskStatusFailure
		if taskResp.ErrCode != "" {
			taskInfo.Reason = taskResp.ErrCode
		}
	default:
		return nil, fmt.Errorf("unknown task state: %s", state)
	}

	return taskInfo, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var viduResp taskResultResponse
	if err := common.Unmarshal(originTask.Data, &viduResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal vidu task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt

	if len(viduResp.Creations) > 0 && viduResp.Creations[0].URL != "" {
		openAIVideo.SetMetadata("url", viduResp.Creations[0].URL)
	}

	if viduResp.State == "failed" && viduResp.ErrCode != "" {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: viduResp.ErrCode,
			Code:    viduResp.ErrCode,
		}
	}

	return common.Marshal(openAIVideo)
}
