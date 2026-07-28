package replicate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type Adaptor struct {
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info == nil {
		return "", errors.New("replicate adaptor: relay info is nil")
	}
	if info.ChannelBaseUrl == "" {
		info.ChannelBaseUrl = constant.ChannelBaseURLs[constant.ChannelTypeReplicate]
	}
	requestPath := info.RequestURLPath
	if requestPath == "" {
		return info.ChannelBaseUrl, nil
	}
	return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, requestPath, info.ChannelType), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	if info == nil {
		return errors.New("replicate adaptor: relay info is nil")
	}
	if info.ApiKey == "" {
		return errors.New("replicate adaptor: api key is required")
	}
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", "Bearer "+info.ApiKey)
	req.Set("Prefer", "wait")
	if req.Get("Content-Type") == "" {
		req.Set("Content-Type", "application/json")
	}
	if req.Get("Accept") == "" {
		req.Set("Accept", "application/json")
	}
	return nil
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if info == nil {
		return nil, errors.New("replicate adaptor: relay info is nil")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		if v := c.PostForm("prompt"); strings.TrimSpace(v) != "" {
			request.Prompt = v
		}
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, errors.New("replicate adaptor: prompt is required")
	}

	modelName := strings.TrimSpace(info.UpstreamModelName)
	if modelName == "" {
		modelName = strings.TrimSpace(request.Model)
	}
	if modelName == "" {
		modelName = ModelFlux11Pro
	}
	info.UpstreamModelName = modelName

	info.RequestURLPath = fmt.Sprintf("/v1/models/%s/predictions", modelName)

	inputPayload := make(map[string]any)
	inputPayload["prompt"] = request.Prompt

	if size := strings.TrimSpace(request.Size); size != "" {
		if aspect, width, height, ok := mapOpenAISizeToFlux(size); ok {
			if aspect != "" {
				if aspect == "custom" {
					inputPayload["aspect_ratio"] = "custom"
					if width > 0 {
						inputPayload["width"] = width
					}
					if height > 0 {
						inputPayload["height"] = height
					}
				} else {
					inputPayload["aspect_ratio"] = aspect
				}
			}
		}
	}

	if len(request.OutputFormat) > 0 {
		var outputFormat string
		if err := common.Unmarshal(request.OutputFormat, &outputFormat); err == nil && strings.TrimSpace(outputFormat) != "" {
			inputPayload["output_format"] = outputFormat
		}
	}

	if imageN := lo.FromPtrOr(request.N, uint(0)); imageN > 0 {
		inputPayload["num_outputs"] = int(imageN)
	}

	if strings.EqualFold(request.Quality, "hd") || strings.EqualFold(request.Quality, "high") {
		inputPayload["prompt_upsampling"] = true
	}

	if info.RelayMode == relayconstant.RelayModeImagesEdits {
		imageURL, err := uploadFileFromForm(c, info, "image", "image[]", "image_prompt")
		if err != nil {
			return nil, err
		}
		if imageURL == "" {
			return nil, errors.New("replicate adaptor: image file is required for edits")
		}
		inputPayload["image_prompt"] = imageURL
	}

	if len(request.ExtraFields) > 0 {
		var extra map[string]any
		if err := common.Unmarshal(request.ExtraFields, &extra); err != nil {
			return nil, fmt.Errorf("replicate adaptor: failed to decode extra_fields: %w", err)
		}
		for key, val := range extra {
			inputPayload[key] = val
		}
	}

	for key, raw := range request.Extra {
		if strings.EqualFold(key, "input") {
			var extraInput map[string]any
			if err := common.Unmarshal(raw, &extraInput); err != nil {
				return nil, fmt.Errorf("replicate adaptor: failed to decode extra input: %w", err)
			}
			for k, v := range extraInput {
				inputPayload[k] = v
			}
			continue
		}
		if raw == nil {
			continue
		}
		var val any
		if err := common.Unmarshal(raw, &val); err != nil {
			return nil, fmt.Errorf("replicate adaptor: failed to decode extra field %s: %w", key, err)
		}
		inputPayload[key] = val
	}

	// The generic image validator reconciles every supported num_outputs
	// passthrough path into N before pricing. Re-apply that validated value last
	// so map iteration order cannot let Extra or Extra["input"] silently change
	// the number of billed outputs.
	if request.N != nil {
		inputPayload["num_outputs"] = int(*request.N)
	}

	return map[string]any{
		"input": inputPayload,
	}, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	if resp == nil {
		return nil, types.NewError(errors.New("replicate adaptor: empty response"), types.ErrorCodeBadResponse)
	}
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeReadResponseBodyFailed)
	}

	var prediction PredictionResponse
	if err := common.Unmarshal(responseBody, &prediction); err != nil {
		return nil, types.NewError(fmt.Errorf("replicate adaptor: failed to decode response: %w", err), types.ErrorCodeBadResponseBody)
	}

	urls := predictionOutputURLs(prediction.Output)
	if (strings.EqualFold(prediction.Status, "starting") || strings.EqualFold(prediction.Status, "processing")) && len(urls) == 0 {
		prediction, err = pollPrediction(c, info, resp, prediction)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
		}
		urls = predictionOutputURLs(prediction.Output)
	}

	if errMsg := predictionErrorMessage(prediction.Error); errMsg != "" {
		return nil, types.NewError(errors.New(errMsg), types.ErrorCodeBadResponse)
	}

	status := strings.ToLower(strings.TrimSpace(prediction.Status))
	if status != "" && status != "succeeded" && !((status == "starting" || status == "processing") && len(urls) > 0) {
		return nil, types.NewError(fmt.Errorf("replicate adaptor: prediction status %q", prediction.Status), types.ErrorCodeBadResponse)
	}

	if len(urls) == 0 {
		return nil, types.NewError(errors.New("replicate adaptor: empty prediction output"), types.ErrorCodeBadResponseBody)
	}
	if len(urls) > dto.MaxImageN {
		return nil, types.NewError(fmt.Errorf("replicate adaptor: prediction returned %d outputs, maximum is %d", len(urls), dto.MaxImageN), types.ErrorCodeBadResponseBody)
	}
	if info != nil {
		info.PriceData.AddOtherRatio("n", float64(len(urls)))
	}

	var imageReq *dto.ImageRequest
	if info != nil {
		if req, ok := info.Request.(*dto.ImageRequest); ok {
			imageReq = req
		}
	}

	wantsBase64 := imageReq != nil && strings.EqualFold(imageReq.ResponseFormat, "b64_json")

	imageResponse := dto.ImageResponse{
		Created: common.GetTimestamp(),
		Data:    make([]dto.ImageData, 0),
	}

	if wantsBase64 {
		converted, convErr := downloadImagesToBase64(urls)
		if convErr != nil {
			return nil, types.NewError(convErr, types.ErrorCodeBadResponse)
		}
		for _, content := range converted {
			if content == "" {
				continue
			}
			imageResponse.Data = append(imageResponse.Data, dto.ImageData{B64Json: content})
		}
	} else {
		for _, url := range urls {
			if url == "" {
				continue
			}
			imageResponse.Data = append(imageResponse.Data, dto.ImageData{Url: url})
		}
	}

	if len(imageResponse.Data) == 0 {
		return nil, types.NewError(errors.New("replicate adaptor: no usable image data"), types.ErrorCodeBadResponse)
	}

	responseBytes, err := common.Marshal(imageResponse)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("replicate adaptor: encode response failed: %w", err), types.ErrorCodeBadResponseBody)
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(responseBytes)

	usage := &dto.Usage{}
	return usage, nil
}

