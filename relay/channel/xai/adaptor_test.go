package xai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertImageRequestSupportsXAIJSONEdits(t *testing.T) {
	resolution := "2k"
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesEdits,
	}, dto.ImageRequest{
		Model:          "grok-imagine-image-quality",
		Prompt:         "render as a pencil sketch",
		Resolution:     &resolution,
		ResponseFormat: "url",
		Image:          []byte(`{"type":"image_url","url":"https://example.com/input.png"}`),
		StorageOptions: []byte(`{"filename":"edited.png","public_url":true}`),
		User:           []byte(`"customer_123"`),
	})

	require.NoError(t, err)
	request, ok := converted.(*dto.ImageRequest)
	require.True(t, ok)
	require.NotNil(t, request.N)
	assert.Equal(t, uint(1), *request.N)
	assert.Equal(t, 1, request.InputImageCount)
	assert.Equal(t, "2k", *request.Resolution)

	payload, err := common.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"model":"grok-imagine-image-quality",
		"prompt":"render as a pencil sketch",
		"n":1,
		"resolution":"2k",
		"response_format":"url",
		"image":{"type":"image_url","url":"https://example.com/input.png"},
		"storage_options":{"filename":"edited.png","public_url":true},
		"user":"customer_123"
	}`, string(payload))
}

func TestConvertImageRequestRejectsMultipartXAIEdit(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=test")

	_, err := (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesEdits,
	}, dto.ImageRequest{Model: "grok-imagine-image", Prompt: "edit"})

	require.Error(t, err)
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.ErrorContains(t, err, "application/json")
}

func TestConvertImageRequestPreservesXAIOutputControls(t *testing.T) {
	n := uint(dto.MaxXAIImageN)
	aspectRatio := "19.5:9"
	resolution := "2k"

	converted, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
	}, dto.ImageRequest{
		Model:          "grok-imagine-image-quality",
		Prompt:         "city skyline",
		N:              &n,
		AspectRatio:    &aspectRatio,
		Resolution:     &resolution,
		ResponseFormat: "b64_json",
	})

	require.NoError(t, err)
	request, ok := converted.(*dto.ImageRequest)
	require.True(t, ok)
	require.NotNil(t, request.N)
	assert.Equal(t, n, *request.N)
	assert.Equal(t, aspectRatio, *request.AspectRatio)
	assert.Equal(t, resolution, *request.Resolution)
	assert.Equal(t, "b64_json", request.ResponseFormat)
}

func TestConvertOpenAIRequestDoesNotOverwriteExplicitZeroMaxCompletionTokens(t *testing.T) {
	legacyMax := uint(4096)
	explicitZero := uint(0)
	request := &dto.GeneralOpenAIRequest{
		Model:               "grok-3-mini",
		MaxTokens:           &legacyMax,
		MaxCompletionTokens: &explicitZero,
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-3-mini"}}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)
	upstream, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, upstream.MaxCompletionTokens)
	assert.Zero(t, *upstream.MaxCompletionTokens)
	require.NotNil(t, upstream.MaxTokens)
	assert.Equal(t, legacyMax, *upstream.MaxTokens)
}
