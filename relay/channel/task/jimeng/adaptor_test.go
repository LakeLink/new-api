package jimeng

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func jimengTestPNG(t *testing.T, width, height int) string {
	t.Helper()
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, width, height))))
	return base64.StdEncoding.EncodeToString(encoded.Bytes())
}

func TestGetModelListUsesCurrentOfficialJimengVideoModels(t *testing.T) {
	adaptor := &TaskAdaptor{}

	models := adaptor.GetModelList()

	assert.Equal(t, []string{
		jimengVideo30Pro,
		jimengVideo30T2V720,
		jimengVideo30I2VFirst720,
		jimengVideo30I2VFirstTail720,
		jimengVideo30I2VRecamera720,
		jimengVideo30T2V1080,
		jimengVideo30I2VFirst1080,
		jimengVideo30I2VFirstTail1080,
	}, models)
	assert.NotContains(t, models, legacyJimengT2V)
	assert.NotContains(t, models, legacyJimengI2V)

	models[0] = "mutated"
	assert.Equal(t, jimengVideo30Pro, adaptor.GetModelList()[0])
}

func TestConvertToRequestPayloadLocksJimengModelAndPrompt(t *testing.T) {
	adaptor := &TaskAdaptor{}
	request := relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat on a river",
		Duration: 10,
		Metadata: map[string]interface{}{
			"req_key": "a-different-priced-model",
			"prompt":  "a different prompt",
			"frames":  241,
		},
	}

	converted, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
	})

	require.NoError(t, err)
	assert.Equal(t, jimengVideo30T2V720, converted.ReqKey)
	assert.Equal(t, request.Prompt, converted.Prompt)
	require.NotNil(t, converted.Frames)
	assert.Equal(t, 241, *converted.Frames)
}

func TestConvertToRequestPayloadMapsCurrentJimengAliasesByImageCount(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		images   []string
		expected string
	}{
		{name: "720 text", model: jimengVideo30Alias, expected: jimengVideo30T2V720},
		{name: "720 first frame", model: jimengVideo30Alias, images: []string{"https://example.com/first.png"}, expected: jimengVideo30I2VFirst720},
		{name: "720 first and last frames", model: jimengVideo30Alias, images: []string{"https://example.com/first.png", "https://example.com/last.png"}, expected: jimengVideo30I2VFirstTail720},
		{name: "1080 text", model: jimengVideo301080Alias, expected: jimengVideo30T2V1080},
		{name: "1080 first frame", model: jimengVideo301080Alias, images: []string{"https://example.com/first.png"}, expected: jimengVideo30I2VFirst1080},
		{name: "1080 first and last frames", model: jimengVideo301080Alias, images: []string{"https://example.com/first.png", "https://example.com/last.png"}, expected: jimengVideo30I2VFirstTail1080},
		{name: "pro text", model: jimengVideo30ProAlias, expected: jimengVideo30Pro},
		{name: "pro image", model: jimengVideo30ProAlias, images: []string{"https://example.com/first.png"}, expected: jimengVideo30Pro},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := relaycommon.TaskSubmitReq{
				Prompt: "a paper boat on a river",
				Images: test.images,
			}

			converted, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: test.model},
			})

			require.NoError(t, err)
			assert.Equal(t, test.expected, converted.ReqKey)
		})
	}
}

func TestConvertToRequestPayloadValidatesCurrentJimengImageCounts(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		images      []string
		errorSubstr string
	}{
		{
			name:        "text model rejects image",
			model:       jimengVideo30T2V720,
			images:      []string{"https://example.com/first.png"},
			errorSubstr: "does not accept input images",
		},
		{
			name:        "first frame model requires image",
			model:       jimengVideo30I2VFirst720,
			errorSubstr: "requires exactly one input image",
		},
		{
			name:        "first frame model rejects two images",
			model:       jimengVideo30I2VFirst1080,
			images:      []string{"https://example.com/first.png", "https://example.com/last.png"},
			errorSubstr: "requires exactly one input image",
		},
		{
			name:        "first last model requires two images",
			model:       jimengVideo30I2VFirstTail720,
			images:      []string{"https://example.com/first.png"},
			errorSubstr: "requires exactly two input images",
		},
		{
			name:        "pro rejects two images",
			model:       jimengVideo30Pro,
			images:      []string{"https://example.com/first.png", "https://example.com/last.png"},
			errorSubstr: "supports at most one input image",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := relaycommon.TaskSubmitReq{
				Prompt: "a paper boat on a river",
				Images: test.images,
			}

			_, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: test.model},
			})

			require.ErrorContains(t, err, test.errorSubstr)
		})
	}
}