func predictionOutputURLs(output any) []string {
	urls := make([]string, 0)
	appendOutput := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			urls = append(urls, value)
		}
	}
	switch value := output.(type) {
	case string:
		appendOutput(value)
	case []any:
		for _, item := range value {
			if str, ok := item.(string); ok {
				appendOutput(str)
			}
		}
	case fmt.Stringer:
		appendOutput(value.String())
	}
	return urls
}

// pollPrediction follows Replicate's documented synchronous-timeout flow:
// Prefer: wait may return a still-running prediction, which must be fetched
// through the prediction GET endpoint until it reaches a terminal state.
func pollPrediction(c *gin.Context, info *relaycommon.RelayInfo, createResp *http.Response, prediction PredictionResponse) (PredictionResponse, error) {
	baseURL := constant.ChannelBaseURLs[constant.ChannelTypeReplicate]
	if info != nil && strings.TrimSpace(info.ChannelBaseUrl) != "" {
		baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return prediction, fmt.Errorf("replicate adaptor: invalid channel base URL")
	}

	pollTarget := strings.TrimSpace(prediction.Urls.Get)
	if pollTarget == "" && createResp != nil {
		pollTarget = strings.TrimSpace(createResp.Header.Get("Location"))
	}
	if pollTarget == "" && prediction.ID != "" {
		pollTarget = "/v1/predictions/" + url.PathEscape(prediction.ID)
	}
	if pollTarget == "" {
		return prediction, errors.New("replicate adaptor: incomplete prediction response is missing a polling URL and id")
	}

	ref, err := url.Parse(pollTarget)
	if err != nil {
		return prediction, fmt.Errorf("replicate adaptor: invalid prediction polling URL: %w", err)
	}
	pollURL := base.ResolveReference(ref)
	if !strings.EqualFold(pollURL.Scheme, base.Scheme) || !strings.EqualFold(pollURL.Host, base.Host) {
		return prediction, errors.New("replicate adaptor: prediction polling URL must use the configured channel origin")
	}
	// Normalize escaped path segments while retaining an optional proxy prefix
	// from a relative Location header.
	pollURL.Path = path.Clean(pollURL.Path)

	parentCtx := c.Request.Context()
	if info != nil {
		parentCtx = info.GetRelayContext(parentCtx)
	}
	pollCtx := parentCtx
	proxyURL := ""
	var headerOverride map[string]string
	if info != nil {
		proxyURL = info.ChannelSetting.Proxy
		headerOverride, err = channel.ResolveHeaderOverride(info, c)
		if err != nil {
			return prediction, fmt.Errorf("replicate adaptor: resolve prediction poll headers failed: %w", err)
		}
	}
	httpClient, err := service.NewProxyHttpClient(proxyURL)
	if err != nil {
		return prediction, fmt.Errorf("replicate adaptor: create prediction poll client failed: %w", err)
	}

	for {
		req, err := http.NewRequestWithContext(pollCtx, http.MethodGet, pollURL.String(), nil)
		if err != nil {
			return prediction, fmt.Errorf("replicate adaptor: create prediction poll request failed: %w", service.SanitizeNetworkError(err))
		}
		if info != nil && info.ApiKey != "" {
			req.Header.Set("Authorization", "Bearer "+info.ApiKey)
		}
		req.Header.Set("Accept", "application/json")
		for key, value := range headerOverride {
			req.Header.Set(key, value)
		}

		pollResp, err := service.DoUpstreamRequest(httpClient, req)
		if err != nil {
			return prediction, fmt.Errorf("replicate adaptor: prediction poll failed: %w", err)
		}
		maxMB := constant.MaxUpstreamResponseBodyMB
		if maxMB <= 0 {
			maxMB = 128
		}
		maxBytes := common.BytesFromMegabytes(maxMB)
		pollBody, readErr := io.ReadAll(io.LimitReader(pollResp.Body, common.ReadLimitWithOverrunByte(maxBytes)))
		_ = pollResp.Body.Close()
		if readErr != nil {
			return prediction, fmt.Errorf("replicate adaptor: read prediction poll response failed: %w", readErr)
		}
		if int64(len(pollBody)) > maxBytes {
			return prediction, fmt.Errorf("replicate adaptor: prediction poll response exceeds %d MB", maxMB)
		}
		if pollResp.StatusCode < http.StatusOK || pollResp.StatusCode >= http.StatusMultipleChoices {
			return prediction, fmt.Errorf(
				"replicate adaptor: prediction poll returned status %d: %s",
				pollResp.StatusCode,
				common.LocalLogPreview(strings.TrimSpace(string(pollBody))),
			)
		}
		if err := common.Unmarshal(pollBody, &prediction); err != nil {
			return prediction, fmt.Errorf("replicate adaptor: decode prediction poll response failed: %w", err)
		}

		switch strings.ToLower(strings.TrimSpace(prediction.Status)) {
		case "succeeded":
			return prediction, nil
		case "failed", "canceled":
			message := predictionErrorMessage(prediction.Error)
			if message == "" {
				message = prediction.Status
			}
			return prediction, fmt.Errorf("replicate adaptor: prediction %s: %s", prediction.Status, message)
		case "starting", "processing":
			timer := time.NewTimer(time.Second)
			select {
			case <-timer.C:
			case <-pollCtx.Done():
				timer.Stop()
				return prediction, fmt.Errorf("replicate adaptor: prediction polling stopped: %w", pollCtx.Err())
			}
		default:
			return prediction, fmt.Errorf("replicate adaptor: unexpected prediction status %q", prediction.Status)
		}
	}
}

