package helper

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAndValidXAIImageGenerationRequest(t *testing.T) {
	c := newImageRequestJSONContext(t, `{
		"model":"grok-imagine-image-quality",
		"prompt":"a city skyline",
		"n":10,
		"aspect_ratio":"20:9",
		"resolution":"2k",
		"response_format":"b64_json",
		"storage_options":{
			"filename":"city.jpg",
			"expires_after":7200,
			"public_url":{"expires_after":3600}
		}
	}`)

	request, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
	require.NoError(t, err)
	require.NotNil(t, request.N)
	assert.Equal(t, uint(dto.MaxXAIImageN), *request.N)
	require.NotNil(t, request.AspectRatio)
	assert.Equal(t, "20:9", *request.AspectRatio)
	require.NotNil(t, request.Resolution)
	assert.Equal(t, "2k", *request.Resolution)
	assert.Equal(t, float64(dto.MaxXAIImageN)*1.4, request.GetTokenCountMeta().BillingRatios[dto.XAIImageBillingRatioKey])
}

func TestValidateXAIImageRequestProtocol(t *testing.T) {
	one := uint(1)
	eleven := uint(dto.MaxXAIImageN + 1)
	validImage := []byte(`{"type":"image_url","url":"https://example.com/input.png"}`)
	validImages := []byte(`[
		{"url":"data:image/png;base64,YQ=="},
		{"file_id":"file_123"},
		{"type":"image_url","image_url":"https://example.com/third.webp"}
	]`)
	aspectRatio := "16:9"
	resolution := "2k"

	tests := []struct {
		name    string
		mode    int
		request dto.ImageRequest
		wantErr string
		inputs  int
	}{
		{
			name: "single URL edit",
			mode: relayconstant.RelayModeImagesEdits,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "edit", N: &one, Image: validImage,
			},
			inputs: 1,
		},
		{
			name: "mixed three-image edit",
			mode: relayconstant.RelayModeImagesEdits,
			request: dto.ImageRequest{
				Model: "grok-imagine-image-quality", Prompt: "combine", Images: validImages,
				AspectRatio: &aspectRatio, Resolution: &resolution,
			},
			inputs: 3,
		},
		{
			name: "batch limit",
			mode: relayconstant.RelayModeImagesGenerations,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "cat", N: &eleven,
			},
			wantErr: fmt.Sprintf("between 1 and %d", dto.MaxXAIImageN),
		},
		{
			name: "model spelling is exact",
			mode: relayconstant.RelayModeImagesGenerations,
			request: dto.ImageRequest{
				Model: " grok-imagine-image", Prompt: "cat",
			},
			wantErr: "is not supported",
		},
		{
			name: "single edit aspect ratio",
			mode: relayconstant.RelayModeImagesEdits,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "edit", Image: validImage, AspectRatio: &aspectRatio,
			},
			wantErr: "not supported for single-image",
		},
		{
			name: "one-element images array",
			mode: relayconstant.RelayModeImagesEdits,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "edit",
				Images: []byte(`[{"url":"https://example.com/input.png"}]`),
			},
			wantErr: "between 2 and 3",
		},
		{
			name: "four image inputs",
			mode: relayconstant.RelayModeImagesEdits,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "edit",
				Images: []byte(`[
					{"file_id":"file_1"},{"file_id":"file_2"},
					{"file_id":"file_3"},{"file_id":"file_4"}
				]`),
			},
			wantErr: "between 2 and 3",
		},
		{
			name: "ambiguous image reference",
			mode: relayconstant.RelayModeImagesEdits,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "edit",
				Image: []byte(`{"url":"https://example.com/input.png","file_id":"file_1"}`),
			},
			wantErr: "exactly one of url or file_id",
		},
		{
			name: "invalid data URI",
			mode: relayconstant.RelayModeImagesEdits,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "edit",
				Image: []byte(`{"url":"data:image/png;base64,"}`),
			},
			wantErr: "valid base64 image data URI",
		},
		{
			name: "unsupported data URI media type",
			mode: relayconstant.RelayModeImagesEdits,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "edit",
				Image: []byte(`{"url":"data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=="}`),
			},
			wantErr: "JPEG, PNG, or WebP",
		},
		{
			name: "URL credentials",
			mode: relayconstant.RelayModeImagesEdits,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "edit",
				Image: []byte(`{"url":"https://user:secret@example.com/input.png"}`),
			},
			wantErr: "HTTP(S) URL",
		},
		{
			name: "generation rejects image input",
			mode: relayconstant.RelayModeImagesGenerations,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "cat", Image: validImage,
			},
			wantErr: "only supported for xAI image edits",
		},
		{
			name: "unsupported OpenAI field",
			mode: relayconstant.RelayModeImagesGenerations,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "cat", Size: "1024x1024",
			},
			wantErr: "unsupported by the xAI image API",
		},
		{
			name: "unknown top-level field",
			mode: relayconstant.RelayModeImagesGenerations,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "cat",
				Extra: map[string]json.RawMessage{"future_option": []byte(`true`)},
			},
			wantErr: `unsupported field "future_option"`,
		},
		{
			name: "non-string user",
			mode: relayconstant.RelayModeImagesGenerations,
			request: dto.ImageRequest{
				Model: "grok-imagine-image", Prompt: "cat", User: []byte(`123`),
			},
			wantErr: "user must be a string",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateXAIImageRequest(&test.request, test.mode)
			if test.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.inputs, test.request.InputImageCount)
		})
	}
}

func TestValidateXAIStorageOptions(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "private permanent file", raw: `{"filename":"asset.png"}`},
		{name: "public URL boolean", raw: `{"filename":"asset.png","public_url":true}`},
		{name: "bounded expiries", raw: `{"filename":"asset.png","expires_after":7200,"public_url":{"expires_after":3600}}`},
		{name: "missing filename", raw: `{"public_url":true}`, wantErr: "filename is required"},
		{name: "file expiry too small", raw: `{"filename":"asset.png","expires_after":3599}`, wantErr: "between 3600 and 2592000"},
		{name: "public expiry too large", raw: `{"filename":"asset.png","public_url":{"expires_after":2592001}}`, wantErr: "between 3600 and 2592000"},
		{name: "public URL outlives file", raw: `{"filename":"asset.png","expires_after":3600,"public_url":{"expires_after":7200}}`, wantErr: "must not exceed"},
		{name: "unknown field", raw: `{"filename":"asset.png","bucket":"x"}`, wantErr: `unsupported field "bucket"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateXAIStorageOptions([]byte(test.raw))
			if test.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateOpenAIImageRequestRejectsXAIOnlyFields(t *testing.T) {
	resolution := "2k"
	request := &dto.ImageRequest{
		Model: "gpt-image-1", Prompt: "cat", Resolution: &resolution,
	}

	err := ValidateOpenAIImageRequest(request, relayconstant.RelayModeImagesGenerations, false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "xAI image parameters")
}
