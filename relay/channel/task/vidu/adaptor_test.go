package vidu

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertToRequestPayloadLocksViduModel(t *testing.T) {
	adaptor := &TaskAdaptor{}
	request := relaycommon.TaskSubmitReq{
		Prompt: "a train crossing a bridge",
		Metadata: map[string]interface{}{
			"model":    "a-different-priced-model",
			"duration": 10,
		},
	}

	converted, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2"},
	})

	require.NoError(t, err)
	assert.Equal(t, "viduq2", converted.Model)
	assert.Equal(t, 10, converted.Duration)
	assert.Equal(t, "720p", converted.Resolution)
}

func TestGetModelListMatchesCurrentViduVideoModels(t *testing.T) {
	models := (&TaskAdaptor{}).GetModelList()

	assert.Equal(t, []string{
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
	}, models)
}

func TestConvertToRequestPayloadValidatesViduDurationByModelAndAction(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		action    string
		duration  int
		wantError string
	}{
		{
			name:     "q3 turbo image minimum",
			model:    "viduq3-turbo",
			action:   constant.TaskActionGenerate,
			duration: 1,
		},
		{
			name:     "q3 pro start end maximum",
			model:    "viduq3-pro",
			action:   constant.TaskActionFirstTailGenerate,
			duration: 16,
		},
		{
			name:      "q3 pro image above maximum",
			model:     "viduq3-pro",
			action:    constant.TaskActionGenerate,
			duration:  17,
			wantError: "between 1 and 16 seconds",
		},
		{
			name:      "q3 turbo reference below minimum",
			model:     "viduq3-turbo",
			action:    constant.TaskActionReferenceGenerate,
			duration:  2,
			wantError: "between 3 and 16 seconds",
		},
		{
			name:     "q3 turbo reference minimum",
			model:    "viduq3-turbo",
			action:   constant.TaskActionReferenceGenerate,
			duration: 3,
		},
		{
			name:     "q3 mix reference supports one second",
			model:    "viduq3-mix",
			action:   constant.TaskActionReferenceGenerate,
			duration: 1,
		},
		{
			name:      "q3 reference rejects two seconds",
			model:     "viduq3",
			action:    constant.TaskActionReferenceGenerate,
			duration:  2,
			wantError: "between 3 and 16 seconds",
		},
		{
			name:     "q3 pro fast image maximum",
			model:    "viduq3-pro-fast",
			action:   constant.TaskActionGenerate,
			duration: 16,
		},
		{
			name:     "q2 image maximum",
			model:    "viduq2-turbo",
			action:   constant.TaskActionGenerate,
			duration: 10,
		},
		{
			name:      "q2 image above maximum",
			model:     "viduq2-pro",
			action:    constant.TaskActionGenerate,
			duration:  11,
			wantError: "between 1 and 10 seconds",
		},
		{
			name:     "q2 start end maximum",
			model:    "viduq2-pro",
			action:   constant.TaskActionFirstTailGenerate,
			duration: 8,
		},
		{
			name:      "q2 start end above maximum",
			model:     "viduq2-turbo",
			action:    constant.TaskActionFirstTailGenerate,
			duration:  9,
			wantError: "between 1 and 8 seconds",
		},
		{
			name:     "q2 pro fast start end maximum",
			model:    "viduq2-pro-fast",
			action:   constant.TaskActionFirstTailGenerate,
			duration: 8,
		},
		{
			name:     "q1 fixed duration",
			model:    "viduq1",
			action:   constant.TaskActionGenerate,
			duration: 5,
		},
		{
			name:      "q1 rejects variable duration",
			model:     "viduq1-classic",
			action:    constant.TaskActionGenerate,
			duration:  4,
			wantError: "duration must be 5 seconds",
		},
		{
			name:     "vidu 2 image supports eight seconds",
			model:    "vidu2.0",
			action:   constant.TaskActionGenerate,
			duration: 8,
		},
		{
			name:     "vidu 2 start end supports eight seconds",
			model:    "vidu2.0",
			action:   constant.TaskActionFirstTailGenerate,
			duration: 8,
		},
		{
			name:     "vidu 2 reference supports four seconds",
			model:    "vidu2.0",
			action:   constant.TaskActionReferenceGenerate,
			duration: 4,
		},
		{
			name:      "vidu 2 reference rejects eight seconds",
			model:     "vidu2.0",
			action:    constant.TaskActionReferenceGenerate,
			duration:  8,
			wantError: "duration must be 4 seconds",
		},
		{
			name:      "vidu 2 rejects other durations",
			model:     "vidu2.0",
			action:    constant.TaskActionGenerate,
			duration:  5,
			wantError: "duration must be either 4 or 8 seconds",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var images []string
			switch test.action {
			case constant.TaskActionGenerate:
				images = []string{"https://example.com/frame.png"}
			case constant.TaskActionFirstTailGenerate:
				images = []string{"https://example.com/start.png", "https://example.com/end.png"}
			case constant.TaskActionReferenceGenerate:
				images = []string{"https://example.com/reference.png"}
			}
			request := relaycommon.TaskSubmitReq{
				Prompt: "a train crossing a bridge",
				Images: images,
				Metadata: map[string]interface{}{
					"duration": test.duration,
				},
			}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: test.model},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{
					Action: test.action,
				},
			}

			payload, err := (&TaskAdaptor{}).convertToRequestPayload(&request, info)

			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				assert.Nil(t, payload)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.duration, payload.Duration)
			assert.Equal(t, test.model, payload.Model)
		})
	}
}

