package vidu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/pkg/errors"
)

// ============================
// Request / Response structures
// ============================

type requestPayload struct {
	Model             string   `json:"model"`
	Images            []string `json:"images,omitempty"`
	Prompt            string   `json:"prompt,omitempty"`
	Duration          int      `json:"duration,omitempty"`
	Seed              *int     `json:"seed,omitempty"`
	Resolution        string   `json:"resolution,omitempty"`
	Style             *string  `json:"style,omitempty"`
	AspectRatio       *string  `json:"aspect_ratio,omitempty"`
	MovementAmplitude string   `json:"movement_amplitude,omitempty"`
	Bgm               *bool    `json:"bgm,omitempty"`
	IsRec             *bool    `json:"is_rec,omitempty"`
	Audio             *bool    `json:"audio,omitempty"`
	AudioType         *string  `json:"audio_type,omitempty"`
	VoiceID           *string  `json:"voice_id,omitempty"`
	OffPeak           *bool    `json:"off_peak,omitempty"`
	Payload           *string  `json:"payload,omitempty"`
	CallbackURL       *string  `json:"callback_url,omitempty"`
}

type responsePayload struct {
	TaskId            string          `json:"task_id"`
	State             string          `json:"state"`
	Model             string          `json:"model"`
	Images            []string        `json:"images"`
	Prompt            string          `json:"prompt"`
	Duration          int             `json:"duration"`
	Seed              int             `json:"seed"`
	Resolution        string          `json:"resolution"`
	Bgm               bool            `json:"bgm"`
	MovementAmplitude string          `json:"movement_amplitude"`
	Payload           string          `json:"payload"`
	Credits           json.RawMessage `json:"credits"`
	CreatedAt         string          `json:"created_at"`
}

type taskResultResponse struct {
	State     string          `json:"state"`
	ErrCode   string          `json:"err_code"`
	Credits   json.RawMessage `json:"credits"`
	Payload   string          `json:"payload"`
	Creations []creation      `json:"creations"`
}

type creation struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	CoverURL string `json:"cover_url"`
}

// Current Vidu video schedules top out at 16s × 29 credits/s, plus the
// documented 10-credit prompt-recommendation add-on.
const maxViduCredits = 474

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

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
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

func (a *TaskAdaptor) ValidateFinalRequest(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapper(err, "get_task_request_failed", http.StatusBadRequest)
	}
	if _, err := a.convertToRequestPayload(&req, info); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	return nil
}

// EstimateBilling normalizes every action of a model against one canonical
// provider payload. A model has one configured base price, so action-specific
// defaults must not each be treated as ratio 1.
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
	canonicalCredits, ok := viduCanonicalCredits(payload.Model)
	if !ok {
		return nil
	}
	action := viduAction(info)
	audio := payload.Audio != nil && *payload.Audio
	isRec := payload.IsRec != nil && *payload.IsRec
	bgm := payload.Bgm != nil && *payload.Bgm
	requestCredits, ok := viduVideoCredits(payload.Model, action, payload.Duration, payload.Resolution, audio, isRec)
	if !ok {
		return nil
	}
	// Current Q2 pricing documents a 15-credit direct-audio add-on. The image
	// API also exposes audio for Q1/2.0 without publishing a separate row, so
	// reserve the same ceiling there and reconcile downward from submit credits.
	if audio && !strings.HasPrefix(payload.Model, "viduq2") &&
		!strings.HasPrefix(payload.Model, "viduq3") {
		requestCredits += 15
	}
	// The public table does not give BGM its own row. Reserve the historical
	// 15-credit ceiling independently from direct audio, then reconcile down to
	// Vidu's authoritative submit credits before persistence.
	if bgm {
		requestCredits += 15
	}
	return map[string]float64{"provider_cost": requestCredits / canonicalCredits}
}