func predictionErrorMessage(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var message string
	if err := common.Unmarshal(raw, &message); err == nil {
		return strings.TrimSpace(message)
	}
	var detail PredictionError
	if err := common.Unmarshal(raw, &detail); err == nil {
		if detail.Message != "" {
			return detail.Message
		}
		if detail.Detail != "" {
			return detail.Detail
		}
		if detail.Code != "" {
			return detail.Code
		}
	}
	return trimmed
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

func downloadImagesToBase64(urls []string) ([]string, error) {
	results := make([]string, 0, len(urls))
	for _, url := range urls {
		if strings.TrimSpace(url) == "" {
			continue
		}
		_, data, err := service.GetImageFromUrl(url)
		if err != nil {
			return nil, fmt.Errorf("replicate adaptor: failed to download image from %s: %w", common.MaskSensitiveInfo(url), err)
		}
		results = append(results, data)
	}
	return results, nil
}

func mapOpenAISizeToFlux(size string) (aspect string, width int, height int, ok bool) {
	parts := strings.Split(size, "x")
	if len(parts) != 2 {
		return "", 0, 0, false
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return "", 0, 0, false
	}

	switch {
	case w == h:
		return "1:1", 0, 0, true
	case w == 1792 && h == 1024:
		return "16:9", 0, 0, true
	case w == 1024 && h == 1792:
		return "9:16", 0, 0, true
	case w == 1536 && h == 1024:
		return "3:2", 0, 0, true
	case w == 1024 && h == 1536:
		return "2:3", 0, 0, true
	}

	rw, rh := reduceRatio(w, h)
	ratioStr := fmt.Sprintf("%d:%d", rw, rh)
	switch ratioStr {
	case "1:1", "16:9", "9:16", "3:2", "2:3", "4:5", "5:4", "3:4", "4:3":
		return ratioStr, 0, 0, true
	}

	width = normalizeFluxDimension(w)
	height = normalizeFluxDimension(h)
	return "custom", width, height, true
}

func reduceRatio(w, h int) (int, int) {
	g := gcd(w, h)
	if g == 0 {
		return w, h
	}
	return w / g, h / g
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

func normalizeFluxDimension(value int) int {
	const (
		minDim = 256
		maxDim = 1440
		step   = 32
	)
	if value < minDim {
		value = minDim
	}
	if value > maxDim {
		value = maxDim
	}
	remainder := value % step
	if remainder != 0 {
		if remainder >= step/2 {
			value += step - remainder
		} else {
			value -= remainder
		}
	}
	if value < minDim {
		value = minDim
	}
	if value > maxDim {
		value = maxDim
	}
	return value
}

func uploadFileFromForm(c *gin.Context, info *relaycommon.RelayInfo, fieldCandidates ...string) (string, error) {
	if info == nil {
		return "", errors.New("replicate adaptor: relay info is nil")
	}

	mf := c.Request.MultipartForm
	if mf == nil {
		if _, err := c.MultipartForm(); err != nil {
			return "", fmt.Errorf("replicate adaptor: parse multipart form failed: %w", err)
		}
		mf = c.Request.MultipartForm
	}
	if mf == nil || len(mf.File) == 0 {
		return "", nil
	}

	if len(fieldCandidates) == 0 {
		fieldCandidates = []string{"image", "image[]", "image_prompt"}
	}

	var fileHeader *multipart.FileHeader
	for _, key := range fieldCandidates {
		if files := mf.File[key]; len(files) > 0 {
			fileHeader = files[0]
			break
		}
	}
	if fileHeader == nil {
		for _, files := range mf.File {
			if len(files) > 0 {
				fileHeader = files[0]
				break
			}
		}
	}
	if fileHeader == nil {
		return "", nil
	}

	file, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("replicate adaptor: failed to open image file: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", fmt.Sprintf("form-data; name=\"content\"; filename=\"%s\"", fileHeader.Filename))
	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	hdr.Set("Content-Type", contentType)

	part, err := writer.CreatePart(hdr)
	if err != nil {
		writer.Close()
		return "", fmt.Errorf("replicate adaptor: create upload form failed: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		writer.Close()
		return "", fmt.Errorf("replicate adaptor: copy image content failed: %w", err)
	}
	formContentType := writer.FormDataContentType()
	writer.Close()

	baseURL := info.ChannelBaseUrl
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[constant.ChannelTypeReplicate]
	}
	uploadURL := relaycommon.GetFullRequestURL(baseURL, "/v1/files", info.ChannelType)

	req, err := http.NewRequestWithContext(info.GetRelayContext(nil), http.MethodPost, uploadURL, &body)
	if err != nil {
		return "", fmt.Errorf("replicate adaptor: create upload request failed: %w", service.SanitizeNetworkError(err))
	}
	req.Header.Set("Content-Type", formContentType)
	req.Header.Set("Authorization", "Bearer "+info.ApiKey)

	client, err := service.GetHttpClientWithProxy(info.ChannelSetting.Proxy)
	if err != nil {
		return "", fmt.Errorf("replicate adaptor: create upload proxy client failed: %w", err)
	}
	resp, err := service.DoUpstreamRequest(client, req)
	if err != nil {
		return "", fmt.Errorf("replicate adaptor: upload image failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := service.ReadResponseBodyWithLimit(resp.Body, 1<<20)
	if err != nil {
		return "", fmt.Errorf("replicate adaptor: read upload response failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("replicate adaptor: upload image failed with status %d", resp.StatusCode)
	}

	var uploadResp FileUploadResponse
	if err := common.Unmarshal(respBody, &uploadResp); err != nil {
		return "", fmt.Errorf("replicate adaptor: decode upload response failed: %w", err)
	}
	if uploadResp.Urls.Get == "" {
		return "", errors.New("replicate adaptor: upload response missing url")
	}
	return uploadResp.Urls.Get, nil
}

func (a *Adaptor) ConvertOpenAIRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeneralOpenAIRequest) (any, error) {
	return nil, errors.New("replicate adaptor: ConvertOpenAIRequest is not implemented")
}

func (a *Adaptor) ConvertRerankRequest(*gin.Context, int, dto.RerankRequest) (any, error) {
	return nil, errors.New("replicate adaptor: ConvertRerankRequest is not implemented")
}

func (a *Adaptor) ConvertEmbeddingRequest(*gin.Context, *relaycommon.RelayInfo, dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("replicate adaptor: ConvertEmbeddingRequest is not implemented")
}

func (a *Adaptor) ConvertAudioRequest(*gin.Context, *relaycommon.RelayInfo, dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("replicate adaptor: ConvertAudioRequest is not implemented")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(*gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("replicate adaptor: ConvertOpenAIResponsesRequest is not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("replicate adaptor: ConvertClaudeRequest is not implemented")
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("replicate adaptor: ConvertGeminiRequest is not implemented")
}
