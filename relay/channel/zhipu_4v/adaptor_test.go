package zhipu_4v

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIRequestPreservesSupportedZhipuFields(t *testing.T) {
	topP := 1.0
	name := "assistant-name"
	prefix := true
	reasoning := "prior reasoning"
	request := &dto.GeneralOpenAIRequest{
		Model:           "glm-5.2",
		Messages:        []dto.Message{{Role: "assistant", Content: "answer", Name: &name, Prefix: &prefix, ReasoningContent: &reasoning}},
		TopP:            &topP,
		Stop:            []any{"first", "second"},
		ReasoningEffort: "high",
		ResponseFormat:  &dto.ResponseFormat{Type: "json_object"},
	}

	convertedAny, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)
	require.NoError(t, err)
	converted, ok := convertedAny.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, converted.TopP)
	assert.Equal(t, 1.0, *converted.TopP)
	assert.Equal(t, []string{"first", "second"}, converted.Stop)
	assert.Equal(t, "high", converted.ReasoningEffort)
	require.NotNil(t, converted.ResponseFormat)
	assert.Equal(t, "json_object", converted.ResponseFormat.Type)
	require.Len(t, converted.Messages, 1)
	assert.Equal(t, &name, converted.Messages[0].Name)
	assert.Equal(t, &prefix, converted.Messages[0].Prefix)
	assert.Equal(t, &reasoning, converted.Messages[0].ReasoningContent)
}

func TestConvertOpenAIRequestRejectsUnsupportedResponseFormat(t *testing.T) {
	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, &dto.GeneralOpenAIRequest{
		Model:          "glm-5.2",
		ResponseFormat: &dto.ResponseFormat{Type: "json_schema"},
	})

	require.ErrorContains(t, err, "unsupported")
}

func TestConvertImageRequestKeepsClientFormatLocal(t *testing.T) {
	watermark := true
	adaptor := &Adaptor{}
	converted, err := adaptor.ConvertImageRequest(nil, nil, dto.ImageRequest{
		Model:            "glm-image",
		Prompt:           "a fox",
		Quality:          "hd",
		Size:             "1280x1280",
		ResponseFormat:   "b64_json",
		WatermarkEnabled: []byte("true"),
		UserId:           []byte(`"user-123"`),
		Watermark:        &watermark,
	})
	require.NoError(t, err)
	assert.Equal(t, "b64_json", adaptor.imageResponseFormat)

	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "response_format")
	assert.Contains(t, string(body), `"watermark_enabled":true`)
	assert.Contains(t, string(body), `"user_id":"user-123"`)
}

func TestImageResponseHonorsRequestedRepresentationAndActualCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name           string
		responseFormat string
		upstreamData   string
		wantURL        string
		wantB64        string
	}{
		{
			name:           "url remains url without downloading",
			responseFormat: "url",
			upstreamData:   `{"url":"https://example.invalid/image.png"}`,
			wantURL:        "https://example.invalid/image.png",
		},
		{
			name:           "base64 remains base64",
			responseFormat: "b64_json",
			upstreamData:   `{"url":"https://example.invalid/image.png","b64_json":"encoded-image"}`,
			wantB64:        "encoded-image",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			info := &relaycommon.RelayInfo{
				RelayMode: relayconstant.RelayModeImagesGenerations,
				StartTime: time.Unix(1_700_000_000, 0),
				PriceData: types.PriceData{UsePrice: true},
			}
			adaptor := &Adaptor{imageResponseFormat: test.responseFormat}
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"created":1700000000,"data":[` + test.upstreamData + `]}`)),
			}

			usage, apiErr := adaptor.DoResponse(ctx, resp, info)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			var imageResponse dto.ImageResponse
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &imageResponse))
			require.Len(t, imageResponse.Data, 1)
			assert.Equal(t, test.wantURL, imageResponse.Data[0].Url)
			assert.Equal(t, test.wantB64, imageResponse.Data[0].B64Json)
			assert.Equal(t, 1.0, info.PriceData.OtherRatios()["n"])
		})
	}
}