// AdjustBillingOnSubmit reconciles the conservative estimate with Vidu's
// provider-reported credits before the accepted task and its consume log are
// persisted. Both integer and quoted-integer responses exist in Vidu's current
// API documentation, so parse both forms and fail closed on invalid values.
func (a *TaskAdaptor) AdjustBillingOnSubmit(info *relaycommon.RelayInfo, taskData []byte) map[string]float64 {
	if info == nil || len(taskData) == 0 {
		return nil
	}
	var response responsePayload
	if err := common.Unmarshal(taskData, &response); err != nil {
		common.SysError("unmarshal Vidu submit credits failed: " + err.Error())
		return nil
	}
	credits, err := parseViduCredits(response.Credits)
	if err != nil {
		common.SysError("invalid Vidu submit credits: " + err.Error())
		return nil
	}
	modelName := strings.ToLower(strings.TrimSpace(info.UpstreamModelName))
	responseModel := strings.ToLower(strings.TrimSpace(response.Model))
	if modelName == "" {
		modelName = responseModel
	}
	if modelName == "" || responseModel == "" || responseModel != modelName {
		common.SysError(fmt.Sprintf(
			"Vidu submit response model %q does not match routed model %q",
			response.Model,
			info.UpstreamModelName,
		))
		return nil
	}
	canonicalCredits, ok := viduCanonicalCredits(modelName)
	if !ok {
		return nil
	}
	estimatedRatios := info.PriceData.OtherRatios()
	estimatedProviderCost, ok := estimatedRatios["provider_cost"]
	if !ok {
		common.SysError("Vidu submit credits cannot be reconciled without the pre-dispatch provider_cost estimate")
		return nil
	}
	estimatedCredits := estimatedProviderCost * canonicalCredits
	if float64(credits) > estimatedCredits+1e-9 {
		// The provider has already accepted the task. Increasing the reservation
		// here can fail and orphan that upstream task, so never turn a post-accept
		// price surprise into a user charge. Keep the conservative pre-charge and
		// surface the anomaly for an administrator to update the schedule.
		common.SysError(fmt.Sprintf(
			"Vidu submit credits %d exceed the pre-dispatch estimate %.2f for %s",
			credits,
			estimatedCredits,
			modelName,
		))
		return nil
	}
	adjustedRatios := make(map[string]float64, len(estimatedRatios))
	for key, ratio := range estimatedRatios {
		adjustedRatios[key] = ratio
	}
	adjustedRatios["provider_cost"] = float64(credits) / canonicalCredits
	return adjustedRatios
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

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
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
	return []string{
		"viduq3-pro-fast",
		"viduq3-turbo",
		"viduq3-pro",
		"viduq3-mix",
		"viduq3",
		"viduq2-pro-fast",
		"viduq2-pro",
		"viduq2-turbo",
		"viduq2",
		"viduq1",
		"viduq1-classic",
		"vidu2.0",
	}
}

func (a *TaskAdaptor) GetChannelName() string {
	return "vidu"
}

// ============================
// helpers
// ============================

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*requestPayload, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	modelName := "viduq1"
	if info != nil {
		modelName = taskcommon.DefaultString(info.UpstreamModelName, modelName)
	}
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	action := viduAction(info)
	if err := validateViduModelAction(modelName, action); err != nil {
		return nil, err
	}
	if err := validateViduMetadata(req.Metadata, action); err != nil {
		return nil, err
	}
	defaultDuration, _ := viduDefaults(modelName)
	r := requestPayload{
		Model:             modelName,
		Images:            req.Images,
		Prompt:            req.Prompt,
		Duration:          taskcommon.DefaultInt(req.Duration, defaultDuration),
		Resolution:        normalizeViduResolution(req.Size),
		MovementAmplitude: "auto",
	}
	if err := taskcommon.UnmarshalMetadata(req.Metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}
	// Model routing determines the configured price. Preserve it after native
	// metadata is applied and bound provider-native duration overrides.
	r.Model = modelName
	r.Resolution = normalizeViduResolution(r.Resolution)
	if err := validateViduDuration(modelName, action, r.Duration); err != nil {
		return nil, err
	}
	if r.Resolution == "" {
		r.Resolution = viduDefaultResolution(modelName, r.Duration)
	}
	if err := validateViduResolution(modelName, action, r.Duration, r.Resolution); err != nil {
		return nil, err
	}
	if err := validateViduImagesAndPrompt(action, r.Images, r.Prompt); err != nil {
		return nil, err
	}
	switch r.MovementAmplitude {
	case "auto", "small", "medium", "large":
	default:
		return nil, fmt.Errorf("movement_amplitude must be one of auto, small, medium, or large")
	}
	if err := validateViduOptions(modelName, action, &r); err != nil {
		return nil, err
	}
	if r.Payload != nil {
		if !utf8.ValidString(*r.Payload) {
			return nil, fmt.Errorf("payload must be valid UTF-8")
		}
		if utf8.RuneCountInString(*r.Payload) > 1_048_576 {
			return nil, fmt.Errorf("payload must not exceed 1048576 characters")
		}
	}
	return &r, nil
}