func TestConvertToRequestPayloadRejectsMixedJimengImageSources(t *testing.T) {
	tests := []relaycommon.TaskSubmitReq{
		{
			Prompt: "a paper boat on a river",
			Images: []string{"https://example.com/first.png", "aGVsbG8="},
		},
		{
			Prompt: "a paper boat on a river",
			Metadata: map[string]interface{}{
				"image_urls":         []string{"https://example.com/first.png"},
				"binary_data_base64": []string{"aGVsbG8="},
			},
		},
	}

	for index, request := range tests {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			_, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30Pro},
			})

			require.Error(t, err)
		})
	}
}

func TestConvertToRequestPayloadPreservesOptionalJimengScalarZeroValues(t *testing.T) {
	request := relaycommon.TaskSubmitReq{
		Prompt: "a paper boat on a river",
		Metadata: map[string]interface{}{
			"seed":         0,
			"aspect_ratio": "16:9",
		},
	}

	converted, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
	})
	require.NoError(t, err)
	require.NotNil(t, converted.Seed)
	assert.Equal(t, int64(0), *converted.Seed)
	require.NotNil(t, converted.AspectRatio)
	assert.Equal(t, "16:9", *converted.AspectRatio)

	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"seed":0`)
	assert.Contains(t, string(body), `"aspect_ratio":"16:9"`)

	withoutOptions, err := (&TaskAdaptor{}).convertToRequestPayload(
		&relaycommon.TaskSubmitReq{Prompt: request.Prompt},
		&relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
		},
	)
	require.NoError(t, err)
	body, err = common.Marshal(withoutOptions)
	require.NoError(t, err)
	assert.NotContains(t, string(body), `"seed"`)
	assert.NotContains(t, string(body), `"aspect_ratio"`)
}

func TestConvertToRequestPayloadValidatesCurrentJimengSeedRange(t *testing.T) {
	for _, seed := range []int64{-1, 0, 1<<31 - 1} {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) {
			request := relaycommon.TaskSubmitReq{
				Prompt:   "a paper boat on a river",
				Metadata: map[string]interface{}{"seed": seed},
			}

			converted, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
			})

			require.NoError(t, err)
			require.NotNil(t, converted.Seed)
			assert.Equal(t, seed, *converted.Seed)
		})
	}

	for _, seed := range []int64{-2, 1 << 31} {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) {
			request := relaycommon.TaskSubmitReq{
				Prompt:   "a paper boat on a river",
				Metadata: map[string]interface{}{"seed": seed},
			}

			_, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
			})

			require.ErrorContains(t, err, "seed must be between")
		})
	}
}

func TestConvertToRequestPayloadValidatesCurrentJimengBinaryImages(t *testing.T) {
	validPNG := jimengTestPNG(t, 320, 320)
	request := relaycommon.TaskSubmitReq{
		Prompt: "a paper boat on a river",
		Images: []string{"data:image/png;base64," + validPNG},
	}

	converted, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30I2VFirst720},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{validPNG}, converted.BinaryDataBase64)

	tests := []struct {
		name        string
		model       string
		images      []string
		errorSubstr string
	}{
		{
			name:        "invalid base64",
			model:       jimengVideo30I2VFirst720,
			images:      []string{"%%%"},
			errorSubstr: "invalid base64",
		},
		{
			name:        "not an image",
			model:       jimengVideo30I2VFirst720,
			images:      []string{base64.StdEncoding.EncodeToString([]byte("not an image"))},
			errorSubstr: "must contain a JPEG or PNG image",
		},
		{
			name:        "short edge below minimum",
			model:       jimengVideo30I2VFirst720,
			images:      []string{jimengTestPNG(t, 319, 320)},
			errorSubstr: "shortest edge must be at least 320",
		},
		{
			name:        "dimension above maximum",
			model:       jimengVideo30I2VFirst720,
			images:      []string{jimengTestPNG(t, 4097, 320)},
			errorSubstr: "must not exceed 4096x4096",
		},
		{
			name:        "aspect ratio above maximum",
			model:       jimengVideo30I2VFirst720,
			images:      []string{jimengTestPNG(t, 961, 320)},
			errorSubstr: "aspect ratio must not exceed 3:1",
		},
		{
			name:        "first and last ratio mismatch",
			model:       jimengVideo30I2VFirstTail720,
			images:      []string{jimengTestPNG(t, 320, 320), jimengTestPNG(t, 640, 320)},
			errorSubstr: "must use the same aspect ratio",
		},
		{
			name:        "file above maximum",
			model:       jimengVideo30I2VFirst720,
			images:      []string{strings.Repeat("A", base64.StdEncoding.EncodedLen(int(MaxFileSize))+4)},
			errorSubstr: "exceeds Jimeng's 4.7 MB limit",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
				Prompt: "a paper boat on a river",
				Images: test.images,
			}, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: test.model},
			})

			require.ErrorContains(t, err, test.errorSubstr)
		})
	}
}

func TestConvertToRequestPayloadBoundsCurrentJimengFrames(t *testing.T) {
	for _, frames := range []int{0, 120, 242, 1000000} {
		t.Run(strconv.Itoa(frames), func(t *testing.T) {
			request := relaycommon.TaskSubmitReq{
				Prompt: "a paper boat on a river",
				Metadata: map[string]interface{}{
					"frames": frames,
				},
			}

			_, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
			})

			require.ErrorContains(t, err, "frames must be either 121")
		})
	}
}

func TestConvertToRequestPayloadValidatesCurrentJimengAspectRatios(t *testing.T) {
	for _, ratio := range []string{"16:9", "4:3", "1:1", "3:4", "9:16", "21:9"} {
		t.Run(ratio, func(t *testing.T) {
			request := relaycommon.TaskSubmitReq{
				Prompt:   "a paper boat on a river",
				Metadata: map[string]interface{}{"aspect_ratio": ratio},
			}

			converted, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V1080},
			})

			require.NoError(t, err)
			require.NotNil(t, converted.AspectRatio)
			assert.Equal(t, ratio, *converted.AspectRatio)
		})
	}

	_, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat on a river",
		Metadata: map[string]interface{}{"aspect_ratio": "2:1"},
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
	})
	require.ErrorContains(t, err, "aspect_ratio is not supported")

	_, err = (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat on a river",
		Images:   []string{"https://example.com/first.png"},
		Metadata: map[string]interface{}{"aspect_ratio": "16:9"},
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30I2VFirst720},
	})
	require.ErrorContains(t, err, "aspect_ratio is only supported")
}

func TestConvertToRequestPayloadValidatesJimengRecameraFields(t *testing.T) {
	templates := []string{
		"hitchcock_dolly_in",
		"hitchcock_dolly_out",
		"robo_arm",
		"dynamic_orbit",
		"central_orbit",
		"crane_push",
		"quick_pull_back",
		"counterclockwise_swivel",
		"clockwise_swivel",
		"handheld",
		"rapid_push_pull",
	}
	for _, templateID := range templates {
		t.Run(templateID, func(t *testing.T) {
			request := relaycommon.TaskSubmitReq{
				Prompt: "a paper boat on a river",
				Images: []string{"https://example.com/first.png"},
				Metadata: map[string]interface{}{
					"template_id":     templateID,
					"camera_strength": "medium",
				},
			}

			converted, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30I2VRecamera720},
			})

			require.NoError(t, err)
			require.NotNil(t, converted.TemplateID)
			assert.Equal(t, templateID, *converted.TemplateID)
		})
	}

	tests := []struct {
		name        string
		metadata    map[string]interface{}
		errorSubstr string
	}{
		{
			name:        "missing template",
			metadata:    map[string]interface{}{"camera_strength": "medium"},
			errorSubstr: "template_id is required",
		},
		{
			name:        "invalid template",
			metadata:    map[string]interface{}{"template_id": "zoom", "camera_strength": "medium"},
			errorSubstr: "template_id is not supported",
		},
		{
			name:        "missing strength",
			metadata:    map[string]interface{}{"template_id": "handheld"},
			errorSubstr: "camera_strength is required",
		},
		{
			name:        "invalid strength",
			metadata:    map[string]interface{}{"template_id": "handheld", "camera_strength": "extreme"},
			errorSubstr: "camera_strength is not supported",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := relaycommon.TaskSubmitReq{
				Prompt:   "a paper boat on a river",
				Images:   []string{"https://example.com/first.png"},
				Metadata: test.metadata,
			}

			_, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30I2VRecamera720},
			})

			require.ErrorContains(t, err, test.errorSubstr)
		})
	}

	_, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat on a river",
		Metadata: map[string]interface{}{"template_id": "handheld", "camera_strength": "medium"},
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
	})
	require.ErrorContains(t, err, "only supported for")
}

func TestConvertToRequestPayloadBoundsJimengPromptByDocumentedModel(t *testing.T) {
	currentPrompt := strings.Repeat("梦", maxJimengPromptRunes)
	_, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt: currentPrompt,
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
	})
	require.NoError(t, err)

	_, err = (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt: currentPrompt + "境",
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
	})
	require.ErrorContains(t, err, "must not exceed 800 characters")

	_, err = (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt: strings.Repeat("a", 151),
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: legacyJimengT2V},
	})
	require.ErrorContains(t, err, "must not exceed 150 characters")

	_, err = (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt: currentPrompt + "境",
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "private_jimeng_model"},
	})
	require.NoError(t, err)
}

func TestConvertToRequestPayloadKeepsLegacyJimengModelsAsCompatibilityOnly(t *testing.T) {
	converted, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt: "a paper boat on a river",
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: legacyJimengT2V},
	})
	require.NoError(t, err)
	assert.Nil(t, converted.Frames)

	_, err = (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat on a river",
		Duration: 10,
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: legacyJimengT2V},
	})
	require.ErrorContains(t, err, "does not document 10-second generation")

	_, err = (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat on a river",
		Metadata: map[string]interface{}{"frames": 121},
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: legacyJimengT2V},
	})
	require.ErrorContains(t, err, "frames is not supported")

	_, err = (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt: "a paper boat on a river",
		Images: []string{"https://example.com/first.png"},
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: legacyJimengI2V},
	})
	require.ErrorContains(t, err, "aspect_ratio is required")

	converted, err = (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat on a river",
		Images:   []string{"https://example.com/first.png"},
		Metadata: map[string]interface{}{"aspect_ratio": "9:21"},
	}, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: legacyJimengI2V},
	})
	require.NoError(t, err)
	assert.Nil(t, converted.Frames)
}

func TestConvertToRequestPayloadRejectsUnsupportedJimengDuration(t *testing.T) {
	request := relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat on a river",
		Duration: 6,
	}

	_, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
	})

	require.ErrorContains(t, err, "duration must be either 5 or 10 seconds")
}

func TestValidateFinalRequestRejectsNativeJimengFramesBeforePreconsume(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewBufferString(`{"prompt":"a paper boat","metadata":{"frames":120}}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(ctx, info))
	info.UpstreamModelName = jimengVideo30T2V720
	taskErr := adaptor.ValidateFinalRequest(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.True(t, taskErr.LocalError)
}

