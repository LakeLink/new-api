package ali

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
}

func TestConvertToAliRequestWan27I2VBuildsMediaFromImage(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:    "wan2.7-i2v",
		Prompt:   "animate the first frame",
		Image:    "https://example.com/first.png",
		Size:     "720p",
		Duration: 10,
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, "wan2.7-i2v", aliReq.Model)
	require.Equal(t, "720P", aliReq.Parameters.Resolution)
	require.Equal(t, 10, aliReq.Parameters.Duration)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
	}, aliReq.Input.Media)
	require.Empty(t, aliReq.Input.ImgURL)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"media"`)
	require.NotContains(t, string(body), `"img_url"`)
}

func TestConvertToAliRequestWan27I2VBuildsFirstAndLastFrameFromImages(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "interpolate between frames",
		Images: []string{
			"https://example.com/first.png",
			"https://example.com/last.png",
		},
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
		{Type: "last_frame", URL: "https://example.com/last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VPrefersImageBeforeImagesAndInputReference(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:          "wan2.7-i2v",
		Prompt:         "use the direct image",
		Image:          " https://example.com/direct.png ",
		Images:         []string{"https://example.com/images-first.png", " https://example.com/images-last.png "},
		InputReference: "https://example.com/input-reference.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/direct.png"},
		{Type: "last_frame", URL: "https://example.com/images-last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VFallsBackToFirstNonEmptyImage(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "skip blank images",
		Image:  " ",
		Images: []string{
			" ",
			" https://example.com/first.png ",
			" https://example.com/last.png ",
		},
		InputReference: "https://example.com/input-reference.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
		{Type: "last_frame", URL: "https://example.com/last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VKeepsExplicitMetadataMedia(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:          "wan2.7-i2v",
		Prompt:         "continue the clip",
		Image:          "https://example.com/direct.png",
		Images:         []string{"https://example.com/images-first.png", "https://example.com/images-last.png"},
		InputReference: "https://example.com/input-reference.png",
		Metadata: map[string]interface{}{
			"input": map[string]interface{}{
				"media": []interface{}{
					map[string]interface{}{
						"type": "first_clip",
						"url":  "https://example.com/input.mp4",
					},
				},
			},
		},
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_clip", URL: "https://example.com/input.mp4"},
	}, aliReq.Input.Media)
	require.Empty(t, aliReq.Input.ImgURL)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"media"`)
	require.NotContains(t, string(body), `"img_url"`)
}

func TestConvertToAliRequestWan27I2VRequiresMedia(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "animate without a frame",
	}

	_, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "requires image"))
}

func TestConvertToAliRequestWan25I2VKeepsLegacyImgURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.5-i2v-preview",
		Prompt: "animate the first frame",
		Image:  "https://example.com/first.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, "https://example.com/first.png", aliReq.Input.ImgURL)
	require.Empty(t, aliReq.Input.Media)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"img_url"`)
	require.NotContains(t, string(body), `"media"`)
}

func TestConvertToAliRequestBoundsMetadataDuration(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]interface{}
	}{
		{
			name: "duration above billing limit",
			metadata: map[string]interface{}{
				"parameters": map[string]interface{}{"duration": relaycommon.MaxTaskDurationSeconds + 1},
			},
		},
		{
			name: "negative duration",
			metadata: map[string]interface{}{
				"parameters": map[string]interface{}{"duration": -1},
			},
		},
		{
			name:     "null parameters",
			metadata: map[string]interface{}{"parameters": nil},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adaptor := &TaskAdaptor{}
			_, err := adaptor.convertToAliRequest(testRelayInfo(), relaycommon.TaskSubmitReq{
				Model:    "wan2.5-t2v-preview",
				Prompt:   "generate a video",
				Metadata: test.metadata,
			})

			require.Error(t, err)
		})
	}
}

func TestConvertToAliRequestAcceptsMaximumMetadataDuration(t *testing.T) {
	adaptor := &TaskAdaptor{}
	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), relaycommon.TaskSubmitReq{
		Model:  "wan2.5-t2v-preview",
		Prompt: "generate a video",
		Metadata: map[string]interface{}{
			"parameters": map[string]interface{}{"duration": relaycommon.MaxTaskDurationSeconds},
		},
	})

	require.NoError(t, err)
	require.Equal(t, relaycommon.MaxTaskDurationSeconds, aliReq.Parameters.Duration)
}

func TestConvertToAliRequestPreservesExplicitFalseAndZeroParameters(t *testing.T) {
	adaptor := &TaskAdaptor{}
	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), relaycommon.TaskSubmitReq{
		Model:  "wan2.5-t2v-preview",
		Prompt: "generate a video",
		Metadata: map[string]interface{}{
			"parameters": map[string]interface{}{
				"prompt_extend": false,
				"watermark":     false,
				"audio":         false,
				"seed":          0,
			},
		},
	})

	require.NoError(t, err)
	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"prompt_extend":false`)
	require.Contains(t, string(body), `"watermark":false`)
	require.Contains(t, string(body), `"audio":false`)
	require.Contains(t, string(body), `"seed":0`)
}

func TestConvertToAliRequestRejectsSeedOutsideProviderRange(t *testing.T) {
	adaptor := &TaskAdaptor{}
	_, err := adaptor.convertToAliRequest(testRelayInfo(), relaycommon.TaskSubmitReq{
		Model:  "wan2.5-t2v-preview",
		Prompt: "generate a video",
		Metadata: map[string]interface{}{
			"parameters": map[string]interface{}{"seed": int64(2147483648)},
		},
	})

	require.ErrorContains(t, err, "seed must be an integer between")
}

func TestValidateFinalRequestUsesMappedAliModelBeforePreconsume(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewBufferString(`{"model":"wan2.5-t2v-preview","prompt":"generate a video"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "wan2.5-t2v-preview"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(ctx, info))
	info.UpstreamModelName = "wan2.7-i2v"
	info.IsModelMapped = true
	taskErr := adaptor.ValidateFinalRequest(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.True(t, taskErr.LocalError)
	require.Error(t, taskErr.Error)
	assert.Contains(t, taskErr.Error.Error(), "requires image")
}