func TestConvertToRequestPayloadBoundsNativeViduDuration(t *testing.T) {
	for _, duration := range []int{-1, 0, 11, 1000000} {
		t.Run(strconv.Itoa(duration), func(t *testing.T) {
			adaptor := &TaskAdaptor{}
			request := relaycommon.TaskSubmitReq{
				Prompt: "a train crossing a bridge",
				Metadata: map[string]interface{}{
					"duration": duration,
				},
			}

			_, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2"},
			})

			require.ErrorContains(t, err, "duration must be between 1 and 10 seconds")
		})
	}
}

func TestConvertToRequestPayloadPreservesExplicitViduZeroValues(t *testing.T) {
	request := relaycommon.TaskSubmitReq{
		Prompt: "a train crossing a bridge",
		Metadata: map[string]interface{}{
			"seed":     0,
			"bgm":      false,
			"off_peak": false,
		},
	}

	converted, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2"},
	})

	require.NoError(t, err)
	require.NotNil(t, converted.Seed)
	assert.Zero(t, *converted.Seed)
	require.NotNil(t, converted.Bgm)
	assert.False(t, *converted.Bgm)
	require.NotNil(t, converted.OffPeak)
	assert.False(t, *converted.OffPeak)
	data, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"seed":0`)
	assert.Contains(t, string(data), `"bgm":false`)
	assert.Contains(t, string(data), `"off_peak":false`)
}

func TestBuildRequestBodyPreservesPricedViduReferenceModel(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a train crossing a bridge",
		Images: []string{
			"https://example.com/reference-1.png",
			"https://example.com/reference-2.png",
			"https://example.com/reference-3.png",
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2-pro"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionReferenceGenerate,
		},
	}

	body, err := (&TaskAdaptor{}).BuildRequestBody(context, info)

	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload requestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	assert.Equal(t, "viduq2-pro", payload.Model)
}

func TestBuildRequestBodyRejectsStaleViduTaskContextType(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", "stale-context-value")

	body, err := (&TaskAdaptor{}).BuildRequestBody(context, &relaycommon.RelayInfo{})

	require.ErrorContains(t, err, "invalid request type in context")
	assert.Nil(t, body)
}

func TestValidateFinalRequestRejectsMappedViduDurationBeforePreconsume(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewBufferString(`{"prompt":"a train","metadata":{"duration":11}}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(context, info))
	info.UpstreamModelName = "viduq2"
	info.IsModelMapped = true
	taskErr := adaptor.ValidateFinalRequest(context, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.True(t, taskErr.LocalError)
}

func TestValidateFinalRequestRechecksMappedViduActionDuration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a train",
		Images: []string{
			"https://example.com/start.png",
			"https://example.com/end.png",
		},
		Metadata: map[string]interface{}{
			"duration": 9,
			"model":    "viduq3-pro",
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "viduq2-pro",
			IsModelMapped:     true,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionFirstTailGenerate,
		},
	}

	taskErr := (&TaskAdaptor{}).ValidateFinalRequest(context, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.ErrorContains(t, taskErr.Error, "duration must be between 1 and 8 seconds")
}

