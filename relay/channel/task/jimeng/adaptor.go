package jimeng

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
)

// ============================
// Request / Response structures
// ============================

type requestPayload struct {
	ReqKey           string   `json:"req_key"`
	BinaryDataBase64 []string `json:"binary_data_base64,omitempty"`
	ImageUrls        []string `json:"image_urls,omitempty"`
	Prompt           string   `json:"prompt,omitempty"`
	Seed             *int64   `json:"seed,omitempty"`
	AspectRatio      *string  `json:"aspect_ratio,omitempty"`
	Frames           *int     `json:"frames,omitempty"`
	TemplateID       *string  `json:"template_id,omitempty"`
	CameraStrength   *string  `json:"camera_strength,omitempty"`
}

type responsePayload struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RequestId string `json:"request_id"`
	Data      struct {
		TaskID string `json:"task_id"`
	} `json:"data"`
}

type responseTask struct {
	Code int `json:"code"`
	Data struct {
		BinaryDataBase64 []interface{} `json:"binary_data_base64"`
		ImageUrls        interface{}   `json:"image_urls"`
		RespData         string        `json:"resp_data"`
		Status           string        `json:"status"`
		VideoUrl         string        `json:"video_url"`
	} `json:"data"`
	Message     string `json:"message"`
	RequestId   string `json:"request_id"`
	Status      int    `json:"status"`
	TimeElapsed string `json:"time_elapsed"`
}

const (
	// 即梦限制单个文件最大4.7MB https://www.volcengine.com/docs/85621/1747301
	MaxFileSize int64 = 4*1024*1024 + 700*1024

	jimengReqKeyContextKey    = "jimeng_actual_req_key"
	jimengTaskReferencePrefix = "jimeng-task-v1:"
	maxJimengPromptRunes      = 800

	jimengVideo30Pro              = "jimeng_ti2v_v30_pro"
	jimengVideo30T2V720           = "jimeng_t2v_v30"
	jimengVideo30I2VFirst720      = "jimeng_i2v_first_v30"
	jimengVideo30I2VFirstTail720  = "jimeng_i2v_first_tail_v30"
	jimengVideo30I2VRecamera720   = "jimeng_i2v_recamera_v30"
	jimengVideo30T2V1080          = "jimeng_t2v_v30_1080p"
	jimengVideo30I2VFirst1080     = "jimeng_i2v_first_v30_1080"
	jimengVideo30I2VFirstTail1080 = "jimeng_i2v_first_tail_v30_1080"

	jimengVideo30Alias     = "jimeng_v30"
	jimengVideo30ProAlias  = "jimeng_v30_pro"
	jimengVideo301080Alias = "jimeng_v30_1080p"

	legacyJimengT2V = "jimeng_vgfm_t2v_l20"
	legacyJimengI2V = "jimeng_vgfm_i2v_l20"
)

var currentJimengVideoModels = []string{
	jimengVideo30Pro,
	jimengVideo30T2V720,
	jimengVideo30I2VFirst720,
	jimengVideo30I2VFirstTail720,
	jimengVideo30I2VRecamera720,
	jimengVideo30T2V1080,
	jimengVideo30I2VFirst1080,
	jimengVideo30I2VFirstTail1080,
}