func TestEstimateBillingNormalizesJimengFramesToDefaultDuration(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat",
		Metadata: map[string]interface{}{"frames": 241},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: jimengVideo30T2V720},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)

	assert.Equal(t, map[string]float64{"duration": 2}, ratios)
}

func TestBuildRequestBodyAndDoResponsePersistActualJimengReqKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a paper boat",
		Images: []string{"https://example.com/first.png"},
	})
	info := &relaycommon.RelayInfo{
		OriginModelName: "public-jimeng-model",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "ak|sk",
			UpstreamModelName: jimengVideo301080Alias,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}
	adaptor := &TaskAdaptor{}

	body, err := adaptor.BuildRequestBody(ctx, info)
	require.NoError(t, err)
	var submitted requestPayload
	require.NoError(t, common.DecodeJson(body, &submitted))
	assert.Equal(t, jimengVideo30I2VFirst1080, submitted.ReqKey)
	assert.Equal(t, jimengVideo30I2VFirst1080, ctx.GetString(jimengReqKeyContextKey))

	upstreamBody := `{"code":10000,"message":"success","data":{"task_id":"upstream-task"}}`
	storedTaskID, taskData, taskErr := adaptor.DoResponse(ctx, &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}, info)

	require.Nil(t, taskErr)
	assert.Equal(t, []byte(upstreamBody), taskData)
	reqKey, upstreamTaskID, err := decodeJimengTaskReference(storedTaskID)
	require.NoError(t, err)
	assert.Equal(t, jimengVideo30I2VFirst1080, reqKey)
	assert.Equal(t, "upstream-task", upstreamTaskID)

	var response dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "task_public", response.ID)
	assert.Equal(t, "task_public", response.TaskID)
	assert.Equal(t, "public-jimeng-model", response.Model)
}