func TestValidateFinalRequestRechecksMappedViduModelAction(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a train",
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "viduq2-turbo",
			IsModelMapped:     true,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionTextGenerate,
		},
	}

	taskErr := (&TaskAdaptor{}).ValidateFinalRequest(context, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.ErrorContains(t, taskErr.Error, "does not support action")

	info.UpstreamModelName = "viduq3-unknown"
	taskErr = (&TaskAdaptor{}).ValidateFinalRequest(context, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.ErrorContains(t, taskErr.Error, "unsupported Vidu model")
}

func TestViduMetadataDurationUsesSameValidatedBillingPayload(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "a train",
		Duration: 1,
		Metadata: map[string]interface{}{
			"duration": 10,
			"model":    "viduq2",
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "viduq3-pro",
			IsModelMapped:     true,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionTextGenerate,
		},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateFinalRequest(context, info))
	body, err := adaptor.BuildRequestBody(context, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload requestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	assert.Equal(t, "viduq3-pro", payload.Model)
	assert.Equal(t, 10, payload.Duration)

	ratios := adaptor.EstimateBilling(context, info)
	require.Contains(t, ratios, "provider_cost")
	// Q3 Pro 720p is 20 credits/second. The configured default is 5s,
	// while the final mapped and metadata-overridden request is 10s.
	assert.InDelta(t, 2.0, ratios["provider_cost"], 1e-12)
}

func TestVidu2MetadataDurationSelectsFinalResolutionForBodyAndBilling(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a train",
		Images: []string{"https://example.com/start.png"},
		Metadata: map[string]interface{}{
			"duration": 8,
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "vidu2.0"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionGenerate,
		},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateFinalRequest(context, info))
	body, err := adaptor.BuildRequestBody(context, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload requestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	assert.Equal(t, 8, payload.Duration)
	assert.Equal(t, "720p", payload.Resolution)

	ratios := adaptor.EstimateBilling(context, info)
	require.Contains(t, ratios, "provider_cost")
	// Vidu2.0 defaults to 4s/360p (20 credits); its 8s variant is
	// necessarily 720p and costs 100 credits.
	assert.InDelta(t, 5.0, ratios["provider_cost"], 1e-12)
}

func TestEstimateBillingUsesDocumentedViduCreditSchedule(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "a train",
		Duration: 10,
		Size:     "1920x1080",
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionTextGenerate,
		},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(context, info)

	// Q2 text-to-video: default 5s/720p costs 35 credits; 10s/1080p costs 110.
	require.Contains(t, ratios, "provider_cost")
	assert.InDelta(t, 110.0/35.0, ratios["provider_cost"], 1e-12)
}

func TestEstimateBillingSeparatesViduQ2AudioFromBgm(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a train",
		Images: []string{"https://example.com/frame.png"},
		Metadata: map[string]interface{}{
			"audio": true,
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2-turbo"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionGenerate,
		},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(context, info)

	// Q2-turbo image-to-video defaults to 40 credits; direct audio adds 15.
	require.Contains(t, ratios, "provider_cost")
	assert.InDelta(t, 55.0/40.0, ratios["provider_cost"], 1e-12)

	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a train",
		Images: []string{"https://example.com/frame.png"},
		Metadata: map[string]interface{}{
			"bgm": true,
		},
	})
	ratios = (&TaskAdaptor{}).EstimateBilling(context, info)

	// BGM keeps its own conservative reserve until submit credits reconcile it.
	require.Contains(t, ratios, "provider_cost")
	assert.InDelta(t, 55.0/40.0, ratios["provider_cost"], 1e-12)

	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a train",
		Images: []string{"https://example.com/frame.png"},
		Metadata: map[string]interface{}{
			"audio": true,
			"bgm":   true,
		},
	})
	ratios = (&TaskAdaptor{}).EstimateBilling(context, info)

	require.Contains(t, ratios, "provider_cost")
	assert.InDelta(t, 70.0/40.0, ratios["provider_cost"], 1e-12)
}

