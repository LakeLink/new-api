package gemini

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *taskdto.TaskError) {
	return ValidateVeoTaskRequest(c, info, false, 1)
}

// ValidateFinalRequest rechecks model-specific capabilities after channel
// model mapping, before the mapped model is priced or sent upstream.
func (a *TaskAdaptor) ValidateFinalRequest(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	return ValidateVeoTaskRequest(c, info, false, 1)
}

// ValidateVeoTaskRequest validates the shared Veo request shape before its
// duration and resolution are used as billing multipliers. Vertex supports
// generateAudio; the Gemini API always generates audio and has no toggle.
func ValidateVeoTaskRequest(c *gin.Context, info *relaycommon.RelayInfo, allowGenerateAudio bool, maxSampleCount int) (taskErr *taskdto.TaskError) {
	if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionTextGenerate); taskErr != nil {
		return taskErr
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}

	if rawDuration, exists := req.Metadata["durationSeconds"]; exists {
		if _, valid := parseVeoDurationValue(rawDuration); !valid {
			return service.TaskErrorWrapperLocal(fmt.Errorf("durationSeconds must be one of 4, 6, or 8"), "invalid_duration", http.StatusBadRequest)
		}
	}
	if rawResolution, exists := req.Metadata["resolution"]; exists {
		if resolution, ok := rawResolution.(string); !ok || strings.TrimSpace(resolution) == "" {
			return service.TaskErrorWrapperLocal(fmt.Errorf("resolution must be one of 720p, 1080p, or 4k"), "invalid_resolution", http.StatusBadRequest)
		}
	}
	if rawAspectRatio, exists := req.Metadata["aspectRatio"]; exists {
		aspectRatio, ok := rawAspectRatio.(string)
		if !ok || (aspectRatio != "16:9" && aspectRatio != "9:16") {
			return service.TaskErrorWrapperLocal(fmt.Errorf("aspectRatio must be either 16:9 or 9:16"), "invalid_aspect_ratio", http.StatusBadRequest)
		}
	}
	if rawSeed, exists := req.Metadata["seed"]; exists {
		seed, valid := parseVeoSeedValue(rawSeed)
		if !valid || seed > uint64(^uint32(0)) {
			return service.TaskErrorWrapperLocal(fmt.Errorf("seed must be an integer between 0 and 4294967295"), "invalid_seed", http.StatusBadRequest)
		}
	}
	if rawSampleCount, exists := req.Metadata["sampleCount"]; exists {
		if sampleCount, valid := parseVeoDurationValue(rawSampleCount); !valid || sampleCount > maxSampleCount {
			return service.TaskErrorWrapperLocal(fmt.Errorf("sampleCount must be between 1 and %d", maxSampleCount), "invalid_sample_count", http.StatusBadRequest)
		}
	}
	if rawGenerateAudio, exists := req.Metadata["generateAudio"]; exists {
		if !allowGenerateAudio {
			return service.TaskErrorWrapperLocal(fmt.Errorf("generateAudio is not supported by the Gemini API; audio is always enabled"), "unsupported_generate_audio", http.StatusBadRequest)
		}
		if _, ok := rawGenerateAudio.(bool); !ok {
			return service.TaskErrorWrapperLocal(fmt.Errorf("generateAudio must be a boolean"), "invalid_generate_audio", http.StatusBadRequest)
		}
	}
	if req.Duration == 0 && req.Seconds != "" {
		if _, err := strconv.Atoi(req.Seconds); err != nil {
			return service.TaskErrorWrapperLocal(fmt.Errorf("seconds must be one of 4, 6, or 8"), "invalid_duration", http.StatusBadRequest)
		}
	}

	duration := ResolveVeoDuration(req.Metadata, req.Duration, req.Seconds)
	if duration != 4 && duration != 6 && duration != 8 {
		return service.TaskErrorWrapperLocal(fmt.Errorf("durationSeconds must be one of 4, 6, or 8"), "invalid_duration", http.StatusBadRequest)
	}
	resolution := ResolveVeoResolution(req.Metadata, req.Size)
	if resolution != "720p" && resolution != "1080p" && resolution != "4k" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("resolution must be one of 720p, 1080p, or 4k"), "invalid_resolution", http.StatusBadRequest)
	}
	if resolution == "4k" && !VeoSupports4K(info.UpstreamModelName) {
		return service.TaskErrorWrapperLocal(fmt.Errorf("%s does not support 4k output", info.UpstreamModelName), "invalid_resolution", http.StatusBadRequest)
	}
	if resolution != "720p" && duration != 8 {
		return service.TaskErrorWrapperLocal(fmt.Errorf("1080p and 4k output require an 8-second duration"), "invalid_duration", http.StatusBadRequest)
	}
	return nil
}

// BuildRequestURL constructs the Gemini API predictLongRunning endpoint for Veo.
func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	modelName := info.UpstreamModelName
	version := model_setting.GetGeminiVersionSetting(modelName)

	return fmt.Sprintf(
		"%s/%s/models/%s:predictLongRunning",
		a.baseURL,
		version,
		modelName,
	), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-goog-api-key", a.apiKey)
	return nil
}