func TestFetchTaskUsesPersistedJimengReqKey(t *testing.T) {
	type capturedRequest struct {
		method  string
		path    string
		action  string
		version string
		auth    string
		payload map[string]string
		err     error
	}
	captured := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		result := capturedRequest{
			method:  request.Method,
			path:    request.URL.Path,
			action:  request.URL.Query().Get("Action"),
			version: request.URL.Query().Get("Version"),
			auth:    request.Header.Get("Authorization"),
		}
		body, err := io.ReadAll(request.Body)
		if err == nil {
			result.payload = make(map[string]string)
			err = common.Unmarshal(body, &result.payload)
		}
		result.err = err
		captured <- result
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":10000,"data":{"status":"in_queue"}}`)
	}))
	defer server.Close()

	taskReference, err := encodeJimengTaskReference(jimengVideo30I2VFirst1080, "upstream-task")
	require.NoError(t, err)
	response, err := (&TaskAdaptor{}).FetchTask(
		context.Background(),
		server.URL+"/",
		"access-key|secret-key",
		map[string]any{"task_id": taskReference},
		"",
	)
	require.NoError(t, err)
	defer response.Body.Close()
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)

	result := <-captured
	require.NoError(t, result.err)
	assert.Equal(t, http.MethodPost, result.method)
	assert.Equal(t, "/", result.path)
	assert.Equal(t, "CVSync2AsyncGetResult", result.action)
	assert.Equal(t, "2022-08-31", result.version)
	assert.Contains(t, result.auth, "Credential=access-key/")
	assert.Equal(t, map[string]string{
		"req_key": jimengVideo30I2VFirst1080,
		"task_id": "upstream-task",
	}, result.payload)
}