func TestValidateViduModelActionMatchesOfficialCapabilities(t *testing.T) {
	allActions := []string{
		constant.TaskActionTextGenerate,
		constant.TaskActionGenerate,
		constant.TaskActionFirstTailGenerate,
		constant.TaskActionReferenceGenerate,
	}
	allowedActions := map[string][]string{
		"viduq3-pro-fast": {constant.TaskActionGenerate},
		"viduq3-pro": {
			constant.TaskActionTextGenerate,
			constant.TaskActionGenerate,
			constant.TaskActionFirstTailGenerate,
		},
		"viduq3-turbo": allActions,
		"viduq3-mix":   {constant.TaskActionReferenceGenerate},
		"viduq3":       {constant.TaskActionReferenceGenerate},
		"viduq2-pro-fast": {
			constant.TaskActionGenerate,
			constant.TaskActionFirstTailGenerate,
		},
		"viduq2-pro": {
			constant.TaskActionGenerate,
			constant.TaskActionFirstTailGenerate,
			constant.TaskActionReferenceGenerate,
		},
		"viduq2-turbo": {
			constant.TaskActionGenerate,
			constant.TaskActionFirstTailGenerate,
		},
		"viduq2": {
			constant.TaskActionTextGenerate,
			constant.TaskActionReferenceGenerate,
		},
		"viduq1": allActions,
		"viduq1-classic": {
			constant.TaskActionGenerate,
			constant.TaskActionFirstTailGenerate,
		},
		"vidu2.0": {
			constant.TaskActionGenerate,
			constant.TaskActionFirstTailGenerate,
			constant.TaskActionReferenceGenerate,
		},
	}

	for modelName, modelActions := range allowedActions {
		t.Run(modelName, func(t *testing.T) {
			for _, action := range allActions {
				err := validateViduModelAction(modelName, action)
				if slices.Contains(modelActions, action) {
					assert.NoError(t, err)
				} else {
					assert.ErrorContains(t, err, "does not support action")
				}
			}
		})
	}

	for _, modelName := range []string{"vidu1.5", "viduq2-unknown", "viduq3-unknown", ""} {
		t.Run("unknown_"+modelName, func(t *testing.T) {
			assert.ErrorContains(
				t,
				validateViduModelAction(modelName, constant.TaskActionGenerate),
				"unsupported Vidu model",
			)
		})
	}
}

