package helper

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newImageRequestJSONContext(t *testing.T, body string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

// TestGetAndValidOpenAIImageRequestMultipartStream verifies multipart image
// edit parsing: the stream field is parsed and validated, and the request body
// stays replayable for the upstream request.
func TestGetAndValidOpenAIImageRequestMultipartStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newContext := func(t *testing.T, streamValue string, withImage bool) (*gin.Context, string) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("model", "gpt-image-1"))
		require.NoError(t, writer.WriteField("prompt", "edit this image"))
		require.NoError(t, writer.WriteField("stream", streamValue))
		if withImage {
			part, err := writer.CreateFormFile("image", "input.png")
			require.NoError(t, err)
			_, err = part.Write([]byte("fake image"))
			require.NoError(t, err)
		}
		require.NoError(t, writer.Close())
		originalBody := body.String()

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		return c, originalBody
	}

	t.Run("valid stream value keeps body replayable", func(t *testing.T) {
		c, originalBody := newContext(t, "true", true)

		req, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.NoError(t, err)
		require.NotNil(t, req.Stream)
		require.True(t, *req.Stream)
		require.True(t, req.IsStream(c.Request))

		bodyAfterValidation, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, originalBody, string(bodyAfterValidation))

		form, err := common.ParseMultipartFormReusable(c)
		require.NoError(t, err)
		require.Equal(t, "true", url.Values(form.Value).Get("stream"))
		require.Len(t, form.File["image"], 1)
	})

	t.Run("invalid stream value is rejected", func(t *testing.T) {
		c, _ := newContext(t, "notabool", false)

		_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid stream value")
	})
}

// TestGetAndValidOpenAIImageRequestNBounds guards the billing invariant that
// the image generation count can never reach quota calculation with a value
// large enough to overflow int64 into a negative charge.
func TestGetAndValidOpenAIImageRequestNBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newJSONContext := func(t *testing.T, body string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		return c
	}

	boundErr := fmt.Sprintf("n must be an integer between 1 and %d", dto.MaxImageN)

	tests := []struct {
		name    string
		body    string
		wantErr string
		wantN   uint
	}{
		{
			name:    "overflowed uint64 n is rejected",
			body:    `{"model":"gpt-image-1","prompt":"a cat","n":18446744073686646784}`,
			wantErr: boundErr,
		},
		{
			name:    "n above max is rejected",
			body:    fmt.Sprintf(`{"model":"gpt-image-1","prompt":"a cat","n":%d}`, dto.MaxImageN+1),
			wantErr: boundErr,
		},
		{
			name:  "OpenAI n at max is accepted",
			body:  fmt.Sprintf(`{"model":"gpt-image-1","prompt":"a cat","n":%d}`, dto.MaxOpenAIImageN),
			wantN: dto.MaxOpenAIImageN,
		},
		{
			name:  "custom provider keeps generic max",
			body:  fmt.Sprintf(`{"model":"custom-image-model","prompt":"a cat","n":%d}`, dto.MaxImageN),
			wantN: dto.MaxImageN,
		},
		{
			name:  "explicit n is accepted",
			body:  `{"model":"gpt-image-1","prompt":"a cat","n":3}`,
			wantN: 3,
		},
		{
			name:    "zero n is rejected",
			body:    `{"model":"gpt-image-1","prompt":"a cat","n":0}`,
			wantErr: boundErr,
		},
		{
			name:  "absent n defaults to 1",
			body:  `{"model":"gpt-image-1","prompt":"a cat"}`,
			wantN: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newJSONContext(t, tt.body)
			req, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, req.N)
			require.Equal(t, tt.wantN, *req.N)
			require.Equal(t, float64(tt.wantN), req.GetTokenCountMeta().BillingRatios["n"])
		})
	}

	t.Run("negative multipart n is rejected", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("model", "gpt-image-1"))
		require.NoError(t, writer.WriteField("prompt", "edit this image"))
		require.NoError(t, writer.WriteField("n", "-22904832"))
		require.NoError(t, writer.Close())

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())

		_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.Error(t, err)
		require.Contains(t, err.Error(), boundErr)
	})

	t.Run("zero multipart n is rejected", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("model", "gpt-image-1"))
		require.NoError(t, writer.WriteField("prompt", "edit this image"))
		require.NoError(t, writer.WriteField("n", "0"))
		require.NoError(t, writer.Close())

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())

		_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.Error(t, err)
		require.Contains(t, err.Error(), boundErr)
	})
}

func TestValidateOpenAIImageRequestProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name            string
		body            string
		path            string
		mode            int
		wantInputImages int
		wantErr         string
	}{
		{
			name:    "dall-e-3 only supports one image",
			body:    `{"model":"dall-e-3","prompt":"cat","n":2}`,
			path:    "/v1/images/generations",
			mode:    relayconstant.RelayModeImagesGenerations,
			wantErr: "n must be 1",
		},
		{
			name:    "GPT Image compression needs a compressed format",
			body:    `{"model":"gpt-image-1","prompt":"cat","output_format":"png","output_compression":50}`,
			path:    "/v1/images/generations",
			mode:    relayconstant.RelayModeImagesGenerations,
			wantErr: "output_compression requires",
		},
		{
			name:    "partials require streaming",
			body:    `{"model":"gpt-image-1","prompt":"cat","partial_images":1}`,
			path:    "/v1/images/generations",
			mode:    relayconstant.RelayModeImagesGenerations,
			wantErr: "requires stream=true",
		},
		{
			name: "explicit zero partials are valid without streaming",
			body: `{"model":"gpt-image-1","prompt":"cat","partial_images":0}`,
			path: "/v1/images/generations",
			mode: relayconstant.RelayModeImagesGenerations,
		},
		{
			name: "maximum partials are valid with streaming",
			body: `{"model":"gpt-image-2","prompt":"cat","stream":true,"partial_images":3}`,
			path: "/v1/images/generations",
			mode: relayconstant.RelayModeImagesGenerations,
		},
		{
			name:    "partials are bounded",
			body:    `{"model":"gpt-image-2","prompt":"cat","stream":true,"partial_images":4}`,
			path:    "/v1/images/generations",
			mode:    relayconstant.RelayModeImagesGenerations,
			wantErr: "between 0 and 3",
		},
		{
			name:    "GPT Image 2 rejects transparent background",
			body:    `{"model":"gpt-image-2","prompt":"cat","background":"transparent"}`,
			path:    "/v1/images/generations",
			mode:    relayconstant.RelayModeImagesGenerations,
			wantErr: "transparent background",
		},
		{
			name:    "transparent background rejects JPEG output",
			body:    `{"model":"gpt-image-1.5","prompt":"cat","background":"transparent","output_format":"jpeg"}`,
			path:    "/v1/images/generations",
			mode:    relayconstant.RelayModeImagesGenerations,
			wantErr: "requires output_format png or webp",
		},
		{
			name: "GPT Image 2 arbitrary dimensions",
			body: `{"model":"gpt-image-2","prompt":"cat","size":"3840x2160"}`,
			path: "/v1/images/generations",
			mode: relayconstant.RelayModeImagesGenerations,
		},
		{
			name:    "GPT Image 2 dimensions must be multiples of sixteen",
			body:    `{"model":"gpt-image-2","prompt":"cat","size":"1000x1000"}`,
			path:    "/v1/images/generations",
			mode:    relayconstant.RelayModeImagesGenerations,
			wantErr: "size is invalid",
		},
		{
			name:            "JSON edit accepts exact image references",
			body:            `{"model":"gpt-image-1","prompt":"edit","images":[{"file_id":"file_1"},{"image_url":"https://example.com/a.png"}]}`,
			path:            "/v1/images/edits",
			mode:            relayconstant.RelayModeImagesEdits,
			wantInputImages: 2,
		},
		{
			name:    "JSON edit reference is exclusive",
			body:    `{"model":"gpt-image-1","prompt":"edit","images":[{"file_id":"file_1","image_url":"https://example.com/a.png"}]}`,
			path:    "/v1/images/edits",
			mode:    relayconstant.RelayModeImagesEdits,
			wantErr: "exactly one",
		},
		{
			name:    "GPT Image 2 edit rejects input fidelity",
			body:    `{"model":"gpt-image-2","prompt":"edit","images":[{"file_id":"file_1"}],"input_fidelity":"high"}`,
			path:    "/v1/images/edits",
			mode:    relayconstant.RelayModeImagesEdits,
			wantErr: "input_fidelity is not supported",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
			c.Request.Header.Set("Content-Type", "application/json")
			request, err := GetAndValidOpenAIImageRequest(c, test.mode)
			if test.wantErr == "" {
				require.NoError(t, err)
				require.Equal(t, test.wantInputImages, request.InputImageCount)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestValidateResponsesImageGenerationTool(t *testing.T) {
	base := func(tool string, stream bool) *dto.OpenAIResponsesRequest {
		return &dto.OpenAIResponsesRequest{
			Model:  "gpt-5",
			Input:  []byte(`"draw a cat"`),
			Tools:  []byte("[" + tool + "]"),
			Stream: common.GetPointer(stream),
		}
	}
	tests := []struct {
		name    string
		request *dto.OpenAIResponsesRequest
		wantErr string
	}{
		{
			name:    "omitted partials",
			request: base(`{"type":"image_generation"}`, false),
		},
		{
			name:    "explicit zero partials",
			request: base(`{"type":"image_generation","partial_images":0}`, false),
		},
		{
			name:    "maximum partials",
			request: base(`{"type":"image_generation","model":"gpt-image-2","partial_images":3}`, true),
		},
		{
			name:    "partials require stream",
			request: base(`{"type":"image_generation","partial_images":1}`, false),
			wantErr: "requires stream=true",
		},
		{
			name:    "unknown image model",
			request: base(`{"type":"image_generation","model":"dall-e-3"}`, false),
			wantErr: "supported GPT Image model",
		},
		{
			name:    "transparent background rejects JPEG output",
			request: base(`{"type":"image_generation","model":"gpt-image-1.5","background":"transparent","output_format":"jpeg"}`, false),
			wantErr: "requires output_format png or webp",
		},
		{
			name:    "GPT Image 2 arbitrary dimensions",
			request: base(`{"type":"image_generation","model":"gpt-image-2","size":"2048x1024"}`, false),
		},
		{
			name:    "unknown image tool field",
			request: base(`{"type":"image_generation","seed":1}`, false),
			wantErr: "unsupported image_generation field",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateResponsesRequest(test.request)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestGetAndValidOpenAIImageRequestReconcilesNativeBatchSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	boundErr := fmt.Sprintf("batch_size must be an integer between 1 and %d", dto.MaxSiliconFlowImageBatchSize)

	for _, test := range []struct {
		name      string
		batchSize string
		wantErr   bool
		wantN     uint
	}{
		{name: "native count replaces standard count", batchSize: `4`, wantN: 4},
		{name: "null uses standard count", batchSize: `null`, wantN: 2},
		{name: "zero is rejected", batchSize: `0`, wantErr: true},
		{name: "fractional count is rejected", batchSize: `1.5`, wantErr: true},
		{name: "count above provider bound is rejected", batchSize: fmt.Sprintf("%d", dto.MaxSiliconFlowImageBatchSize+1), wantErr: true},
		{name: "count above shared bound is rejected", batchSize: fmt.Sprintf("%d", dto.MaxImageN+1), wantErr: true},
		{name: "wrapped negative unsigned value is rejected", batchSize: `18446744073686646784`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"model":"Kwai-Kolors/Kolors","prompt":"a cat","n":2,"batch_size":%s}`, test.batchSize)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(body))
			c.Request.Header.Set("Content-Type", "application/json")

			request, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
			if test.wantErr {
				require.ErrorContains(t, err, boundErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, request.N)
			require.Equal(t, test.wantN, *request.N)
			require.Equal(t, float64(test.wantN), request.GetTokenCountMeta().BillingRatios["n"])
		})
	}
}

func TestGetAndValidOpenAIImageRequestReconcilesReplicateOutputCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	boundErr := fmt.Sprintf("num_outputs must be an integer between 1 and %d", dto.MaxImageN)

	for _, test := range []struct {
		name    string
		body    string
		wantErr bool
		wantN   uint
	}{
		{name: "top-level native count", body: `{"model":"black-forest-labs/flux-1.1-pro","prompt":"cat","n":2,"num_outputs":3}`, wantN: 3},
		{name: "nested input count", body: `{"model":"black-forest-labs/flux-1.1-pro","prompt":"cat","n":2,"input":{"num_outputs":4}}`, wantN: 4},
		{name: "extra fields count", body: `{"model":"black-forest-labs/flux-1.1-pro","prompt":"cat","n":2,"extra_fields":{"num_outputs":5}}`, wantN: 5},
		{name: "top level deterministically wins", body: `{"model":"black-forest-labs/flux-1.1-pro","prompt":"cat","input":{"num_outputs":4},"num_outputs":6}`, wantN: 6},
		{name: "zero rejected", body: `{"model":"black-forest-labs/flux-1.1-pro","prompt":"cat","num_outputs":0}`, wantErr: true},
		{name: "fraction rejected", body: `{"model":"black-forest-labs/flux-1.1-pro","prompt":"cat","num_outputs":1.5}`, wantErr: true},
		{name: "shared bound enforced", body: fmt.Sprintf(`{"model":"black-forest-labs/flux-1.1-pro","prompt":"cat","num_outputs":%d}`, dto.MaxImageN+1), wantErr: true},
		{name: "wrapped negative unsigned rejected", body: `{"model":"black-forest-labs/flux-1.1-pro","prompt":"cat","num_outputs":18446744073686646784}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newImageRequestJSONContext(t, test.body)
			request, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
			if test.wantErr {
				require.ErrorContains(t, err, boundErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, request.N)
			require.Equal(t, test.wantN, *request.N)
			require.Equal(t, float64(test.wantN), request.GetTokenCountMeta().BillingRatios["n"])
		})
	}
}
