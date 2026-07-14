package replicate

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplicateDoResponsePollsIncompletePrediction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var pollCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/predictions/prediction-1", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "poll-value", r.Header.Get("X-Replicate-Poll"))
		pollCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"prediction-1","status":"succeeded","output":["https://example.test/one.png","https://example.test/two.png"]}`)
	}))
	t.Cleanup(server.Close)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: server.URL,
			ApiKey:         "test-token",
			HeadersOverride: map[string]interface{}{
				"X-Replicate-Poll": "poll-value",
			},
		},
		Request:   &dto.ImageRequest{},
		PriceData: types.PriceData{UsePrice: true},
	}
	initial := `{"id":"prediction-1","status":"processing","output":null,"urls":{"get":"` + server.URL + `/v1/predictions/prediction-1"}}`
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(initial)),
	}

	usage, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 1, pollCount)
	assert.Equal(t, 2.0, info.PriceData.OtherRatios()["n"])

	var imageResponse dto.ImageResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &imageResponse))
	require.Len(t, imageResponse.Data, 2)
	assert.Equal(t, "https://example.test/one.png", imageResponse.Data[0].Url)
}

func TestReplicateDoResponseRejectsCrossOriginPollURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.replicate.com", ApiKey: "test-token"}}
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"id":"prediction-1","status":"starting","urls":{"get":"http://127.0.0.1/internal"}}`)),
	}

	_, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "configured channel origin")
}

func TestReplicateDoResponseAcceptsReadyFilesBeforeTerminalStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.replicate.com", ApiKey: "test-token"},
		Request:     &dto.ImageRequest{},
	}
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			`{"id":"prediction-1","status":"processing","output":["https://example.test/ready.png"]}`,
		)),
	}

	usage, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	var imageResponse dto.ImageResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &imageResponse))
	require.Len(t, imageResponse.Data, 1)
	assert.Equal(t, "https://example.test/ready.png", imageResponse.Data[0].Url)
}

func TestReplicateDoResponseRejectsOutputCountAboveBillingBound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	outputs := make([]string, dto.MaxImageN+1)
	for i := range outputs {
		outputs[i] = "https://example.test/image.png"
	}
	body, err := common.Marshal(map[string]any{
		"id":     "prediction-1",
		"status": "succeeded",
		"output": outputs,
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	resp := &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}
	_, apiErr := (&Adaptor{}).DoResponse(c, resp, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "maximum")
}

func TestReplicateConvertImageRequestReappliesValidatedOutputCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	validatedN := uint(3)
	request := dto.ImageRequest{
		Model:       ModelFlux11Pro,
		Prompt:      "a cat",
		N:           &validatedN,
		ExtraFields: []byte(`{"num_outputs":99}`),
		Extra: map[string]json.RawMessage{
			"input":       json.RawMessage(`{"num_outputs":88}`),
			"num_outputs": json.RawMessage(`77`),
		},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	converted, err := (&Adaptor{}).ConvertImageRequest(c, info, request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	var payload struct {
		Input map[string]any `json:"input"`
	}
	require.NoError(t, common.Unmarshal(body, &payload))
	assert.Equal(t, float64(3), payload.Input["num_outputs"])
}

func TestPredictionErrorMessageAcceptsProviderStringAndObject(t *testing.T) {
	assert.Equal(t, "model crashed", predictionErrorMessage([]byte(`"model crashed"`)))
	assert.Equal(t, "out of memory", predictionErrorMessage([]byte(`{"code":"E1001","detail":"out of memory"}`)))
	assert.Empty(t, predictionErrorMessage([]byte(`null`)))
}