func TestValidateViduImagesAndPromptProtocolBoundaries(t *testing.T) {
	oneImage := []string{"https://example.com/1.png"}
	twoImages := []string{"https://example.com/1.png", "https://example.com/2.png"}
	sevenImages := make([]string, 7)
	for index := range sevenImages {
		sevenImages[index] = "https://example.com/" + strconv.Itoa(index) + ".png"
	}
	eightImages := append(append([]string{}, sevenImages...), "https://example.com/8.png")
	tests := []struct {
		name      string
		action    string
		images    []string
		prompt    string
		wantError string
	}{
		{name: "text accepts no images", action: constant.TaskActionTextGenerate, prompt: "train"},
		{name: "text rejects images", action: constant.TaskActionTextGenerate, images: oneImage, prompt: "train", wantError: "must not include images"},
		{name: "text requires prompt", action: constant.TaskActionTextGenerate, wantError: "prompt is required"},
		{name: "image accepts one", action: constant.TaskActionGenerate, images: oneImage},
		{name: "image rejects zero", action: constant.TaskActionGenerate, wantError: "exactly 1 image"},
		{name: "image rejects two", action: constant.TaskActionGenerate, images: twoImages, wantError: "exactly 1 image"},
		{name: "start end accepts two", action: constant.TaskActionFirstTailGenerate, images: twoImages},
		{name: "start end rejects one", action: constant.TaskActionFirstTailGenerate, images: oneImage, wantError: "exactly 2 images"},
		{name: "reference accepts one", action: constant.TaskActionReferenceGenerate, images: oneImage, prompt: "train"},
		{name: "reference accepts seven", action: constant.TaskActionReferenceGenerate, images: sevenImages, prompt: "train"},
		{name: "reference rejects zero", action: constant.TaskActionReferenceGenerate, prompt: "train", wantError: "between 1 and 7 images"},
		{name: "reference rejects eight", action: constant.TaskActionReferenceGenerate, images: eightImages, prompt: "train", wantError: "between 1 and 7 images"},
		{name: "reference requires prompt", action: constant.TaskActionReferenceGenerate, images: oneImage, wantError: "prompt is required"},
		{name: "empty image is rejected", action: constant.TaskActionGenerate, images: []string{" "}, wantError: "must not be empty"},
		{name: "five thousand unicode characters", action: constant.TaskActionTextGenerate, prompt: strings.Repeat("界", 5000)},
		{name: "prompt over character limit", action: constant.TaskActionTextGenerate, prompt: strings.Repeat("界", 5001), wantError: "5000 characters"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateViduImagesAndPrompt(test.action, test.images, test.prompt)
			if test.wantError == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestConvertToRequestPayloadRevalidatesMetadataImagesAndPrompt(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		action    string
		request   relaycommon.TaskSubmitReq
		wantError string
	}{
		{
			name:   "image metadata cannot replace one image with two",
			model:  "viduq3-pro",
			action: constant.TaskActionGenerate,
			request: relaycommon.TaskSubmitReq{
				Images: []string{"https://example.com/start.png"},
				Metadata: map[string]interface{}{
					"images": []string{"https://example.com/start.png", "https://example.com/end.png"},
				},
			},
			wantError: "exactly 1 image",
		},
		{
			name:   "start end metadata cannot remove an image",
			model:  "viduq2-pro",
			action: constant.TaskActionFirstTailGenerate,
			request: relaycommon.TaskSubmitReq{
				Images: []string{"https://example.com/start.png", "https://example.com/end.png"},
				Metadata: map[string]interface{}{
					"images": []string{"https://example.com/start.png"},
				},
			},
			wantError: "exactly 2 images",
		},
		{
			name:   "reference metadata enforces upper bound",
			model:  "viduq3",
			action: constant.TaskActionReferenceGenerate,
			request: relaycommon.TaskSubmitReq{
				Prompt: "train",
				Images: []string{"https://example.com/reference.png"},
				Metadata: map[string]interface{}{
					"images": []string{"1", "2", "3", "4", "5", "6", "7", "8"},
				},
			},
			wantError: "between 1 and 7 images",
		},
		{
			name:   "text metadata cannot inject images",
			model:  "viduq2",
			action: constant.TaskActionTextGenerate,
			request: relaycommon.TaskSubmitReq{
				Prompt: "train",
				Metadata: map[string]interface{}{
					"images": []string{"https://example.com/reference.png"},
				},
			},
			wantError: "must not include images",
		},
		{
			name:   "metadata prompt is checked after overlay",
			model:  "viduq2",
			action: constant.TaskActionTextGenerate,
			request: relaycommon.TaskSubmitReq{
				Prompt: "train",
				Metadata: map[string]interface{}{
					"prompt": strings.Repeat("界", 5001),
				},
			},
			wantError: "5000 characters",
		},
		{
			name:   "valid reference metadata remains supported",
			model:  "viduq3-mix",
			action: constant.TaskActionReferenceGenerate,
			request: relaycommon.TaskSubmitReq{
				Prompt: "train",
				Images: []string{"https://example.com/reference.png"},
				Metadata: map[string]interface{}{
					"images": []string{"1", "2", "3", "4", "5", "6", "7"},
					"prompt": "final prompt",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: test.model},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{
					Action: test.action,
				},
			}
			payload, err := (&TaskAdaptor{}).convertToRequestPayload(&test.request, info)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				assert.Nil(t, payload)
				return
			}
			require.NoError(t, err)
			assert.Len(t, payload.Images, 7)
			assert.Equal(t, "final prompt", payload.Prompt)
		})
	}
}

func TestConvertToRequestPayloadRejectsUnknownViduMetadata(t *testing.T) {
	request := relaycommon.TaskSubmitReq{
		Prompt: "train",
		Metadata: map[string]interface{}{
			"parameters": map[string]interface{}{"duration": 16},
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionTextGenerate,
		},
	}

	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&request, info)

	require.ErrorContains(t, err, `metadata field "parameters" is not supported`)
	assert.Nil(t, payload)

	request.Metadata = map[string]interface{}{
		"videos": []string{"https://example.com/reference.mp4"},
	}
	payload, err = (&TaskAdaptor{}).convertToRequestPayload(&request, info)

	require.ErrorContains(t, err, `metadata field "videos" is not supported`)
	assert.Nil(t, payload)
}