type jimengTaskReference struct {
	ReqKey string `json:"req_key"`
	TaskID string `json:"task_id"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	accessKey   string
	secretKey   string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl

	// apiKey format: "access_key|secret_key"
	keyParts := strings.Split(info.ApiKey, "|")
	if len(keyParts) == 2 {
		a.accessKey = strings.TrimSpace(keyParts[0])
		a.secretKey = strings.TrimSpace(keyParts[1])
	}
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

func (a *TaskAdaptor) ValidateFinalRequest(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if _, err := a.convertToRequestPayload(&req, info); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	return nil
}

// EstimateBilling normalizes the provider-native frame count to the default
// five-second payload. Current Jimeng Video 3.0 models are billed per second
// and accept 121 (5s) or 241 (10s) frames.
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	payload, err := a.convertToRequestPayload(&req, info)
	if err != nil || payload.Frames == nil || *payload.Frames != 241 {
		return nil
	}
	return map[string]float64{"duration": 2}
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	baseURL := strings.TrimRight(a.baseURL, "/")
	if isNewAPIRelay(info.ApiKey) {
		return fmt.Sprintf("%s/jimeng/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31", baseURL), nil
	}
	return fmt.Sprintf("%s/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31", baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if isNewAPIRelay(info.ApiKey) {
		req.Header.Set("Authorization", "Bearer "+info.ApiKey)
	} else {
		return a.signRequest(req, a.accessKey, a.secretKey)
	}
	return nil
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
	// 支持openai sdk的图片上传方式
	if mf, err := c.MultipartForm(); err == nil {
		if files, exists := mf.File["input_reference"]; exists && len(files) > 0 {
			if len(files) == 1 {
				info.Action = constant.TaskActionGenerate
			} else if len(files) > 1 {
				info.Action = constant.TaskActionFirstTailGenerate
			}

			// 将上传的文件转换为base64格式
			var images []string

			for _, fileHeader := range files {
				// 检查文件大小
				if fileHeader.Size > MaxFileSize {
					return nil, fmt.Errorf("file %s exceeds Jimeng's 4.7 MB limit", fileHeader.Filename)
				}

				file, err := fileHeader.Open()
				if err != nil {
					return nil, fmt.Errorf("open file %s: %w", fileHeader.Filename, err)
				}
				fileBytes, err := io.ReadAll(file)
				file.Close()
				if err != nil {
					return nil, fmt.Errorf("read file %s: %w", fileHeader.Filename, err)
				}
				// 将文件内容转换为base64
				base64Str := base64.StdEncoding.EncodeToString(fileBytes)
				images = append(images, base64Str)
			}
			req.Images = images
		}
	}

	body, err := a.convertToRequestPayload(&req, info)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	c.Set(jimengReqKeyContextKey, body.ReqKey)
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
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}

	var upstreamTaskID string
	if isNewAPIRelay(info.ApiKey) {
		var nestedResponse dto.OpenAIVideo
		if err := common.Unmarshal(responseBody, &nestedResponse); err != nil {
			taskErr = service.TaskErrorWrapper(
				errors.Wrapf(err, "unmarshal %d-byte nested response body", len(responseBody)),
				"unmarshal_response_body_failed",
				http.StatusInternalServerError,
			)
			return
		}
		upstreamTaskID = nestedResponse.TaskID
		if upstreamTaskID == "" {
			upstreamTaskID = nestedResponse.ID
		}
		if upstreamTaskID == "" {
			taskErr = service.TaskErrorWrapper(
				errors.New("nested Jimeng relay returned an empty task ID"),
				"invalid_response",
				http.StatusBadGateway,
			)
			return
		}
	} else {
		var jResp responsePayload
		if err := common.Unmarshal(responseBody, &jResp); err != nil {
			taskErr = service.TaskErrorWrapper(
				errors.Wrapf(err, "unmarshal %d-byte response body", len(responseBody)),
				"unmarshal_response_body_failed",
				http.StatusInternalServerError,
			)
			return
		}
		if jResp.Code != 10000 {
			taskErr = service.TaskErrorWrapper(fmt.Errorf("%s", jResp.Message), fmt.Sprintf("%d", jResp.Code), http.StatusInternalServerError)
			return
		}
		upstreamTaskID = jResp.Data.TaskID
	}

	reqKey := c.GetString(jimengReqKeyContextKey)
	if reqKey == "" {
		reqKey = info.UpstreamModelName
	}
	taskReference, err := encodeJimengTaskReference(reqKey, upstreamTaskID)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "invalid_response", http.StatusBadGateway)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)
	return taskReference, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(ctx context.Context, baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	storedTaskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(storedTaskID) == "" {
		return nil, fmt.Errorf("invalid task_id")
	}
	reqKey, taskID, err := decodeJimengTaskReference(storedTaskID)
	if err != nil {
		return nil, err
	}

	method := http.MethodPost
	uri := fmt.Sprintf("%s/?Action=CVSync2AsyncGetResult&Version=2022-08-31", strings.TrimRight(baseUrl, "/"))
	var requestBody io.Reader
	if isNewAPIRelay(key) {
		method = http.MethodGet
		uri = fmt.Sprintf("%s/v1/videos/%s", strings.TrimRight(baseUrl, "/"), url.PathEscape(taskID))
	} else {
		payloadBytes, err := common.Marshal(map[string]string{
			"req_key": reqKey,
			"task_id": taskID,
		})
		if err != nil {
			return nil, errors.Wrap(err, "marshal fetch task payload failed")
		}
		requestBody = bytes.NewReader(payloadBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, uri, requestBody)
	if err != nil {
		return nil, service.SanitizeNetworkError(err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	if isNewAPIRelay(key) {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		keyParts := strings.Split(key, "|")
		if len(keyParts) != 2 {
			return nil, fmt.Errorf("invalid api key format for jimeng: expected 'ak|sk'")
		}
		accessKey := strings.TrimSpace(keyParts[0])
		secretKey := strings.TrimSpace(keyParts[1])

		if err := a.signRequest(req, accessKey, secretKey); err != nil {
			return nil, errors.Wrap(err, "sign request failed")
		}
	}
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return service.DoUpstreamRequest(client, req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return append([]string(nil), currentJimengVideoModels...)
}

func (a *TaskAdaptor) GetChannelName() string {
	return "jimeng"
}

func (a *TaskAdaptor) signRequest(req *http.Request, accessKey, secretKey string) error {
	if strings.TrimSpace(accessKey) == "" || strings.TrimSpace(secretKey) == "" {
		return errors.New("invalid api key format for jimeng: access key and secret key are required")
	}

	var bodyBytes []byte
	var err error

	if req.Body != nil {
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return errors.Wrap(err, "read request body failed")
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Rewind
	} else {
		bodyBytes = []byte{}
	}

	payloadHash := sha256.Sum256(bodyBytes)
	hexPayloadHash := hex.EncodeToString(payloadHash[:])

	t := time.Now().UTC()
	xDate := t.Format("20060102T150405Z")
	shortDate := t.Format("20060102")

	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", hexPayloadHash)

	// Sort and encode query parameters to create canonical query string
	queryParams := req.URL.Query()
	sortedKeys := make([]string, 0, len(queryParams))
	for k := range queryParams {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	var queryParts []string
	for _, k := range sortedKeys {
		values := queryParams[k]
		sort.Strings(values)
		for _, v := range values {
			queryParts = append(queryParts, fmt.Sprintf("%s=%s", url.QueryEscape(k), url.QueryEscape(v)))
		}
	}
	canonicalQueryString := strings.Join(queryParts, "&")

	headersToSign := map[string]string{
		"host":             req.URL.Host,
		"x-date":           xDate,
		"x-content-sha256": hexPayloadHash,
	}
	if req.Header.Get("Content-Type") != "" {
		headersToSign["content-type"] = req.Header.Get("Content-Type")
	}

	var signedHeaderKeys []string
	for k := range headersToSign {
		signedHeaderKeys = append(signedHeaderKeys, k)
	}
	sort.Strings(signedHeaderKeys)

	var canonicalHeaders strings.Builder
	for _, k := range signedHeaderKeys {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(headersToSign[k]))
		canonicalHeaders.WriteString("\n")
	}
	signedHeaders := strings.Join(signedHeaderKeys, ";")

	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method,
		req.URL.Path,
		canonicalQueryString,
		canonicalHeaders.String(),
		signedHeaders,
		hexPayloadHash,
	)

	hashedCanonicalRequest := sha256.Sum256([]byte(canonicalRequest))
	hexHashedCanonicalRequest := hex.EncodeToString(hashedCanonicalRequest[:])

	region := "cn-north-1"
	serviceName := "cv"
	credentialScope := fmt.Sprintf("%s/%s/%s/request", shortDate, region, serviceName)
	stringToSign := fmt.Sprintf("HMAC-SHA256\n%s\n%s\n%s",
		xDate,
		credentialScope,
		hexHashedCanonicalRequest,
	)

	kDate := hmacSHA256([]byte(secretKey), []byte(shortDate))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(serviceName))
	kSigning := hmacSHA256(kService, []byte("request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	authorization := fmt.Sprintf("HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey,
		credentialScope,
		signedHeaders,
		signature,
	)
	req.Header.Set("Authorization", authorization)
	return nil
}

func hmacSHA256(key []byte, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*requestPayload, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	if info == nil || info.ChannelMeta == nil {
		return nil, fmt.Errorf("relay info is required")
	}

	frames := 121
	switch req.Duration {
	case 0, 5:
	case 10:
		frames = 241
	default:
		return nil, fmt.Errorf("duration must be either 5 or 10 seconds")
	}

	r := requestPayload{
		ReqKey: info.UpstreamModelName,
		Prompt: req.Prompt,
		Frames: &frames,
	}
	authoritativeReqKey := r.ReqKey

	// Handle one-of image_urls or binary_data_base64
	if req.HasImage() {
		firstImage := strings.ToLower(req.Images[0])
		firstIsURL := strings.HasPrefix(firstImage, "http://") || strings.HasPrefix(firstImage, "https://")
		for _, image := range req.Images[1:] {
			lowerImage := strings.ToLower(image)
			isURL := strings.HasPrefix(lowerImage, "http://") || strings.HasPrefix(lowerImage, "https://")
			if isURL != firstIsURL {
				return nil, fmt.Errorf("images must use either URLs or base64 data, not both")
			}
		}
		if firstIsURL {
			r.ImageUrls = append([]string(nil), req.Images...)
		} else {
			r.BinaryDataBase64 = append([]string(nil), req.Images...)
		}
	}
	if err := taskcommon.UnmarshalMetadata(req.Metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}
	// req_key selects both the upstream model and its configured price. Native
	// metadata must not be able to switch it after routing and pre-consume.
	r.ReqKey = authoritativeReqKey
	r.Prompt = req.Prompt
	if r.Frames == nil {
		r.Frames = &frames
	}
	if *r.Frames != 121 && *r.Frames != 241 {
		return nil, fmt.Errorf("frames must be either 121 (5 seconds) or 241 (10 seconds)")
	}
	if len(r.BinaryDataBase64) > 0 && len(r.ImageUrls) > 0 {
		return nil, fmt.Errorf("binary_data_base64 and image_urls are mutually exclusive")
	}

	imageCount := len(r.ImageUrls)
	if len(r.BinaryDataBase64) > imageCount {
		imageCount = len(r.BinaryDataBase64)
	}
	switch r.ReqKey {
	case jimengVideo30Alias:
		switch imageCount {
		case 0:
			r.ReqKey = jimengVideo30T2V720
		case 1:
			r.ReqKey = jimengVideo30I2VFirst720
		case 2:
			r.ReqKey = jimengVideo30I2VFirstTail720
		default:
			return nil, fmt.Errorf("%s supports at most two input images", jimengVideo30Alias)
		}
	case jimengVideo301080Alias:
		switch imageCount {
		case 0:
			r.ReqKey = jimengVideo30T2V1080
		case 1:
			r.ReqKey = jimengVideo30I2VFirst1080
		case 2:
			r.ReqKey = jimengVideo30I2VFirstTail1080
		default:
			return nil, fmt.Errorf("%s supports at most two input images", jimengVideo301080Alias)
		}
	case jimengVideo30ProAlias:
		r.ReqKey = jimengVideo30Pro
	}

	switch r.ReqKey {
	case jimengVideo30T2V720, jimengVideo30T2V1080, legacyJimengT2V:
		if imageCount != 0 {
			return nil, fmt.Errorf("%s does not accept input images", r.ReqKey)
		}
	case jimengVideo30I2VFirst720, jimengVideo30I2VFirst1080, jimengVideo30I2VRecamera720, legacyJimengI2V:
		if imageCount != 1 {
			return nil, fmt.Errorf("%s requires exactly one input image", r.ReqKey)
		}
	case jimengVideo30I2VFirstTail720, jimengVideo30I2VFirstTail1080:
		if imageCount != 2 {
			return nil, fmt.Errorf("%s requires exactly two input images", r.ReqKey)
		}
	case jimengVideo30Pro:
		if imageCount > 1 {
			return nil, fmt.Errorf("%s supports at most one input image", r.ReqKey)
		}
	}

	isCurrentModel := false
	for _, modelName := range currentJimengVideoModels {
		if r.ReqKey == modelName {
			isCurrentModel = true
			break
		}
	}
	if isCurrentModel {
		if utf8.RuneCountInString(r.Prompt) > maxJimengPromptRunes {
			return nil, fmt.Errorf("prompt must not exceed %d characters", maxJimengPromptRunes)
		}
		if r.Seed != nil && (*r.Seed < -1 || *r.Seed > 1<<31-1) {
			return nil, fmt.Errorf("seed must be between -1 and %d", int64(1<<31-1))
		}
		if len(r.BinaryDataBase64) > 0 {
			normalizedImages, err := validateJimengBinaryImages(r.BinaryDataBase64)
			if err != nil {
				return nil, err
			}
			r.BinaryDataBase64 = normalizedImages
		}
		if r.AspectRatio != nil {
			switch *r.AspectRatio {
			case "16:9", "4:3", "1:1", "3:4", "9:16", "21:9":
			default:
				return nil, fmt.Errorf("aspect_ratio is not supported: %s", *r.AspectRatio)
			}
			switch r.ReqKey {
			case jimengVideo30I2VFirst720, jimengVideo30I2VFirstTail720,
				jimengVideo30I2VRecamera720, jimengVideo30I2VFirst1080,
				jimengVideo30I2VFirstTail1080:
				return nil, fmt.Errorf("aspect_ratio is only supported for text-to-video and %s", jimengVideo30Pro)
			}
		}
		if r.ReqKey == jimengVideo30I2VRecamera720 {
			if r.TemplateID == nil || strings.TrimSpace(*r.TemplateID) == "" {
				return nil, fmt.Errorf("template_id is required for %s", r.ReqKey)
			}
			switch *r.TemplateID {
			case "hitchcock_dolly_in", "hitchcock_dolly_out", "robo_arm",
				"dynamic_orbit", "central_orbit", "crane_push", "quick_pull_back",
				"counterclockwise_swivel", "clockwise_swivel", "handheld", "rapid_push_pull":
			default:
				return nil, fmt.Errorf("template_id is not supported: %s", *r.TemplateID)
			}
			if r.CameraStrength == nil {
				return nil, fmt.Errorf("camera_strength is required for %s", r.ReqKey)
			}
			switch *r.CameraStrength {
			case "weak", "medium", "strong":
			default:
				return nil, fmt.Errorf("camera_strength is not supported: %s", *r.CameraStrength)
			}
		} else if r.TemplateID != nil || r.CameraStrength != nil {
			return nil, fmt.Errorf("template_id and camera_strength are only supported for %s", jimengVideo30I2VRecamera720)
		}
	}

	if r.ReqKey == legacyJimengT2V || r.ReqKey == legacyJimengI2V {
		if req.Duration == 10 {
			return nil, fmt.Errorf("%s does not document 10-second generation", r.ReqKey)
		}
		if _, ok := req.Metadata["frames"]; ok {
			return nil, fmt.Errorf("frames is not supported for %s", r.ReqKey)
		}
		r.Frames = nil
		if utf8.RuneCountInString(r.Prompt) > 150 {
			return nil, fmt.Errorf("prompt must not exceed 150 characters for %s", r.ReqKey)
		}
		if r.AspectRatio != nil {
			switch *r.AspectRatio {
			case "16:9", "4:3", "1:1", "3:4", "9:16", "21:9":
			case "9:21":
				if r.ReqKey != legacyJimengI2V {
					return nil, fmt.Errorf("aspect_ratio is not supported: %s", *r.AspectRatio)
				}
			default:
				return nil, fmt.Errorf("aspect_ratio is not supported: %s", *r.AspectRatio)
			}
		}
		if r.ReqKey == legacyJimengI2V && r.AspectRatio == nil {
			return nil, fmt.Errorf("aspect_ratio is required for %s", r.ReqKey)
		}
	}

	return &r, nil
}

func validateJimengBinaryImages(encodedImages []string) ([]string, error) {
	normalizedImages := make([]string, len(encodedImages))
	type dimensions struct {
		width  int
		height int
	}
	imageDimensions := make([]dimensions, len(encodedImages))
	maxEncodedSize := base64.StdEncoding.EncodedLen(int(MaxFileSize))

	for index, encodedImage := range encodedImages {
		if strings.HasPrefix(strings.ToLower(encodedImage), "data:") {
			commaIndex := strings.IndexByte(encodedImage, ',')
			if commaIndex < 0 || !strings.HasSuffix(strings.ToLower(encodedImage[:commaIndex]), ";base64") {
				return nil, fmt.Errorf("binary_data_base64[%d] must be a base64-encoded JPEG or PNG image", index)
			}
			encodedImage = encodedImage[commaIndex+1:]
		}
		if len(encodedImage) > maxEncodedSize {
			return nil, fmt.Errorf("binary_data_base64[%d] exceeds Jimeng's 4.7 MB limit", index)
		}
		decodedImage, err := base64.StdEncoding.DecodeString(encodedImage)
		if err != nil {
			return nil, fmt.Errorf("binary_data_base64[%d] is invalid base64: %w", index, err)
		}
		if int64(len(decodedImage)) > MaxFileSize {
			return nil, fmt.Errorf("binary_data_base64[%d] exceeds Jimeng's 4.7 MB limit", index)
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(decodedImage))
		if err != nil || (format != "jpeg" && format != "png") {
			return nil, fmt.Errorf("binary_data_base64[%d] must contain a JPEG or PNG image", index)
		}
		if config.Width > 4096 || config.Height > 4096 {
			return nil, fmt.Errorf("binary_data_base64[%d] dimensions must not exceed 4096x4096", index)
		}
		shortEdge := config.Width
		longEdge := config.Height
		if shortEdge > longEdge {
			shortEdge, longEdge = longEdge, shortEdge
		}
		if shortEdge < 320 {
			return nil, fmt.Errorf("binary_data_base64[%d] shortest edge must be at least 320 pixels", index)
		}
		if longEdge > shortEdge*3 {
			return nil, fmt.Errorf("binary_data_base64[%d] aspect ratio must not exceed 3:1", index)
		}
		normalizedImages[index] = encodedImage
		imageDimensions[index] = dimensions{width: config.Width, height: config.Height}
	}

	if len(imageDimensions) == 2 {
		first := imageDimensions[0]
		last := imageDimensions[1]
		if first.width*last.height != last.width*first.height {
			return nil, fmt.Errorf("first and last frame images must use the same aspect ratio")
		}
	}
	return normalizedImages, nil
}

func encodeJimengTaskReference(reqKey, taskID string) (string, error) {
	if strings.TrimSpace(reqKey) == "" {
		return "", fmt.Errorf("Jimeng response is missing the submitted req_key")
	}
	if strings.TrimSpace(taskID) == "" {
		return "", fmt.Errorf("Jimeng response is missing task_id")
	}
	data, err := common.Marshal(jimengTaskReference{
		ReqKey: reqKey,
		TaskID: taskID,
	})
	if err != nil {
		return "", errors.Wrap(err, "marshal Jimeng task reference failed")
	}
	return jimengTaskReferencePrefix + string(data), nil
}

func decodeJimengTaskReference(storedTaskID string) (reqKey, taskID string, err error) {
	if !strings.HasPrefix(storedTaskID, jimengTaskReferencePrefix) {
		// Tasks created before req_key persistence always polled with this
		// legacy text-to-video key. Keep that behavior for existing rows.
		return legacyJimengT2V, storedTaskID, nil
	}
	var reference jimengTaskReference
	if err := common.UnmarshalJsonStr(strings.TrimPrefix(storedTaskID, jimengTaskReferencePrefix), &reference); err != nil {
		return "", "", errors.Wrap(err, "unmarshal Jimeng task reference failed")
	}
	if strings.TrimSpace(reference.ReqKey) == "" || strings.TrimSpace(reference.TaskID) == "" {
		return "", "", fmt.Errorf("invalid Jimeng task reference")
	}
	return reference.ReqKey, reference.TaskID, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var nestedResponse dto.OpenAIVideo
	if err := common.Unmarshal(respBody, &nestedResponse); err == nil &&
		(nestedResponse.Object == "video" || nestedResponse.ID != "") {
		taskResult := relaycommon.TaskInfo{}
		switch nestedResponse.Status {
		case dto.VideoStatusQueued:
			taskResult.Status = model.TaskStatusQueued
			taskResult.Progress = "10%"
		case dto.VideoStatusInProgress:
			taskResult.Status = model.TaskStatusInProgress
			if nestedResponse.Progress > 0 {
				taskResult.Progress = fmt.Sprintf("%d%%", nestedResponse.Progress)
			}
		case dto.VideoStatusCompleted:
			taskResult.Status = model.TaskStatusSuccess
			taskResult.Progress = "100%"
		case dto.VideoStatusFailed:
			taskResult.Status = model.TaskStatusFailure
			taskResult.Progress = "100%"
			if nestedResponse.Error != nil {
				taskResult.Reason = nestedResponse.Error.Message
			}
		default:
			return nil, fmt.Errorf("unknown nested Jimeng task status: %s", nestedResponse.Status)
		}
		if nestedResponse.Metadata != nil {
			taskResult.Url, _ = nestedResponse.Metadata["url"].(string)
		}
		return &taskResult, nil
	}

	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}
	taskResult := relaycommon.TaskInfo{}
	if resTask.Code == 10000 {
		taskResult.Code = 0
	} else {
		taskResult.Code = resTask.Code // todo uni code
		taskResult.Reason = resTask.Message
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		return &taskResult, nil
	}
	switch resTask.Data.Status {
	case "in_queue":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "generating":
		taskResult.Status = model.TaskStatusInProgress
	case "done":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
	case "not_found", "expired":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.Data.Status
	default:
		if resTask.Code == 10000 {
			return nil, fmt.Errorf("unknown Jimeng task status: %s", resTask.Data.Status)
		}
	}
	taskResult.Url = resTask.Data.VideoUrl
	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var nestedResponse dto.OpenAIVideo
	if err := common.Unmarshal(originTask.Data, &nestedResponse); err == nil &&
		(nestedResponse.Object == "video" || nestedResponse.ID != "") {
		nestedResponse.ID = originTask.TaskID
		nestedResponse.TaskID = originTask.TaskID
		nestedResponse.Model = originTask.Properties.OriginModelName
		nestedResponse.Status = originTask.Status.ToVideoStatus()
		nestedResponse.SetProgressStr(originTask.Progress)
		nestedResponse.CreatedAt = originTask.CreatedAt
		nestedResponse.CompletedAt = originTask.UpdatedAt
		return common.Marshal(nestedResponse)
	}

	var jimengResp responseTask
	if err := common.Unmarshal(originTask.Data, &jimengResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal jimeng task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", jimengResp.Data.VideoUrl)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt

	if jimengResp.Code != 10000 {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: jimengResp.Message,
			Code:    fmt.Sprintf("%d", jimengResp.Code),
		}
	}

	return common.Marshal(openAIVideo)
}

func isNewAPIRelay(apiKey string) bool {
	return strings.HasPrefix(apiKey, "sk-")
}