func TestFetchTaskUsesOpenAIVideoEndpointForNestedNewAPIRelay(t *testing.T) {
	type capturedRequest struct {
		method string
		path   string
		auth   string
	}
	captured := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		captured <- capturedRequest{
			method: request.Method,
			path:   request.URL.Path,
			auth:   request.Header.Get("Authorization"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"nested-task","object":"video","status":"in_progress","progress":35}`)
	}))
	defer server.Close()

	taskReference, err := encodeJimengTaskReference(jimengVideo30T2V720, "nested-task")
	require.NoError(t, err)
	response, err := (&TaskAdaptor{}).FetchTask(
		context.Background(),
		server.URL+"/",
		"sk-nested",
		map[string]any{"task_id": taskReference},
		"",
	)
	require.NoError(t, err)
	defer response.Body.Close()
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)

	result := <-captured
	assert.Equal(t, http.MethodGet, result.method)
	assert.Equal(t, "/v1/videos/nested-task", result.path)
	assert.Equal(t, "Bearer sk-nested", result.auth)
}

func TestDecodeJimengTaskReferenceKeepsLegacyTaskCompatibility(t *testing.T) {
	reqKey, taskID, err := decodeJimengTaskReference("legacy-upstream-task")

	require.NoError(t, err)
	assert.Equal(t, legacyJimengT2V, reqKey)
	assert.Equal(t, "legacy-upstream-task", taskID)
}

func TestParseTaskResultHandlesOfficialJimengStatuses(t *testing.T) {
	tests := []struct {
		status           string
		expectedStatus   string
		expectedProgress string
		expectedReason   string
	}{
		{status: "in_queue", expectedStatus: string(model.TaskStatusQueued), expectedProgress: "10%"},
		{status: "generating", expectedStatus: string(model.TaskStatusInProgress)},
		{status: "done", expectedStatus: string(model.TaskStatusSuccess), expectedProgress: "100%"},
		{status: "not_found", expectedStatus: string(model.TaskStatusFailure), expectedProgress: "100%", expectedReason: "not_found"},
		{status: "expired", expectedStatus: string(model.TaskStatusFailure), expectedProgress: "100%", expectedReason: "expired"},
	}

	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			body := []byte(fmt.Sprintf(
				`{"code":10000,"data":{"status":%q,"video_url":"https://example.com/result.mp4"}}`,
				test.status,
			))

			result, err := (&TaskAdaptor{}).ParseTaskResult(body)

			require.NoError(t, err)
			assert.Equal(t, test.expectedStatus, result.Status)
			assert.Equal(t, test.expectedProgress, result.Progress)
			assert.Equal(t, test.expectedReason, result.Reason)
			assert.Equal(t, "https://example.com/result.mp4", result.Url)
		})
	}

	result, err := (&TaskAdaptor{}).ParseTaskResult(
		[]byte(`{"code":50412,"message":"invalid request","data":{"status":"in_queue"}}`),
	)
	require.NoError(t, err)
	assert.Equal(t, string(model.TaskStatusFailure), result.Status)
	assert.Equal(t, "invalid request", result.Reason)

	_, err = (&TaskAdaptor{}).ParseTaskResult(
		[]byte(`{"code":10000,"data":{"status":"new_unknown_status"}}`),
	)
	require.ErrorContains(t, err, "unknown Jimeng task status")
}

func TestParseTaskResultHandlesNestedOpenAIVideo(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{
		"id":"nested-task",
		"object":"video",
		"status":"in_progress",
		"progress":37,
		"metadata":{"url":"https://example.com/result.mp4"}
	}`))

	require.NoError(t, err)
	assert.Equal(t, string(model.TaskStatusInProgress), result.Status)
	assert.Equal(t, "37%", result.Progress)
	assert.Equal(t, "https://example.com/result.mp4", result.Url)
}