// BuildRequestBody converts request into the Veo predictLongRunning format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	v, ok := c.Get("task_request")
	if !ok {
		return nil, fmt.Errorf("request not found in context")
	}
	req, ok := v.(relaycommon.TaskSubmitReq)
	if !ok {
		return nil, fmt.Errorf("unexpected task_request type")
	}

	instance := VeoInstance{Prompt: req.Prompt}
	if img := ExtractMultipartImage(c, info); img != nil {
		instance.Image = img
	} else if len(req.Images) > 0 {
		if parsed := ParseImageInput(req.Images[0]); parsed != nil {
			instance.Image = parsed
			info.Action = constant.TaskActionGenerate
		}
	}

	params := &VeoParameters{}
	if err := taskcommon.UnmarshalMetadata(req.Metadata, params); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}
	// Use the same normalized values that EstimateBilling validates and bills.
	// Metadata is user-controlled and must not bypass duration bounds or create
	// a mismatch between the upstream request and the pre-consumed multiplier.
	params.DurationSeconds = ResolveVeoDuration(req.Metadata, req.Duration, req.Seconds)
	params.Resolution = ResolveVeoResolution(req.Metadata, req.Size)
	if params.AspectRatio == "" && req.Size != "" {
		params.AspectRatio = SizeToVeoAspectRatio(req.Size)
	}
	params.Resolution = strings.ToLower(params.Resolution)
	params.SampleCount = 1

	body := VeoRequestPayload{
		Instances:  []VeoInstance{instance},
		Parameters: params,
	}

	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}

	var s submitResponse
	if err := common.Unmarshal(responseBody, &s); err != nil {
		return "", nil, service.TaskErrorWrapper(err, "unmarshal_response_failed", http.StatusInternalServerError)
	}
	if strings.TrimSpace(s.Name) == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("missing operation name"), "invalid_response", http.StatusInternalServerError)
	}
	taskID = taskcommon.EncodeLocalTaskID(s.Name)
	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)
	return taskID, responseBody, nil
}

func (a *TaskAdaptor) GetModelList() []string {
	return []string{
		"veo-3.1-generate-preview",
		"veo-3.1-fast-generate-preview",
		"veo-3.1-lite-generate-preview",
	}
}

func (a *TaskAdaptor) GetChannelName() string {
	return "gemini"
}

// EstimateBilling returns OtherRatios based on durationSeconds and resolution.
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	v, ok := c.Get("task_request")
	if !ok {
		return nil
	}
	req, ok := v.(relaycommon.TaskSubmitReq)
	if !ok {
		return nil
	}

	seconds := ResolveVeoDuration(req.Metadata, req.Duration, req.Seconds)
	resolution := ResolveVeoResolution(req.Metadata, req.Size)
	resRatio := VeoResolutionRatio(info.UpstreamModelName, resolution)

	return map[string]float64{
		"seconds":    float64(seconds),
		"resolution": resRatio,
	}
}

// FetchTask polls task status via the Gemini operations GET endpoint.
func (a *TaskAdaptor) FetchTask(ctx context.Context, baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	upstreamName, err := taskcommon.DecodeLocalTaskID(taskID)
	if err != nil {
		return nil, fmt.Errorf("decode task_id failed: %w", err)
	}

	version := model_setting.GetGeminiVersionSetting("default")
	url := fmt.Sprintf("%s/%s/%s", baseUrl, version, upstreamName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, service.SanitizeNetworkError(err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-goog-api-key", key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return service.DoUpstreamRequest(client, req)
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var op operationResponse
	if err := common.Unmarshal(respBody, &op); err != nil {
		return nil, fmt.Errorf("unmarshal operation response failed: %w", err)
	}

	ti := &relaycommon.TaskInfo{}

	if op.Error.Message != "" {
		ti.Status = model.TaskStatusFailure
		ti.Reason = op.Error.Message
		ti.Progress = "100%"
		return ti, nil
	}

	if !op.Done {
		ti.Status = model.TaskStatusInProgress
		ti.Progress = "50%"
		return ti, nil
	}

	ti.Status = model.TaskStatusSuccess
	ti.Progress = "100%"

	ti.TaskID = taskcommon.EncodeLocalTaskID(op.Name)

	if len(op.Response.GenerateVideoResponse.GeneratedVideos) > 0 {
		if uri := op.Response.GenerateVideoResponse.GeneratedVideos[0].Video.URI; uri != "" {
			ti.RemoteUrl = uri
		}
	}

	return ti, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	upstreamTaskID := task.GetUpstreamTaskID()
	upstreamName, err := taskcommon.DecodeLocalTaskID(upstreamTaskID)
	if err != nil {
		upstreamName = ""
	}
	modelName := extractModelFromOperationName(upstreamName)
	if strings.TrimSpace(modelName) == "" {
		modelName = "veo-3.1-generate-preview"
	}

	video := dto.NewOpenAIVideo()
	video.ID = task.TaskID
	video.Model = modelName
	video.Status = task.Status.ToVideoStatus()
	video.SetProgressStr(task.Progress)
	video.CreatedAt = task.CreatedAt
	if task.FinishTime > 0 {
		video.CompletedAt = task.FinishTime
	} else if task.UpdatedAt > 0 {
		video.CompletedAt = task.UpdatedAt
	}

	return common.Marshal(video)
}

// ============================
// helpers
// ============================

var modelRe = regexp.MustCompile(`models/([^/]+)/operations/`)

func extractModelFromOperationName(name string) string {
	if name == "" {
		return ""
	}
	if m := modelRe.FindStringSubmatch(name); len(m) == 2 {
		return m[1]
	}
	if idx := strings.Index(name, "models/"); idx >= 0 {
		s := name[idx+len("models/"):]
		if p := strings.Index(s, "/operations/"); p > 0 {
			return s[:p]
		}
	}
	return ""
}