func validateViduMetadata(metadata map[string]any, action string) error {
	for key, value := range metadata {
		switch key {
		case "action":
			metadataAction, ok := value.(string)
			if !ok || metadataAction != action {
				return fmt.Errorf("metadata action must match the routed Vidu action %q", action)
			}
		case "model", "images", "prompt", "duration", "seed", "resolution",
			"style", "aspect_ratio", "movement_amplitude", "bgm", "is_rec",
			"audio", "audio_type", "voice_id", "off_peak", "payload", "callback_url":
		default:
			return fmt.Errorf("metadata field %q is not supported by Vidu", key)
		}
	}
	return nil
}

func validateViduModelAction(modelName, action string) error {
	allowed := false
	switch modelName {
	case "viduq3-pro-fast":
		allowed = action == constant.TaskActionGenerate
	case "viduq3-pro":
		allowed = action == constant.TaskActionTextGenerate ||
			action == constant.TaskActionGenerate ||
			action == constant.TaskActionFirstTailGenerate
	case "viduq3-turbo":
		allowed = action == constant.TaskActionTextGenerate ||
			action == constant.TaskActionGenerate ||
			action == constant.TaskActionFirstTailGenerate ||
			action == constant.TaskActionReferenceGenerate
	case "viduq3-mix", "viduq3":
		allowed = action == constant.TaskActionReferenceGenerate
	case "viduq2-pro-fast", "viduq2-turbo":
		allowed = action == constant.TaskActionGenerate ||
			action == constant.TaskActionFirstTailGenerate
	case "viduq2-pro":
		allowed = action == constant.TaskActionGenerate ||
			action == constant.TaskActionFirstTailGenerate ||
			action == constant.TaskActionReferenceGenerate
	case "viduq2":
		allowed = action == constant.TaskActionTextGenerate ||
			action == constant.TaskActionReferenceGenerate
	case "viduq1":
		allowed = action == constant.TaskActionTextGenerate ||
			action == constant.TaskActionGenerate ||
			action == constant.TaskActionFirstTailGenerate ||
			action == constant.TaskActionReferenceGenerate
	case "viduq1-classic":
		allowed = action == constant.TaskActionGenerate ||
			action == constant.TaskActionFirstTailGenerate
	case "vidu2.0":
		allowed = action == constant.TaskActionGenerate ||
			action == constant.TaskActionFirstTailGenerate ||
			action == constant.TaskActionReferenceGenerate
	default:
		return fmt.Errorf("unsupported Vidu model %q", modelName)
	}
	if !allowed {
		return fmt.Errorf("Vidu model %s does not support action %s", modelName, action)
	}
	return nil
}

func validateViduImagesAndPrompt(action string, images []string, prompt string) error {
	if !utf8.ValidString(prompt) {
		return fmt.Errorf("prompt must be valid UTF-8")
	}
	if utf8.RuneCountInString(prompt) > 5000 {
		return fmt.Errorf("prompt must not exceed 5000 characters")
	}
	if (action == constant.TaskActionTextGenerate || action == constant.TaskActionReferenceGenerate) &&
		strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("prompt is required for Vidu action %s", action)
	}
	for index, image := range images {
		if strings.TrimSpace(image) == "" {
			return fmt.Errorf("images[%d] must not be empty", index)
		}
	}
	switch action {
	case constant.TaskActionTextGenerate:
		if len(images) != 0 {
			return fmt.Errorf("text-to-video must not include images")
		}
	case constant.TaskActionGenerate:
		if len(images) != 1 {
			return fmt.Errorf("image-to-video requires exactly 1 image")
		}
	case constant.TaskActionFirstTailGenerate:
		if len(images) != 2 {
			return fmt.Errorf("start-end-to-video requires exactly 2 images")
		}
	case constant.TaskActionReferenceGenerate:
		if len(images) < 1 || len(images) > 7 {
			return fmt.Errorf("reference-to-video requires between 1 and 7 images")
		}
	}
	return nil
}