func TestConvertToOpenAIVideoPreservesNestedNewAPIRelayData(t *testing.T) {
	nested := dto.NewOpenAIVideo()
	nested.ID = "nested-task"
	nested.Model = "nested-model"
	nested.SetMetadata("url", "https://example.com/result.mp4")
	data, err := common.Marshal(nested)
	require.NoError(t, err)
	originTask := &model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		Progress:  "100%",
		CreatedAt: 100,
		UpdatedAt: 200,
		Properties: model.Properties{
			OriginModelName: "public-jimeng-model",
		},
		Data: data,
	}

	converted, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(originTask)
	require.NoError(t, err)
	var response dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(converted, &response))
	assert.Equal(t, "task_public", response.ID)
	assert.Equal(t, "task_public", response.TaskID)
	assert.Equal(t, "public-jimeng-model", response.Model)
	assert.Equal(t, dto.VideoStatusCompleted, response.Status)
	assert.Equal(t, 100, response.Progress)
	assert.Equal(t, int64(100), response.CreatedAt)
	assert.Equal(t, int64(200), response.CompletedAt)
	assert.Equal(t, "https://example.com/result.mp4", response.Metadata["url"])
}

func TestJimengRequestURLsAvoidDuplicateSlashes(t *testing.T) {
	adaptor := &TaskAdaptor{baseURL: "https://example.com/"}

	directURL, err := adaptor.BuildRequestURL(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "access|secret"},
	})
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31", directURL)

	nestedURL, err := adaptor.BuildRequestURL(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-nested"},
	})
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/jimeng/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31", nestedURL)
}

func TestJimengTaskSignerRejectsEmptyCredentials(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"https://visual.volcengineapi.com/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31",
		nil,
	)

	require.Error(t, (&TaskAdaptor{}).signRequest(request, "", "secret"))
	require.Error(t, (&TaskAdaptor{}).signRequest(request, "access", ""))
}