func TestConvertToRequestPayloadEnforcesFastModelResolution(t *testing.T) {
	for _, modelName := range []string{"viduq3-pro-fast", "viduq2-pro-fast"} {
		t.Run(modelName, func(t *testing.T) {
			request := relaycommon.TaskSubmitReq{
				Images: []string{"https://example.com/frame.png"},
				Size:   "540p",
			}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: modelName},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{
					Action: constant.TaskActionGenerate,
				},
			}

			payload, err := (&TaskAdaptor{}).convertToRequestPayload(&request, info)

			require.ErrorContains(t, err, `resolution "540p" is not supported`)
			assert.Nil(t, payload)

			request.Size = "720p"
			payload, err = (&TaskAdaptor{}).convertToRequestPayload(&request, info)
			require.NoError(t, err)
			assert.Equal(t, "720p", payload.Resolution)
		})
	}
}

func TestEstimateBillingUsesCanonicalViduCreditsAcrossActions(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		action    string
		images    []string
		wantRatio float64
	}{
		{
			name:      "vidu 2 image canonical",
			model:     "vidu2.0",
			action:    constant.TaskActionGenerate,
			images:    []string{"https://example.com/frame.png"},
			wantRatio: 1,
		},
		{
			name:      "vidu 2 reference costs four times image canonical",
			model:     "vidu2.0",
			action:    constant.TaskActionReferenceGenerate,
			images:    []string{"https://example.com/reference.png"},
			wantRatio: 4,
		},
		{
			name:      "q2 text canonical",
			model:     "viduq2",
			action:    constant.TaskActionTextGenerate,
			wantRatio: 1,
		},
		{
			name:      "q2 reference preserves action premium",
			model:     "viduq2",
			action:    constant.TaskActionReferenceGenerate,
			images:    []string{"https://example.com/reference.png"},
			wantRatio: 45.0 / 35.0,
		},
		{
			name:      "q3 turbo reference can be below canonical",
			model:     "viduq3-turbo",
			action:    constant.TaskActionReferenceGenerate,
			images:    []string{"https://example.com/reference.png"},
			wantRatio: 50.0 / 55.0,
		},
		{
			name:      "q2 pro reference remains relative to image canonical",
			model:     "viduq2-pro",
			action:    constant.TaskActionReferenceGenerate,
			images:    []string{"https://example.com/reference.png"},
			wantRatio: 50.0 / 55.0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Set("task_request", relaycommon.TaskSubmitReq{
				Prompt: "train",
				Images: test.images,
			})
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: test.model},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{
					Action: test.action,
				},
			}

			ratios := (&TaskAdaptor{}).EstimateBilling(context, info)

			require.Contains(t, ratios, "provider_cost")
			assert.InDelta(t, test.wantRatio, ratios["provider_cost"], 1e-12)
		})
	}
}

func TestViduCanonicalCreditsCoverEveryPublicModel(t *testing.T) {
	expected := map[string]float64{
		"viduq3-pro-fast": 100,
		"viduq3-turbo":    55,
		"viduq3-pro":      100,
		"viduq3-mix":      120,
		"viduq3":          60,
		"viduq2-pro-fast": 16,
		"viduq2-pro":      55,
		"viduq2-turbo":    40,
		"viduq2":          35,
		"viduq1":          80,
		"viduq1-classic":  80,
		"vidu2.0":         20,
	}

	for _, modelName := range (&TaskAdaptor{}).GetModelList() {
		credits, ok := viduCanonicalCredits(modelName)

		require.True(t, ok, modelName)
		assert.InDelta(t, expected[modelName], credits, 1e-12, modelName)
	}
	_, ok := viduCanonicalCredits("viduq3-unknown")
	assert.False(t, ok)
}

func TestEstimateBillingIncludesViduPromptRecommendationSurcharge(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Images: []string{"https://example.com/frame.png"},
		Metadata: map[string]interface{}{
			"is_rec": true,
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq3-pro"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionGenerate,
		},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(context, info)

	require.Contains(t, ratios, "provider_cost")
	assert.InDelta(t, 110.0/100.0, ratios["provider_cost"], 1e-12)
}

func TestEstimateBillingConservativelyReservesLegacyViduAudio(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Images: []string{"https://example.com/frame.png"},
		Metadata: map[string]interface{}{
			"audio":      true,
			"audio_type": "speech_only",
			"voice_id":   "voice-1",
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq1"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionGenerate,
		},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(context, info)

	require.Contains(t, ratios, "provider_cost")
	assert.InDelta(t, 95.0/80.0, ratios["provider_cost"], 1e-12)
}