func validateViduOptions(modelName, action string, payload *requestPayload) error {
	if payload.Style != nil {
		style := strings.ToLower(strings.TrimSpace(*payload.Style))
		if action != constant.TaskActionTextGenerate || modelName != "viduq1" {
			return fmt.Errorf("style is supported only by viduq1 text-to-video")
		}
		if style != "general" && style != "anime" {
			return fmt.Errorf("style must be either general or anime")
		}
		payload.Style = &style
	}
	if payload.AspectRatio != nil {
		aspectRatio := strings.TrimSpace(*payload.AspectRatio)
		if action != constant.TaskActionTextGenerate && action != constant.TaskActionReferenceGenerate {
			return fmt.Errorf("aspect_ratio is supported only by text-to-video and reference-to-video")
		}
		allowed := aspectRatio == "16:9" || aspectRatio == "9:16" || aspectRatio == "1:1"
		if (strings.HasPrefix(modelName, "viduq2") || strings.HasPrefix(modelName, "viduq3")) &&
			(aspectRatio == "3:4" || aspectRatio == "4:3") {
			allowed = true
		}
		if !allowed {
			return fmt.Errorf("aspect_ratio %q is not supported by %s", aspectRatio, modelName)
		}
		payload.AspectRatio = &aspectRatio
	}
	if payload.IsRec != nil &&
		action != constant.TaskActionGenerate &&
		action != constant.TaskActionFirstTailGenerate {
		return fmt.Errorf("is_rec is supported only by image-to-video and start-end-to-video")
	}
	if payload.Bgm != nil && *payload.Bgm {
		if strings.HasPrefix(modelName, "viduq3") {
			return fmt.Errorf("bgm is not supported by ViduQ3 models")
		}
		if strings.HasPrefix(modelName, "viduq2") && payload.Duration >= 9 {
			return fmt.Errorf("bgm is not supported for ViduQ2 durations of 9 or 10 seconds")
		}
	}
	if payload.Audio != nil {
		q3Audio := strings.HasPrefix(modelName, "viduq3")
		imageOrReferenceAudio :=
			(action == constant.TaskActionGenerate || action == constant.TaskActionReferenceGenerate)
		if !q3Audio && !imageOrReferenceAudio {
			return fmt.Errorf("audio is not supported by %s for action %s", modelName, action)
		}
	}
	if payload.AudioType != nil {
		audioType := strings.ToLower(strings.TrimSpace(*payload.AudioType))
		if payload.Audio == nil || !*payload.Audio {
			return fmt.Errorf("audio_type requires audio=true")
		}
		switch audioType {
		case "all", "speech_only", "sound_effect_only":
		default:
			return fmt.Errorf("audio_type must be one of all, speech_only, or sound_effect_only")
		}
		if strings.HasPrefix(modelName, "viduq3") && audioType != "all" {
			return fmt.Errorf("ViduQ3 models do not support split audio_type values")
		}
		payload.AudioType = &audioType
	}
	if payload.VoiceID != nil {
		voiceID := strings.TrimSpace(*payload.VoiceID)
		if voiceID == "" {
			return fmt.Errorf("voice_id must not be empty")
		}
		if action != constant.TaskActionGenerate ||
			strings.HasPrefix(modelName, "viduq3") ||
			payload.Audio == nil ||
			!*payload.Audio {
			return fmt.Errorf("voice_id requires non-Q3 image-to-video with audio=true")
		}
		payload.VoiceID = &voiceID
	}
	if payload.OffPeak != nil && *payload.OffPeak {
		// Vidu may keep off-peak jobs for 48 hours, while this gateway's task
		// timeout currently defaults to 24 hours and has no per-task override.
		// Forwarding the flag would let the gateway refund a still-running task.
		return fmt.Errorf("off_peak is not supported until Vidu tasks can use a provider-specific 48-hour timeout")
	}
	if payload.CallbackURL != nil {
		callbackURL, err := url.ParseRequestURI(*payload.CallbackURL)
		if err != nil || callbackURL.Host == "" ||
			(callbackURL.Scheme != "http" && callbackURL.Scheme != "https") {
			return fmt.Errorf("callback_url must be an absolute HTTP or HTTPS URL")
		}
	}
	return nil
}

func validateViduDuration(modelName, action string, duration int) error {
	switch {
	case modelName == "viduq3-pro-fast" ||
		modelName == "viduq3-pro" ||
		modelName == "viduq3-turbo" ||
		modelName == "viduq3-mix" ||
		modelName == "viduq3":
		minDuration := 1
		// Q3 text/image/start-end generation supports 1–16 seconds. The
		// reference-to-video endpoint documents 3–16 seconds for Q3 Turbo/Q3;
		// Q3 Mix retains its separately documented 1–16 second range.
		if action == constant.TaskActionReferenceGenerate && modelName != "viduq3-mix" {
			minDuration = 3
		}
		if duration < minDuration || duration > 16 {
			return fmt.Errorf("duration must be between %d and 16 seconds for %s", minDuration, modelName)
		}
	case modelName == "viduq2-pro-fast" ||
		modelName == "viduq2-pro" ||
		modelName == "viduq2-turbo" ||
		modelName == "viduq2":
		// Q2 text/image/reference generation supports 1–10 seconds, while
		// start-end generation is limited to 1–8 seconds.
		maxDuration := 10
		if action == constant.TaskActionFirstTailGenerate {
			maxDuration = 8
		}
		if duration < 1 || duration > maxDuration {
			return fmt.Errorf("duration must be between 1 and %d seconds for %s", maxDuration, modelName)
		}
	case modelName == "viduq1" || modelName == "viduq1-classic":
		if duration != 5 {
			return fmt.Errorf("duration must be 5 seconds for %s", modelName)
		}
	case modelName == "vidu2.0":
		// Reference-to-video supports only 4 seconds. Image-to-video and
		// start-end generation support 4 or 8 seconds.
		if action == constant.TaskActionReferenceGenerate {
			if duration != 4 {
				return fmt.Errorf("duration must be 4 seconds for %s reference-to-video", modelName)
			}
		} else if duration != 4 && duration != 8 {
			return fmt.Errorf("duration must be either 4 or 8 seconds for %s", modelName)
		}
	default:
		return fmt.Errorf("unsupported Vidu model %q", modelName)
	}
	return nil
}

func viduAction(info *relaycommon.RelayInfo) string {
	if info != nil && info.TaskRelayInfo != nil && info.Action != "" {
		return info.Action
	}
	return constant.TaskActionTextGenerate
}

func viduDefaults(modelName string) (int, string) {
	duration := 5
	switch {
	case strings.EqualFold(modelName, "vidu2.0"):
		duration = 4
	}
	return duration, viduDefaultResolution(strings.ToLower(modelName), duration)
}

func viduDefaultResolution(modelName string, duration int) string {
	switch {
	case modelName == "vidu2.0" && duration == 8:
		return "720p"
	case modelName == "vidu2.0":
		return "360p"
	case strings.HasPrefix(modelName, "viduq2"), strings.HasPrefix(modelName, "viduq3"):
		return "720p"
	default:
		return "1080p"
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
	case modelName == "viduq3-pro-fast", modelName == "viduq2-pro-fast":
		allowed = []string{"720p", "1080p"}
	case modelName == "viduq3-pro", modelName == "viduq3-turbo", modelName == "viduq3",
		modelName == "viduq2-pro", modelName == "viduq2-turbo", modelName == "viduq2":
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

func viduCanonicalCredits(modelName string) (float64, bool) {
	switch strings.ToLower(modelName) {
	case "viduq3-pro-fast", "viduq3-pro":
		return 100, true
	case "viduq3-turbo":
		return 55, true
	case "viduq3-mix":
		return 120, true
	case "viduq3":
		return 60, true
	case "viduq2-pro-fast":
		return 16, true
	case "viduq2-pro":
		return 55, true
	case "viduq2-turbo":
		return 40, true
	case "viduq2":
		return 35, true
	case "viduq1", "viduq1-classic":
		return 80, true
	case "vidu2.0":
		return 20, true
	default:
		return 0, false
	}
}

func viduVideoCredits(modelName, action string, duration int, resolution string, audio, isRec bool) (float64, bool) {
	modelName = strings.ToLower(modelName)
	var credits float64
	switch {
	case modelName == "viduq3-pro-fast" ||
		modelName == "viduq3-pro" ||
		modelName == "viduq3-turbo" ||
		modelName == "viduq3-mix" ||
		modelName == "viduq3":
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
		credits = rate * float64(duration)
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
	if audio && strings.HasPrefix(modelName, "viduq2") &&
		(action == constant.TaskActionGenerate || action == constant.TaskActionReferenceGenerate) {
		credits += 15
	}
	if isRec {
		credits += 10
	}
	return credits, true
}

func parseViduCredits(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, fmt.Errorf("credits are missing")
	}
	var credits int64
	if err := common.Unmarshal(raw, &credits); err != nil {
		var text string
		if stringErr := common.Unmarshal(raw, &text); stringErr != nil {
			return 0, fmt.Errorf("credits must be an integer or integer string")
		}
		if text == "" {
			return 0, fmt.Errorf("credits must be an integer or integer string")
		}
		for _, character := range text {
			if character < '0' || character > '9' {
				return 0, fmt.Errorf("credits must be an integer or integer string")
			}
		}
		parsed, parseErr := strconv.ParseInt(text, 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("credits must be an integer or integer string: %w", parseErr)
		}
		credits = parsed
	}
	if credits <= 0 || credits > maxViduCredits {
		return 0, fmt.Errorf("credits must be between 1 and %d", maxViduCredits)
	}
	return int(credits), nil
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