func TestAdjustBillingOnSubmitUsesAuthoritativeViduCredits(t *testing.T) {
	adaptor := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq3-turbo"},
	}
	tests := []struct {
		name           string
		response       string
		estimatedRatio float64
		wantRatio      float64
	}{
		{name: "integer credits", response: `{"model":"viduq3-turbo","credits":27}`, estimatedRatio: 1, wantRatio: 27.0 / 55.0},
		{name: "quoted integer credits", response: `{"model":"viduq3-turbo","credits":"110"}`, estimatedRatio: 2, wantRatio: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info.PriceData.AddOtherRatio("provider_cost", test.estimatedRatio)
			info.PriceData.AddOtherRatio("preserved", 1.25)
			ratios := adaptor.AdjustBillingOnSubmit(info, []byte(test.response))

			require.Contains(t, ratios, "provider_cost")
			assert.InDelta(t, test.wantRatio, ratios["provider_cost"], 1e-12)
			assert.InDelta(t, 1.25, ratios["preserved"], 1e-12)
		})
	}
}

func TestAdjustBillingOnSubmitRejectsInvalidViduCredits(t *testing.T) {
	adaptor := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq3-turbo"},
	}
	info.PriceData.AddOtherRatio("provider_cost", 1)
	tests := []struct {
		name     string
		response string
	}{
		{name: "missing", response: `{}`},
		{name: "null", response: `{"credits":null}`},
		{name: "zero", response: `{"credits":0}`},
		{name: "negative", response: `{"credits":-1}`},
		{name: "fraction", response: `{"credits":1.5}`},
		{name: "scientific notation", response: `{"credits":1e2}`},
		{name: "non numeric", response: `{"credits":"many"}`},
		{name: "signed string", response: `{"credits":"+1"}`},
		{name: "spaced string", response: `{"credits":" 1 "}`},
		{name: "boolean", response: `{"credits":true}`},
		{name: "object", response: `{"credits":{"value":1}}`},
		{name: "array", response: `{"credits":[1]}`},
		{name: "over bound", response: `{"credits":475}`},
		{name: "integer overflow", response: `{"credits":"18446744073709551615"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Nil(t, adaptor.AdjustBillingOnSubmit(info, []byte(test.response)))
		})
	}
}

func TestParseViduCreditsAcceptsCurrentDocumentedMaximum(t *testing.T) {
	credits, err := parseViduCredits([]byte("474"))

	require.NoError(t, err)
	assert.Equal(t, 474, credits)

	credits, err = parseViduCredits([]byte("475"))
	require.ErrorContains(t, err, "credits must be between")
	assert.Zero(t, credits)
}

func TestAdjustBillingOnSubmitNeverRaisesPostAcceptViduCharge(t *testing.T) {
	adaptor := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq3-turbo"},
	}
	info.PriceData.AddOtherRatio("provider_cost", 1)

	ratios := adaptor.AdjustBillingOnSubmit(info, []byte(`{"model":"viduq3-turbo","credits":56}`))

	assert.Nil(t, ratios)
	assert.InDelta(t, 1.0, info.PriceData.OtherRatios()["provider_cost"], 1e-12)

	info.PriceData.RemoveOtherRatio("provider_cost")
	ratios = adaptor.AdjustBillingOnSubmit(info, []byte(`{"model":"viduq3-turbo","credits":55}`))
	assert.Nil(t, ratios)
}

func TestAdjustBillingOnSubmitRejectsMismatchedViduModel(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq3-turbo"},
	}
	info.PriceData.AddOtherRatio("provider_cost", 1)

	ratios := (&TaskAdaptor{}).AdjustBillingOnSubmit(
		info,
		[]byte(`{"model":"viduq3-pro","credits":50}`),
	)

	assert.Nil(t, ratios)
	assert.InDelta(t, 1.0, info.PriceData.OtherRatios()["provider_cost"], 1e-12)
}

func TestConvertToRequestPayloadValidatesSupportedViduOptions(t *testing.T) {
	style := " ANIME "
	aspectRatio := "1:1"
	request := relaycommon.TaskSubmitReq{
		Prompt: "train",
		Metadata: map[string]interface{}{
			"style":        style,
			"aspect_ratio": aspectRatio,
			"off_peak":     false,
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq1"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionTextGenerate,
		},
	}

	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&request, info)

	require.NoError(t, err)
	require.NotNil(t, payload.Style)
	assert.Equal(t, "anime", *payload.Style)
	require.NotNil(t, payload.AspectRatio)
	assert.Equal(t, "1:1", *payload.AspectRatio)
	require.NotNil(t, payload.OffPeak)
	assert.False(t, *payload.OffPeak)

	request = relaycommon.TaskSubmitReq{
		Images: []string{"https://example.com/frame.png"},
		Metadata: map[string]interface{}{
			"audio":      true,
			"audio_type": "speech_only",
			"voice_id":   "voice-1",
		},
	}
	info.ChannelMeta.UpstreamModelName = "viduq2-pro"
	info.TaskRelayInfo.Action = constant.TaskActionGenerate
	payload, err = (&TaskAdaptor{}).convertToRequestPayload(&request, info)

	require.NoError(t, err)
	require.NotNil(t, payload.Audio)
	assert.True(t, *payload.Audio)
	require.NotNil(t, payload.AudioType)
	assert.Equal(t, "speech_only", *payload.AudioType)
	require.NotNil(t, payload.VoiceID)
	assert.Equal(t, "voice-1", *payload.VoiceID)
}

func TestConvertToRequestPayloadRejectsUnsupportedViduOptionCombinations(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		action    string
		images    []string
		metadata  map[string]interface{}
		wantError string
	}{
		{
			name:      "style is ineffective on q2",
			model:     "viduq2",
			action:    constant.TaskActionTextGenerate,
			metadata:  map[string]interface{}{"style": "anime"},
			wantError: "style is supported only",
		},
		{
			name:      "aspect ratio is not accepted on image endpoint",
			model:     "viduq3-pro",
			action:    constant.TaskActionGenerate,
			images:    []string{"https://example.com/frame.png"},
			metadata:  map[string]interface{}{"aspect_ratio": "16:9"},
			wantError: "aspect_ratio is supported only",
		},
		{
			name:      "q1 does not accept portrait q2 ratio",
			model:     "viduq1",
			action:    constant.TaskActionTextGenerate,
			metadata:  map[string]interface{}{"aspect_ratio": "3:4"},
			wantError: "is not supported",
		},
		{
			name:      "is rec is not accepted on text endpoint",
			model:     "viduq3-pro",
			action:    constant.TaskActionTextGenerate,
			metadata:  map[string]interface{}{"is_rec": true},
			wantError: "is_rec is supported only",
		},
		{
			name:      "q3 rejects bgm",
			model:     "viduq3-pro",
			action:    constant.TaskActionGenerate,
			images:    []string{"https://example.com/frame.png"},
			metadata:  map[string]interface{}{"bgm": true},
			wantError: "bgm is not supported",
		},
		{
			name:      "audio type requires audio",
			model:     "viduq2-pro",
			action:    constant.TaskActionGenerate,
			images:    []string{"https://example.com/frame.png"},
			metadata:  map[string]interface{}{"audio_type": "all"},
			wantError: "requires audio=true",
		},
		{
			name:      "non q3 audio is rejected outside image and reference endpoints",
			model:     "viduq1",
			action:    constant.TaskActionTextGenerate,
			metadata:  map[string]interface{}{"audio": true},
			wantError: "audio is not supported",
		},
		{
			name:      "q3 mix rejects off peak",
			model:     "viduq3-mix",
			action:    constant.TaskActionReferenceGenerate,
			images:    []string{"https://example.com/reference.png"},
			metadata:  map[string]interface{}{"off_peak": true},
			wantError: "off_peak is not supported",
		},
		{
			name:      "callback must be absolute",
			model:     "viduq2",
			action:    constant.TaskActionTextGenerate,
			metadata:  map[string]interface{}{"callback_url": "/callback"},
			wantError: "absolute HTTP or HTTPS URL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := relaycommon.TaskSubmitReq{
				Prompt:   "train",
				Images:   test.images,
				Metadata: test.metadata,
			}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: test.model},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{
					Action: test.action,
				},
			}

			payload, err := (&TaskAdaptor{}).convertToRequestPayload(&request, info)

			require.ErrorContains(t, err, test.wantError)
			assert.Nil(t, payload)
		})
	}
}
